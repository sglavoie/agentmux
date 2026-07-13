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
	RestartDaemon bool                     `json:"restart_daemon"`
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
	services := NewDataServices()
	if handoff.RestartDaemon {
		if err := services.deferDaemonRestartAfterRecovery(restartDaemonFromCurrentExecutable); err != nil {
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

// AbortRecoveryReexec restores daemon liveness when the CLI cannot reach
// RunFromWindow (for example because configuration loading failed after exec).
// It consumes the marker so an enclosing process cannot retry the handoff.
func AbortRecoveryReexec() error {
	handoff, err := consumeRecoveryProcessHandoff()
	if err != nil {
		return err
	}
	if !handoff.RestartDaemon {
		return nil
	}
	return restartDaemonFromCurrentExecutable()
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

	restartDaemon := services.takeDaemonRestartForProcessHandoff()
	if closeErr := services.Close(); closeErr != nil {
		return errors.Join(runErr, closeErr, invokeRecoveryRestart(restartDaemon))
	}
	if reexec == nil {
		return errors.Join(runErr, errors.New("restarting dashboard after datastore recovery: re-exec is unavailable"), invokeRecoveryRestart(restartDaemon))
	}
	reexecErr := reexec(m.dataServicesRecoveryResult, restartDaemon != nil)
	if reexecErr == nil {
		reexecErr = errors.New("restarting dashboard after datastore recovery: re-exec returned without replacing the process")
	}
	return errors.Join(runErr, reexecErr, invokeRecoveryRestart(restartDaemon))
}

func invokeRecoveryRestart(restart func() error) error {
	if restart == nil {
		return nil
	}
	return restart()
}

func reexecDashboard(result datastore.RecoveryResult, restartDaemon bool) error {
	handoff, err := json.Marshal(recoveryProcessHandoff{Result: result, RestartDaemon: restartDaemon})
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

func restartDaemonFromCurrentExecutable() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving daemon executable after datastore recovery: %w", err)
	}
	if err := daemon.SpawnDetached(executable); err != nil {
		return fmt.Errorf("restarting daemon after datastore recovery: %w", err)
	}
	return nil
}
