package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func boolPointer(value bool) *bool { return &value }

type testJournalMaintainer struct {
	calls     chan string
	promote   func(context.Context) (diagnostics.MaintenanceReport, error)
	retention func(context.Context) (diagnostics.MaintenanceReport, error)
}

func (m *testJournalMaintainer) PromoteSpool(ctx context.Context) (diagnostics.MaintenanceReport, error) {
	m.calls <- "promote"
	if m.promote != nil {
		return m.promote(ctx)
	}
	return diagnostics.MaintenanceReport{PromotedRecords: 1, PromotedFiles: 1}, nil
}

func (m *testJournalMaintainer) RotateAndRetain(ctx context.Context) (diagnostics.MaintenanceReport, error) {
	m.calls <- "retention"
	if m.retention != nil {
		return m.retention(ctx)
	}
	return diagnostics.MaintenanceReport{RotatedFiles: 1, RemovedFiles: 1}, nil
}

func TestJournalMaintenanceScheduleAndPassBudget(t *testing.T) {
	if journalPromotionInterval != 60*time.Second || journalRetentionInterval != 15*time.Minute || journalMaintenanceBudget != 5*time.Second {
		t.Fatalf("production maintenance schedule = promotion %v retention %v budget %v",
			journalPromotionInterval, journalRetentionInterval, journalMaintenanceBudget)
	}
	maintainer := &testJournalMaintainer{calls: make(chan string, 32)}
	records := make(chan string, 32)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runJournalMaintenance(ctx, maintainer, func(_ string, outcome, reason string, _ map[string]string) {
			records <- outcome + ":" + reason
		}, journalMaintenanceSchedule{
			promotionInterval: 10 * time.Millisecond,
			retentionInterval: 15 * time.Millisecond,
			passBudget:        100 * time.Millisecond,
		})
		close(done)
	}()
	counts := map[string]int{}
	deadline := time.After(time.Second)
	for counts["promote"] < 2 || counts["retention"] < 2 {
		select {
		case call := <-maintainer.calls:
			counts[call]++
		case <-deadline:
			cancel()
			t.Fatalf("maintenance calls = %v, want startup and periodic calls", counts)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance loop ignored cancellation")
	}
	close(records)
	seenStartupPromotion := false
	seenStartupRetention := false
	for record := range records {
		seenStartupPromotion = seenStartupPromotion || record == "success:startup_spool_promotion"
		seenStartupRetention = seenStartupRetention || record == "success:startup_rotation_retention"
	}
	if !seenStartupPromotion || !seenStartupRetention {
		t.Fatalf("startup outcomes missing: promotion=%v retention=%v", seenStartupPromotion, seenStartupRetention)
	}
}

func TestJournalMaintenanceRecordsOnlyAfterTimedPassReturns(t *testing.T) {
	var insidePass atomic.Bool
	maintainer := &testJournalMaintainer{
		calls: make(chan string, 2),
		promote: func(ctx context.Context) (diagnostics.MaintenanceReport, error) {
			insidePass.Store(true)
			<-ctx.Done()
			insidePass.Store(false)
			return diagnostics.MaintenanceReport{}, ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	recorded := make(chan bool, 1)
	started := time.Now()
	go func() {
		runJournalMaintenance(ctx, maintainer, func(_ string, outcome, _ string, _ map[string]string) {
			recorded <- outcome == "error" && !insidePass.Load()
			cancel()
		}, journalMaintenanceSchedule{
			promotionInterval: time.Hour,
			retentionInterval: time.Hour,
			passBudget:        20 * time.Millisecond,
		})
		close(done)
	}()
	select {
	case valid := <-recorded:
		if !valid {
			t.Fatal("maintenance outcome was recorded before the timed pass released its work")
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("timed maintenance pass did not report")
	}
	<-done
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("maintenance pass elapsed = %v, want bounded by its timeout", elapsed)
	}
}

func TestJournalMaintenanceDoesNotRecordLifecycleCancellationAsError(t *testing.T) {
	maintainer := &testJournalMaintainer{
		calls: make(chan string, 2),
		promote: func(ctx context.Context) (diagnostics.MaintenanceReport, error) {
			<-ctx.Done()
			return diagnostics.MaintenanceReport{}, fmt.Errorf("promotion interrupted: %w", ctx.Err())
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	recorded := make(chan string, 1)
	go func() {
		runJournalMaintenance(ctx, maintainer, func(_ string, outcome, reason string, _ map[string]string) {
			recorded <- outcome + ":" + reason
		}, journalMaintenanceSchedule{
			promotionInterval: time.Hour,
			retentionInterval: time.Hour,
			passBudget:        time.Hour,
		})
		close(done)
	}()

	select {
	case call := <-maintainer.calls:
		if call != "promote" {
			t.Fatalf("first maintenance call = %q, want promote", call)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup maintenance did not begin")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance pass ignored lifecycle cancellation")
	}
	select {
	case record := <-recorded:
		t.Fatalf("lifecycle cancellation recorded a maintenance outcome: %q", record)
	default:
	}
}

func TestSpawnDetachedPersistsBootstrapFailure(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv(daemonDetachedEnv, "0")
	t.Setenv(daemonLaunchIDEnv, "stale-parent-launch")
	t.Setenv(daemonBootstrapEnv, "/stale/parent.log")
	script := filepath.Join(t.TempDir(), "fake-amx")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"detached=$AMX_DAEMON_DETACHED launch=$AMX_DAEMON_LAUNCH_ID bootstrap=$AMX_DAEMON_BOOTSTRAP_LOG\" >&2\necho 'fatal: config could not be loaded' >&2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SpawnDetached(script); err != nil {
		t.Fatal(err)
	}
	daemonDir := filepath.Join(stateHome, "agent-mux", "daemon")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(daemonDir, "bootstrap-*.log"))
		if len(matches) == 1 {
			data, err := os.ReadFile(matches[0])
			if err == nil && strings.Contains(string(data), "fatal: config could not be loaded") {
				if !strings.Contains(string(data), "detached=1") || strings.Contains(string(data), "stale-parent") || !strings.Contains(string(data), "bootstrap="+matches[0]) {
					t.Fatalf("detached environment was not overridden: %s", data)
				}
				if !strings.Contains(string(data), "daemon bootstrap started") {
					t.Fatalf("bootstrap header missing: %s", data)
				}
				info, err := os.Stat(matches[0])
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("bootstrap mode = %v, err = %v", info.Mode().Perm(), err)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached daemon failure did not reach bootstrap log")
}

func TestSpawnDetachedFallbackWithoutDiagnostics(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateHome, 0o700); err != nil {
		t.Fatal(err)
	}
	// A non-directory at the diagnostics root makes OpenBootstrapLog fail while
	// leaving process creation itself available.
	if err := os.WriteFile(filepath.Join(stateHome, "agent-mux"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", stateHome)

	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("AMX_SPAWN_FALLBACK_MARKER", marker)
	script := filepath.Join(t.TempDir(), "fake-amx")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf started > \"$AMX_SPAWN_FALLBACK_MARKER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := SpawnDetached(script); err != nil {
		t.Fatalf("SpawnDetached() error = %v, want diagnostics failure to be fail-open", err)
	}
	// The full acceptance suite runs package tests in parallel, so the detached
	// child may not be scheduled promptly even after cmd.Start succeeds. Keep
	// this fail-open regression tolerant of host load while still bounding a
	// genuinely missing marker.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "started" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached daemon command did not start after bootstrap-log failure")
}

func TestOperationalLoggerWritesStructuredPrivateRecords(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv(daemonLaunchIDEnv, "launch-test")
	t.Setenv(daemonBootstrapEnv, "/private/bootstrap.log")
	logger, closer, logDir, err := NewOperationalLogger(config.DiagnosticsConfig{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	logger.Error("startup failed", "reason", "socket unavailable")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logDir, "daemon.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("daemon log is not JSON: %v\n%s", err, data)
	}
	if record["msg"] != "startup failed" || record["launch_id"] != "launch-test" || record["reason"] != "socket unavailable" {
		t.Fatalf("structured daemon record = %+v", record)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("daemon log mode = %v, err = %v", info.Mode().Perm(), err)
	}
}

func TestRunRecordsLifecycleAndJournalMaintenance(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "amx-diag-run-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	stateHome := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateHome)
	cfg := &config.Config{Defaults: config.Defaults{
		GuardDuration: 1,
		Diagnostics: config.DiagnosticsConfig{
			JournalEnabled:  boolPointer(true),
			JournalKeepDays: 14,
			JournalMaxMiB:   32,
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	published := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, cfg, runDependencies{afterStartupPublication: func() { close(published) }})
	}()
	select {
	case <-published:
	case err := <-runErr:
		t.Fatalf("daemon exited before publication: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not publish")
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	if err := NewClientWithSocket(SocketPath()).ShutdownAndWait(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not shut down")
	}

	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	// The daemon has stopped, so this assertion is about durable lifecycle
	// coverage rather than the production lock-wait budget. Repository-wide
	// package concurrency can otherwise exhaust the 25ms default spuriously.
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	records, err := journal.Read(context.Background(), diagnostics.Selector{})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, record := range records {
		seen[record.Stage] = true
	}
	for _, stage := range []string{"journal.maintenance", "daemon.startup", "daemon.lock", "daemon.started", "daemon.ready", "daemon.shutdown", "daemon.exit"} {
		if !seen[stage] {
			t.Errorf("lifecycle journal missing stage %q; seen %v", stage, seen)
		}
	}
}

func TestDuplicateDaemonDoesNotRunStartupJournalMaintenance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{}); err != nil {
		t.Fatal(err)
	}
	envelope := diagnostics.NewEnvelope("spool.duplicate-daemon")
	envelope.RequestID = "must-remain-spooled"
	record, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
	if err := os.WriteFile(spoolPath, append(record, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(SocketPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireDaemonLock(SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseDaemonLock(SocketPath(), lock)
	cfg := &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		JournalEnabled: boolPointer(true), JournalKeepDays: 14, JournalMaxMiB: 32,
	}}}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spoolPath); err != nil {
		t.Fatalf("duplicate daemon claimed the active spool: %v", err)
	}
	segments, err := filepath.Glob(filepath.Join(paths.JournalDir, "events-spool-*.jsonl"))
	if err != nil || len(segments) != 0 {
		t.Fatalf("duplicate daemon published spool segments = %v, err = %v", segments, err)
	}
}

func TestActiveDaemonPromotesSpoolAtStartup(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "amx-diag-promotion-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{}); err != nil {
		t.Fatal(err)
	}
	envelope := diagnostics.NewEnvelope("spool.active-daemon")
	envelope.RequestID = "promote-after-lock"
	record, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
	if err := os.WriteFile(spoolPath, append(record, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		JournalEnabled: boolPointer(true), JournalKeepDays: 14, JournalMaxMiB: 32,
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	published := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, cfg, runDependencies{afterStartupPublication: func() { close(published) }})
	}()
	select {
	case <-published:
	case err := <-runErr:
		t.Fatalf("daemon exited before publication: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not publish")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		segments, globErr := filepath.Glob(filepath.Join(paths.JournalDir, "events-spool-*.jsonl"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		_, spoolErr := os.Stat(spoolPath)
		if len(segments) == 1 && os.IsNotExist(spoolErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup promotion incomplete: segments=%v spool_err=%v", segments, spoolErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	if err := NewClientWithSocket(SocketPath()).ShutdownAndWait(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

func TestRunShutdownPersistsQueuedStageRows(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "amx-diag-render-shutdown-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))

	cfg := &config.Config{Defaults: config.Defaults{
		GuardDuration: 1,
		Diagnostics: config.DiagnosticsConfig{
			JournalEnabled:  boolPointer(true),
			JournalKeepDays: 14,
			JournalMaxMiB:   32,
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerPersisted := make(chan struct{})
	releaseWorker := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseWorker) }) }
	t.Cleanup(release)

	const traceID = "trace-run-render-shutdown"
	var publicationErr error
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, cfg, runDependencies{
			afterStartupPublication: func() {
				resp, sendErr := NewClientWithSocket(SocketPath()).Send(Request{
					Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "UserPromptSubmit",
					TraceID: traceID, RequestID: "request-run-render-shutdown",
				})
				if sendErr != nil {
					publicationErr = sendErr
				} else if !resp.OK {
					publicationErr = fmt.Errorf("state update response: %+v", resp)
				}
				cancel()
			},
			afterRenderWorkerShutdown: func() {
				close(workerPersisted)
				<-releaseWorker
			},
		})
	}()

	select {
	case <-workerPersisted:
		select {
		case err := <-runErr:
			t.Fatalf("Run returned before render-worker shutdown completed: %v", err)
		default:
		}
	case err := <-runErr:
		t.Fatalf("Run returned before render-worker persistence: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("render worker did not complete shutdown persistence")
	}

	release()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after render worker completed")
	}
	if publicationErr != nil {
		t.Fatal(publicationErr)
	}

	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	records, err := journal.Read(context.Background(), diagnostics.Selector{
		TraceID: traceID, Stages: []string{"daemon.renderer.queued"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("queued-stage records after Run shutdown = %+v, want exactly one", records)
	}
	if records[0].Outcome != "pending" || records[0].Reason != "latest_generation" {
		t.Fatalf("queued-stage record after Run shutdown = %+v", records[0])
	}
}
