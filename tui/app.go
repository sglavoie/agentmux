package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const recoveryReexecEnv = "AMX_INTERNAL_DATASTORE_RECOVERY_REEXEC"

type recoveryProcessHandoff struct {
	Result        datastore.RecoveryResult `json:"result"`
	FinishRebuild bool                     `json:"finish_rebuild"`
}

// Run launches the dashboard TUI. It blocks until the user quits.
func Run(cfg *config.Config, client kitty.KittyClient) error {
	return RunFromWindow(cfg, client, 0)
}

// RunFromWindow launches the dashboard and initially selects the managed
// session in the Kitty tab containing sourceWindowID when one exists.
func RunFromWindow(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) error {
	handoff, err := consumeRecoveryProcessHandoff()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var finishRebuild func() error
	if handoff.FinishRebuild {
		finishRebuild = finishRebuildFromCurrentExecutable()
	} else {
		claimedGate, pendingResult, claimErr := datastore.ClaimDefaultPendingRebuildGate(ctx)
		if claimErr != nil {
			cancel()
			return fmt.Errorf("claiming pending datastore rebuild: %w", claimErr)
		}
		if claimedGate != nil {
			handoff = recoveryProcessHandoff{Result: pendingResult, FinishRebuild: true}
			finishRebuild = finishRebuildFromClaimedGate(claimedGate)
		}
	}
	services := NewDataServices()
	if finishRebuild != nil {
		if err := services.deferRebuildFinishAfterRecovery(finishRebuild); err != nil {
			cancel()
			return errors.Join(err, services.Close())
		}
	}
	m := newModelWithDataServices(ctx, cfg, client, sourceWindowID, ModeList, services)
	if handoff.Result.Replaced {
		// Preserve the one-shot guard and the operator-visible quarantine path
		// across exec while the fresh process performs the ordinary rebuild.
		m.dataServicesRecoveryState = dataServicesRecoverySucceeded
		m.dataServicesRecoveryResult = handoff.Result
	}
	p := tea.NewProgram(m, tea.WithContext(ctx))
	finalModel, runErr := p.Run()
	return finishDashboardRun(cancel, services, finalModel, runErr, reexecDashboard)
}

// RecoveryReexecPending reports the private one-shot marker early enough for
// the CLI entry point to bypass its existing-dashboard focus shortcut. The
// handoff is consumed only once RunFromWindow owns the daemon fallback.
func RecoveryReexecPending() bool {
	return strings.TrimSpace(os.Getenv(recoveryReexecEnv)) != ""
}

// AbortRecoveryReexec consumes the process handoff when the CLI cannot reach
// RunFromWindow (for example because configuration loading failed after exec).
// It deliberately leaves rebuild completion unconsumed: without compatibility
// imports and a checkpoint, reopening daemon startup would be unsafe.
func AbortRecoveryReexec() error {
	handoff, err := consumeRecoveryProcessHandoff()
	if err != nil {
		return err
	}
	if !handoff.FinishRebuild {
		return nil
	}
	return errors.New("datastore rebuild remains gated because fresh dashboard initialization did not start")
}

func consumeRecoveryProcessHandoff() (recoveryProcessHandoff, error) {
	raw := os.Getenv(recoveryReexecEnv)
	if strings.TrimSpace(raw) == "" {
		return recoveryProcessHandoff{}, nil
	}
	if err := os.Unsetenv(recoveryReexecEnv); err != nil {
		return recoveryProcessHandoff{}, fmt.Errorf("clearing datastore recovery process handoff: %w", err)
	}
	var handoff recoveryProcessHandoff
	if err := json.Unmarshal([]byte(raw), &handoff); err != nil {
		return recoveryProcessHandoff{}, fmt.Errorf("decoding datastore recovery process handoff: %w", err)
	}
	if !handoff.Result.Replaced {
		return recoveryProcessHandoff{}, errors.New("invalid datastore recovery process handoff: replacement is not marked complete")
	}
	return handoff, nil
}

func finishDashboardRun(
	cancel context.CancelFunc,
	services *DataServices,
	finalModel tea.Model,
	runErr error,
	reexec func(datastore.RecoveryResult, bool) error,
) error {
	cancel()
	m, ok := finalModel.(Model)
	if !ok || !m.dataServicesProcessRestartRequested {
		return errors.Join(runErr, services.Close())
	}

	finishRebuild := services.takeRebuildFinishForProcessHandoff()
	if closeErr := services.Close(); closeErr != nil {
		return errors.Join(runErr, closeErr)
	}
	if reexec == nil {
		return errors.Join(runErr, errors.New("restarting dashboard after datastore recovery: re-exec is unavailable; datastore rebuild remains gated"))
	}
	reexecErr := reexec(m.dataServicesRecoveryResult, finishRebuild != nil)
	if reexecErr == nil {
		reexecErr = errors.New("restarting dashboard after datastore recovery: re-exec returned without replacing the process")
	}
	return errors.Join(runErr, reexecErr, errors.New("datastore rebuild remains gated until a fresh dashboard completes its checkpoint"))
}

func reexecDashboard(result datastore.RecoveryResult, finishRebuild bool) error {
	handoff, err := json.Marshal(recoveryProcessHandoff{Result: result, FinishRebuild: finishRebuild})
	if err != nil {
		return fmt.Errorf("encoding datastore recovery process handoff: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving dashboard executable for datastore recovery: %w", err)
	}
	environ := make([]string, 0, len(os.Environ())+1)
	prefix := recoveryReexecEnv + "="
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			environ = append(environ, entry)
		}
	}
	environ = append(environ, prefix+string(handoff))
	if err := syscall.Exec(executable, os.Args, environ); err != nil {
		return fmt.Errorf("restarting dashboard after datastore recovery: %w", err)
	}
	return nil
}

type rebuildFinisher struct {
	finishGate      func() (bool, error)
	executable      func() (string, error)
	restart         func(string) error
	gateFinished    bool
	restartRequired bool
}

// finishRebuildFromCurrentExecutable returns one process-owned completion
// callback. The callback preserves its partial stage across Initialize retries:
// after gate removal succeeds, executable lookup or daemon spawn failures retry
// only the restart and never attempt to consume the removed gate again.
func finishRebuildFromCurrentExecutable() func() error {
	finisher := &rebuildFinisher{
		finishGate: datastore.FinishDefaultRebuildGate,
		executable: os.Executable,
		restart:    daemon.SpawnDetached,
	}
	return finisher.finish
}

func finishRebuildFromClaimedGate(gate *datastore.RebuildGate) func() error {
	finisher := &rebuildFinisher{
		finishGate: gate.Finish,
		executable: os.Executable,
		restart:    daemon.SpawnDetached,
	}
	return finisher.finish
}

func (f *rebuildFinisher) finish() error {
	if !f.gateFinished {
		restartDaemon, err := f.finishGate()
		if err != nil {
			return fmt.Errorf("finishing datastore rebuild gate: %w", err)
		}
		f.gateFinished = true
		f.restartRequired = restartDaemon
	}
	if !f.restartRequired {
		return nil
	}
	executable, err := f.executable()
	if err != nil {
		return fmt.Errorf("resolving daemon executable after datastore recovery: %w", err)
	}
	if err := f.restart(executable); err != nil {
		return fmt.Errorf("restarting daemon after datastore recovery: %w", err)
	}
	f.restartRequired = false
	return nil
}
