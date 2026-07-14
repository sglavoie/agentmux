package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/datastore"
)

const (
	recoveryGateHelperModeEnv = "AMX_TEST_RECOVERY_GATE_OWNER_MODE"
	recoveryGateHelperPathEnv = "AMX_TEST_RECOVERY_GATE_OWNER_PATH"
)

func TestRecoveryProcessHandoffIsOneShot(t *testing.T) {
	want := recoveryProcessHandoff{
		Result: datastore.RecoveryResult{
			Replaced:      true,
			DatabasePath:  filepath.Join(t.TempDir(), "agent-mux.db"),
			QuarantineDir: filepath.Join(t.TempDir(), "recovery", "now"),
		},
		FinishRebuild: true,
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal handoff: %v", err)
	}
	t.Setenv(recoveryReexecEnv, string(raw))
	if !RecoveryReexecPending() {
		t.Fatal("handoff marker was not visible to the CLI entry point")
	}
	got, err := consumeRecoveryProcessHandoff()
	if err != nil {
		t.Fatalf("consume handoff: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("handoff = %#v, want %#v", got, want)
	}
	if RecoveryReexecPending() {
		t.Fatal("handoff marker survived consumption and could create an exec loop")
	}
	second, err := consumeRecoveryProcessHandoff()
	if err != nil || !reflect.DeepEqual(second, recoveryProcessHandoff{}) {
		t.Fatalf("second consume = %#v, %v; want empty handoff", second, err)
	}
}

func TestFinishDashboardRun_ReexecFailureKeepsRebuildGateAndDaemonStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var trace []string
	services := newDataServices(dataServicesDependencies{})
	if err := services.deferRebuildFinishAfterRecovery(func() error {
		trace = append(trace, "daemon-restart")
		return nil
	}); err != nil {
		t.Fatalf("defer daemon restart: %v", err)
	}
	result := datastore.RecoveryResult{Replaced: true, QuarantineDir: "/tmp/recovery/now"}
	reexecErr := errors.New("exec failed")
	err := finishDashboardRun(cancel, services, Model{
		dataServicesProcessRestartRequested: true,
		dataServicesRecoveryResult:          result,
	}, nil, func(got datastore.RecoveryResult, restartDaemon bool) error {
		trace = append(trace, "reexec")
		if !reflect.DeepEqual(got, result) || !restartDaemon {
			t.Fatalf("reexec handoff = %#v restart %v, want %#v restart true", got, restartDaemon, result)
		}
		services.stateMu.RLock()
		closed := services.closed
		services.stateMu.RUnlock()
		if !closed {
			t.Fatal("reexec ran before the old data services closed")
		}
		return reexecErr
	})
	if !errors.Is(err, reexecErr) {
		t.Fatalf("finish error = %v, want exec failure", err)
	}
	if ctx.Err() == nil {
		t.Fatal("model context was not canceled before reexec")
	}
	if got := strings.Join(trace, ","); got != "reexec" {
		t.Fatalf("handoff trace = %q, want reexec with no daemon restart", got)
	}
	if err := services.Close(); err != nil || len(trace) != 1 {
		t.Fatalf("second close = %v trace %v; failed re-exec must keep daemon stopped", err, trace)
	}
}

func TestFreshProcessCloseKeepsTransferredRebuildGateAndDaemonStopped(t *testing.T) {
	var restarts int
	services := newDataServices(dataServicesDependencies{})
	if err := services.deferRebuildFinishAfterRecovery(func() error {
		restarts++
		return nil
	}); err != nil {
		t.Fatalf("receive daemon restart: %v", err)
	}
	if err := services.Close(); err == nil || !strings.Contains(err.Error(), "rebuild remains gated") {
		t.Fatalf("close fresh process services = %v, want gated rebuild error", err)
	}
	if restarts != 0 {
		t.Fatalf("fresh-process fallback restarts = %d, want 0", restarts)
	}
}

func TestRecoveryReexecFailureKeepsGateAcrossProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), datastore.DatabaseFileName)
	runRecoveryGateOwnerProcess(t, "reexec-failure", path)
	assertPendingRebuildBlocksUntilFreshOwnerFinishes(t, path)
}

func TestRecoveryConfigFailureKeepsGateAcrossProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), datastore.DatabaseFileName)
	runRecoveryGateOwnerProcess(t, "config-failure", path)
	assertPendingRebuildBlocksUntilFreshOwnerFinishes(t, path)
}

func TestRecoveryGateOwnerProcessHelper(t *testing.T) {
	mode := os.Getenv(recoveryGateHelperModeEnv)
	if mode == "" {
		return
	}
	path := os.Getenv(recoveryGateHelperPathEnv)
	if path == "" {
		t.Fatal("helper datastore path is empty")
	}
	gate, err := datastore.BeginRebuildGate(path)
	if err != nil {
		t.Fatalf("begin helper rebuild gate: %v", err)
	}
	if err := gate.SetRestartDaemon(true); err != nil {
		t.Fatalf("record helper restart intent: %v", err)
	}
	result := datastore.RecoveryResult{
		Replaced:      true,
		DatabasePath:  path,
		QuarantineDir: filepath.Join(filepath.Dir(path), "recovery", "now"),
	}
	if err := gate.MarkRebuildPending(result); err != nil {
		t.Fatalf("mark helper rebuild pending: %v", err)
	}

	switch mode {
	case "reexec-failure":
		ctx, cancel := context.WithCancel(t.Context())
		services := newDataServices(dataServicesDependencies{})
		if err := services.deferRebuildFinishAfterRecovery(func() error {
			_, err := gate.Finish()
			return err
		}); err != nil {
			t.Fatalf("defer helper rebuild finish: %v", err)
		}
		reexecErr := errors.New("simulated exec failure")
		err := finishDashboardRun(cancel, services, Model{
			dataServicesProcessRestartRequested: true,
			dataServicesRecoveryResult:          result,
		}, nil, func(datastore.RecoveryResult, bool) error {
			return reexecErr
		})
		if !errors.Is(err, reexecErr) || ctx.Err() == nil {
			t.Fatalf("helper reexec failure = %v context %v", err, ctx.Err())
		}
	case "config-failure":
		raw, err := json.Marshal(recoveryProcessHandoff{Result: result, FinishRebuild: true})
		if err != nil {
			t.Fatalf("marshal helper handoff: %v", err)
		}
		if err := os.Setenv(recoveryReexecEnv, string(raw)); err != nil {
			t.Fatalf("set helper handoff: %v", err)
		}
		if err := AbortRecoveryReexec(); err == nil || !strings.Contains(err.Error(), "remains gated") {
			t.Fatalf("abort helper recovery reexec = %v, want gated error", err)
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func runRecoveryGateOwnerProcess(t *testing.T, mode, path string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryGateOwnerProcessHelper$")
	cmd.Env = append(os.Environ(),
		recoveryGateHelperModeEnv+"="+mode,
		recoveryGateHelperPathEnv+"="+path,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("recovery gate owner helper %s: %v\n%s", mode, err, output)
	}
}

func assertPendingRebuildBlocksUntilFreshOwnerFinishes(t *testing.T, path string) {
	t.Helper()
	if _, err := datastore.BeginDaemonStartup(t.Context(), path); !errors.Is(err, datastore.ErrRebuildInProgress) {
		t.Fatalf("daemon startup after owner exit = %v, want ErrRebuildInProgress", err)
	}
	if active, err := datastore.RebuildGateActive(path); err != nil || !active {
		t.Fatalf("pending gate after owner exit = %v, %v; want active", active, err)
	}

	claimed, result, err := datastore.ClaimPendingRebuildGate(t.Context(), path)
	if err != nil || claimed == nil {
		t.Fatalf("fresh owner claim = gate %v, error %v", claimed != nil, err)
	}
	if !result.Replaced || filepath.Clean(result.DatabasePath) != filepath.Clean(path) {
		t.Fatalf("claimed recovery result = %+v", result)
	}
	store, err := datastore.OpenPath(t.Context(), path)
	if err != nil {
		t.Fatalf("fresh owner open: %v", err)
	}
	if err := store.Checkpoint(t.Context()); err != nil {
		_ = store.Close()
		t.Fatalf("fresh owner checkpoint: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("fresh owner close: %v", err)
	}
	restart, err := claimed.Finish()
	if err != nil || !restart {
		t.Fatalf("fresh owner finish = restart %v, error %v", restart, err)
	}
	startup, err := datastore.BeginDaemonStartup(t.Context(), path)
	if err != nil {
		t.Fatalf("daemon startup after successful finalization: %v", err)
	}
	if err := startup.Release(); err != nil {
		t.Fatalf("release daemon startup after successful finalization: %v", err)
	}
	if active, err := datastore.RebuildGateActive(path); err != nil || active {
		t.Fatalf("finished rebuild gate = %v, %v; want inactive", active, err)
	}
}
