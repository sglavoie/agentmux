package datastore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRebuildGateBlocksStartupUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	gate, err := BeginRebuildGate(path)
	if err != nil {
		t.Fatalf("begin rebuild gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Release() })

	active, err := RebuildGateActive(path)
	if err != nil || !active {
		t.Fatalf("active gate = %v, %v; want true", active, err)
	}
	if _, err := BeginRebuildGate(path); !errors.Is(err, ErrRebuildInProgress) {
		t.Fatalf("second gate error = %v, want ErrRebuildInProgress", err)
	}
	if _, err := BeginDaemonStartup(t.Context(), path); !errors.Is(err, ErrRebuildInProgress) {
		t.Fatalf("daemon startup error = %v, want ErrRebuildInProgress", err)
	}
	if err := gate.Release(); err != nil {
		t.Fatalf("release rebuild gate: %v", err)
	}
	active, err = RebuildGateActive(path)
	if err != nil || active {
		t.Fatalf("released gate = %v, %v; want false", active, err)
	}
	startup, err := BeginDaemonStartup(t.Context(), path)
	if err != nil {
		t.Fatalf("daemon startup after gate release: %v", err)
	}
	if err := startup.Release(); err != nil {
		t.Fatalf("release daemon startup lease: %v", err)
	}
}

func TestRebuildGateWaitsForInflightDaemonStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	startup, err := BeginDaemonStartup(t.Context(), path)
	if err != nil {
		t.Fatalf("begin daemon startup: %v", err)
	}
	t.Cleanup(func() { _ = startup.Release() })

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	gateResult := make(chan *RebuildGate, 1)
	errResult := make(chan error, 1)
	go func() {
		gate, err := BeginRebuildGateContext(ctx, path)
		if err != nil {
			errResult <- err
			return
		}
		gateResult <- gate
	}()

	select {
	case gate := <-gateResult:
		_ = gate.Release()
		t.Fatal("rebuild gate published while daemon startup still owned serialization")
	case err := <-errResult:
		t.Fatalf("rebuild gate failed before daemon startup released: %v", err)
	case <-time.After(75 * time.Millisecond):
	}

	if err := startup.Release(); err != nil {
		t.Fatalf("release daemon startup: %v", err)
	}
	select {
	case gate := <-gateResult:
		t.Cleanup(func() { _ = gate.Release() })
		active, err := RebuildGateActive(path)
		if err != nil || !active {
			t.Fatalf("gate after startup release = %v, %v; want active", active, err)
		}
	case err := <-errResult:
		t.Fatalf("publish rebuild gate after daemon startup release: %v", err)
	case <-ctx.Done():
		t.Fatalf("rebuild gate remained blocked after daemon startup released: %v", ctx.Err())
	}
}

func TestFinishRebuildGateConsumesCrossExecMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	gate, err := BeginRebuildGate(path)
	if err != nil {
		t.Fatalf("begin rebuild gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Release() })
	if err := gate.SetRestartDaemon(true); err != nil {
		t.Fatalf("record restart intent: %v", err)
	}

	restart, err := FinishRebuildGate(path)
	if err != nil || !restart {
		t.Fatalf("finish gate = restart %v, error %v; want restart", restart, err)
	}
	if _, err := os.Lstat(rebuildGatePath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finished marker still exists: %v", err)
	}
	if err := gate.Release(); err != nil {
		t.Fatalf("release inherited descriptor: %v", err)
	}
}

func TestRebuildGateActiveRemovesUnlockedCrashResidue(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	marker := rebuildGatePath(path)
	if err := os.WriteFile(marker, []byte(`{"pid":999999,"restart_daemon":true}`), databaseFileMode); err != nil {
		t.Fatal(err)
	}
	active, err := RebuildGateActive(path)
	if err != nil || active {
		t.Fatalf("stale gate = %v, %v; want false", active, err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale marker still exists: %v", err)
	}
}

func TestRebuildGateDurableMarkerSurvivesUnlockedOwnerExitAndCanBeClaimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	gate, err := BeginRebuildGate(path)
	if err != nil {
		t.Fatalf("begin rebuild gate: %v", err)
	}
	if err := gate.SetRestartDaemon(true); err != nil {
		t.Fatalf("record restart intent: %v", err)
	}
	wantResult := RecoveryResult{Replaced: true, DatabasePath: path, QuarantineDir: filepath.Join(filepath.Dir(path), "recovery", "now")}
	if err := gate.MarkRebuildPending(wantResult); err != nil {
		t.Fatalf("mark rebuild pending: %v", err)
	}

	// Model process exit: the descriptor disappears without the owner removing
	// the marker. Durable post-replacement state must remain fail-closed.
	gate.mu.Lock()
	gate.released = true
	owner := gate.file
	gate.mu.Unlock()
	if err := owner.Close(); err != nil {
		t.Fatalf("close simulated exited owner: %v", err)
	}
	active, err := RebuildGateActive(path)
	if err != nil || !active {
		t.Fatalf("durable unlocked gate = %v, %v; want active", active, err)
	}
	if _, err := os.Lstat(rebuildGatePath(path)); err != nil {
		t.Fatalf("durable marker was removed after owner exit: %v", err)
	}

	claimed, gotResult, err := ClaimPendingRebuildGate(t.Context(), path)
	if err != nil || claimed == nil {
		t.Fatalf("claim pending rebuild = gate %v, error %v", claimed != nil, err)
	}
	if gotResult != wantResult {
		t.Fatalf("claimed recovery result = %+v, want %+v", gotResult, wantResult)
	}
	restart, err := claimed.Finish()
	if err != nil || !restart {
		t.Fatalf("finish claimed rebuild = restart %v, error %v; want restart", restart, err)
	}
	startup, err := BeginDaemonStartup(t.Context(), path)
	if err != nil {
		t.Fatalf("daemon startup after claimed rebuild finish: %v", err)
	}
	if err := startup.Release(); err != nil {
		t.Fatalf("release daemon startup: %v", err)
	}
}

func TestRebuildGateClaimedFinishRemainsRetryableBeforeMarkerRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	gate, err := BeginRebuildGate(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.MarkRebuildPending(RecoveryResult{Replaced: true, DatabasePath: path}); err != nil {
		t.Fatal(err)
	}
	gate.mu.Lock()
	gate.released = true
	owner := gate.file
	gate.mu.Unlock()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := ClaimPendingRebuildGate(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	marker := rebuildGatePath(path)
	displaced := marker + ".displaced"
	if err := os.Rename(marker, displaced); err != nil {
		t.Fatal(err)
	}
	if _, err := claimed.Finish(); err == nil {
		t.Fatal("finish succeeded after claimed marker pathname was displaced")
	}
	if err := os.Rename(displaced, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := claimed.Finish(); err != nil {
		t.Fatalf("retry finish after restoring marker: %v", err)
	}
}

func TestCheckpointPublishesWALIntoMainDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	store, err := OpenPath(t.Context(), path)
	if err != nil {
		t.Fatalf("open datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().ExecContext(t.Context(), `PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `
		CREATE TABLE checkpoint_payloads (payload BLOB NOT NULL);
		INSERT INTO checkpoint_payloads (payload) VALUES (zeroblob(8388608));
	`); err != nil {
		t.Fatal(err)
	}
	walInfo, err := os.Stat(path + "-wal")
	if err != nil || walInfo.Size() == 0 {
		t.Fatalf("pre-checkpoint WAL = %v, %v; want non-empty", walInfo, err)
	}
	if err := store.Checkpoint(t.Context()); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	walInfo, err = os.Stat(path + "-wal")
	if err != nil || walInfo.Size() != 0 {
		t.Fatalf("post-checkpoint WAL size = %v, %v; want zero", walInfo, err)
	}
	check, err := pragmaCheck(t.Context(), store.DB(), `PRAGMA quick_check`)
	if err != nil || check != "ok" {
		t.Fatalf("post-checkpoint quick_check = %q, %v", check, err)
	}
}
