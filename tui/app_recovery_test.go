package tui

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/datastore"
)

func TestRecoveryProcessHandoffIsOneShot(t *testing.T) {
	want := recoveryProcessHandoff{
		Result: datastore.RecoveryResult{
			Replaced:      true,
			DatabasePath:  filepath.Join(t.TempDir(), "agent-mux.db"),
			QuarantineDir: filepath.Join(t.TempDir(), "recovery", "now"),
		},
		RestartDaemon: true,
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

func TestFinishDashboardRunClosesBeforeReexecAndRestartsDaemonOnFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var trace []string
	services := newDataServices(dataServicesDependencies{})
	if err := services.deferDaemonRestartAfterRecovery(func() error {
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
	if got := strings.Join(trace, ","); got != "reexec,daemon-restart" {
		t.Fatalf("handoff trace = %q, want reexec,daemon-restart", got)
	}
	if err := services.Close(); err != nil || len(trace) != 2 {
		t.Fatalf("second close = %v trace %v; daemon restart must remain exactly once", err, trace)
	}
}

func TestFreshProcessCloseOwnsTransferredDaemonRestart(t *testing.T) {
	var restarts int
	services := newDataServices(dataServicesDependencies{})
	if err := services.deferDaemonRestartAfterRecovery(func() error {
		restarts++
		return nil
	}); err != nil {
		t.Fatalf("receive daemon restart: %v", err)
	}
	if err := services.Close(); err != nil {
		t.Fatalf("close fresh process services: %v", err)
	}
	if restarts != 1 {
		t.Fatalf("fresh-process fallback restarts = %d, want 1", restarts)
	}
}
