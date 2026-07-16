package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func testJournal(t *testing.T, options JournalOptions) (*Journal, Paths) {
	t.Helper()
	paths := PathsForRoot(filepath.Join(t.TempDir(), "state", "agent-mux"))
	journal, err := NewJournal(paths, options)
	if err != nil {
		t.Fatal(err)
	}
	return journal, paths
}

func testEnvelope(stage, request string) TraceEnvelope {
	envelope := NewEnvelope(stage)
	envelope.RequestID = request
	envelope.Outcome = "success"
	return envelope
}

func TestDefaultPathsResolveXDGAndHomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "agent-mux")
	if paths.Root != want || paths.JournalFile != filepath.Join(want, "journal", "events.jsonl") || paths.DaemonLogFile != filepath.Join(want, "daemon", "daemon.jsonl") {
		t.Fatalf("home fallback paths = %+v, want root %s", paths, want)
	}
	xdg := filepath.Join(t.TempDir(), "xdg-state")
	t.Setenv("XDG_STATE_HOME", xdg)
	paths, err = DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != filepath.Join(xdg, "agent-mux") {
		t.Fatalf("XDG root = %s", paths.Root)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	if _, err := DefaultPaths(); err == nil {
		t.Fatal("relative XDG_STATE_HOME was accepted")
	}
	if err := (Paths{}).Ensure(); err == nil {
		t.Fatal("empty paths were accepted")
	}
	unsafe := PathsForRoot(filepath.Join(t.TempDir(), "root"))
	unsafe.JournalDir = t.TempDir()
	if err := unsafe.Ensure(); err == nil {
		t.Fatal("journal directory outside state root was accepted")
	}
}

func TestJournalProcessHelper(t *testing.T) {
	if os.Getenv("AMX_JOURNAL_HELPER") != "1" {
		return
	}
	root := os.Getenv("AMX_JOURNAL_ROOT")
	count, _ := strconv.Atoi(os.Getenv("AMX_JOURNAL_COUNT"))
	journal, err := NewJournal(PathsForRoot(root), JournalOptions{LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		envelope := testEnvelope("concurrency.process", fmt.Sprintf("%d-%d", os.Getpid(), i))
		if err := journal.Append(context.Background(), envelope); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJournalConcurrentProcessesDoNotCorruptJSONL(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{LockTimeout: 2 * time.Second})
	const processes = 4
	const recordsPerProcess = 12
	commands := make([]*exec.Cmd, processes)
	outputs := make([]bytes.Buffer, processes)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestJournalProcessHelper$")
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		commands[i].Env = append(os.Environ(),
			"AMX_JOURNAL_HELPER=1",
			"AMX_JOURNAL_ROOT="+paths.Root,
			"AMX_JOURNAL_COUNT="+strconv.Itoa(recordsPerProcess),
		)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("helper failed: %v\n%s", err, outputs[i].Bytes())
		}
	}
	records, err := journal.Read(context.Background(), Selector{Stages: []string{"concurrency.process"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != processes*recordsPerProcess {
		t.Fatalf("record count = %d, want %d", len(records), processes*recordsPerProcess)
	}
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if seen[record.RequestID] {
			t.Fatalf("duplicate request ID %q", record.RequestID)
		}
		seen[record.RequestID] = true
	}
}

func TestJournalConcurrentGoroutinesDoNotCorruptJSONL(t *testing.T) {
	journal, _ := testJournal(t, JournalOptions{})
	const writers = 8
	const each = 10
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := journal.Append(context.Background(), testEnvelope("concurrency.goroutine", fmt.Sprintf("%d-%d", writer, i))); err != nil {
					errs <- err
					return
				}
			}
		}(writer)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	records, err := journal.Read(context.Background(), Selector{Stages: []string{"concurrency.goroutine"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != writers*each {
		t.Fatalf("record count = %d, want %d", len(records), writers*each)
	}
}

func TestJournalRestartAndCrashTailRecovery(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	if err := journal.Append(context.Background(), testEnvelope("restart", "before")); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(paths.JournalDir, "events.jsonl")
	file, err := os.OpenFile(current, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"schema_version":1,"trace_id":"crash`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	restarted, err := NewJournal(paths, JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Append(context.Background(), testEnvelope("restart", "after")); err != nil {
		t.Fatal(err)
	}
	records, err := restarted.Read(context.Background(), Selector{Stages: []string{"restart"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].RequestID != "before" || records[1].RequestID != "after" {
		t.Fatalf("recovered records = %+v", records)
	}
	quarantines, err := filepath.Glob(current + ".partial-*")
	if err != nil || len(quarantines) != 1 {
		t.Fatalf("partial quarantines = %v, err = %v", quarantines, err)
	}
	partial, err := os.ReadFile(quarantines[0])
	if err != nil || !bytes.Contains(partial, []byte(`"trace_id":"crash`)) {
		t.Fatalf("quarantined tail = %q, err = %v", partial, err)
	}
}

func TestJournalSpoolsOnLockTimeoutAndReconciles(t *testing.T) {
	// This test exercises fallback and reconciliation rather than the precise
	// lock-wait budget. Leave enough scheduling headroom for the repository-wide
	// package gate while still forcing the held journal lock to time out.
	journal, paths := testJournal(t, JournalOptions{LockTimeout: 100 * time.Millisecond})
	lockFile, err := os.OpenFile(filepath.Join(paths.JournalDir, "events.lock"), os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(context.Background(), testEnvelope("spool", "fallback")); err != nil {
		t.Fatalf("Append should succeed through spool: %v", err)
	}
	spools, _ := filepath.Glob(filepath.Join(paths.SpoolDir, "journal-*.jsonl"))
	if len(spools) != 1 {
		t.Fatalf("spool files = %v, want one", spools)
	}
	_ = unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
	_ = lockFile.Close()

	report, err := journal.Maintain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ReconciledRecords != 1 || report.ReconciledFiles != 1 {
		t.Fatalf("maintenance report = %+v", report)
	}
	records, err := journal.Read(context.Background(), Selector{Stages: []string{"spool"}})
	if err != nil || len(records) != 1 || records[0].RequestID != "fallback" {
		t.Fatalf("reconciled records = %+v, err = %v", records, err)
	}
	spools, _ = filepath.Glob(filepath.Join(paths.SpoolDir, "journal-*.jsonl"))
	if len(spools) != 0 {
		t.Fatalf("spool files remain after reconciliation: %v", spools)
	}
}

func TestReconcileSpoolSkipsIdentityScanWhenSpoolEmpty(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	segment := filepath.Join(paths.JournalDir, "events-oversized.jsonl")
	file, err := os.OpenFile(segment, os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxSegmentBytes + 2); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := journal.ReconcileSpool(context.Background())
	if err != nil {
		t.Fatalf("empty-spool reconciliation read journal segment contents: %v", err)
	}
	if report.ReconciledRecords != 0 || report.ReconciledFiles != 0 || report.CorruptLines != 0 || len(report.QuarantinedFiles) != 0 || report.RotatedFiles != 0 || report.RemovedFiles != 0 || report.RemovedBytes != 0 {
		t.Fatalf("empty-spool reconciliation = %+v, want empty report", report)
	}
}

func TestAppendSpoolLockIsBounded(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{LockTimeout: 20 * time.Millisecond})
	spoolPath := filepath.Join(paths.SpoolDir, fmt.Sprintf("journal-%d.jsonl", os.Getpid()))
	lockFile, err := os.OpenFile(spoolPath, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
		_ = lockFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_UN); err != nil {
			t.Errorf("unlock spool fixture: %v", err)
		}
		if err := lockFile.Close(); err != nil {
			t.Errorf("close spool fixture: %v", err)
		}
	})

	started := time.Now()
	err = journal.appendSpool(context.Background(), []byte("{}\n"))
	elapsed := time.Since(started)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("appendSpool error = %v, want %v", err, ErrLockTimeout)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("appendSpool lock wait = %v, want a bounded wait", elapsed)
	}
}

func TestPromoteSpoolSkipsBusyActiveWriter(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
	record, err := json.Marshal(testEnvelope("spool.busy", "busy"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spoolPath, append(record, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	active, err := os.OpenFile(spoolPath, os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(active.Fd()), unix.LOCK_EX); err != nil {
		_ = active.Close()
		t.Fatal(err)
	}

	report, err := journal.PromoteSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.BusySpoolFiles != 1 || report.PromotedFiles != 0 {
		t.Fatalf("busy promotion = %+v", report)
	}
	if _, err := os.Stat(spoolPath); err != nil {
		t.Fatalf("busy spool was claimed: %v", err)
	}
	if err := errors.Join(unix.Flock(int(active.Fd()), unix.LOCK_UN), active.Close()); err != nil {
		t.Fatal(err)
	}

	report, err = journal.PromoteSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.PromotedFiles != 1 || report.PromotedRecords != 1 {
		t.Fatalf("retry promotion = %+v", report)
	}
}

func TestSpoolPromotionIsAtomicForConcurrentSnapshots(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
	record, _ := json.Marshal(testEnvelope("spool.snapshot", "atomic"))
	if err := os.WriteFile(spoolPath, append(record, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatal(err)
	}
	claimed := make(chan string, 1)
	release := make(chan struct{})
	promoted := make(chan error, 1)
	go func() {
		_, err := journal.promoteSpool(context.Background(), spoolPromotionHooks{afterClaim: func(path string) error {
			claimed <- path
			<-release
			return nil
		}})
		promoted <- err
	}()
	claimPath := <-claimed
	if _, err := os.Stat(claimPath); err != nil {
		t.Fatalf("claimed spool missing: %v", err)
	}
	during, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"spool.snapshot"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(during.Records) != 1 || during.Statistics.SpoolFilesRead != 1 {
		t.Fatalf("snapshot during promotion = %+v", during)
	}
	close(release)
	if err := <-promoted; err != nil {
		t.Fatal(err)
	}
	segments, err := filepath.Glob(filepath.Join(paths.JournalDir, "events-spool-*.jsonl"))
	if err != nil || len(segments) != 1 {
		t.Fatalf("promoted segments = %v, err = %v", segments, err)
	}
	after, err := os.Stat(segments[0])
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("promotion copied the spool instead of atomically renaming its inode")
	}
	if after.Mode().Perm() != fileMode {
		t.Fatalf("promoted segment mode = %o, want %o", after.Mode().Perm(), fileMode)
	}
	delta, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"spool.snapshot"}}, &during.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 0 {
		t.Fatalf("renamed inode replayed through snapshot cursor: %+v", delta.Records)
	}
}

func TestSpoolPromotionQuarantinesCorruptAndPartialLines(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	record, _ := json.Marshal(testEnvelope("spool.corrupt", "valid"))
	spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
	data := append(append(append(record, '\n'), []byte("{broken}\n")...), []byte(`{"partial":true}`)...)
	if err := os.WriteFile(spoolPath, data, fileMode); err != nil {
		t.Fatal(err)
	}
	report, err := journal.PromoteSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.PromotedRecords != 1 || report.PromotedFiles != 1 || report.CorruptLines != 2 || len(report.QuarantinedFiles) != 1 {
		t.Fatalf("corrupt promotion = %+v", report)
	}
	quarantine, err := os.ReadFile(report.QuarantinedFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(quarantine, []byte("{broken}")) || !bytes.Contains(quarantine, []byte(`{"partial":true}`)) {
		t.Fatalf("quarantine = %q", quarantine)
	}
	snapshot, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"spool.corrupt"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 1 || snapshot.Records[0].RequestID != "valid" {
		t.Fatalf("promoted valid evidence = %+v", snapshot.Records)
	}
}

func TestSpoolPromotionKeepsRewrittenClaimLockedThroughPublish(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	competitor, err := NewJournal(paths, JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(testEnvelope("spool.locked-rewrite", "preserved"))
	if err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
	data := append(append(record, '\n'), []byte("{broken}\n")...)
	if err := os.WriteFile(spoolPath, data, fileMode); err != nil {
		t.Fatal(err)
	}

	validated := make(chan string, 1)
	release := make(chan struct{})
	type promotionResult struct {
		report MaintenanceReport
		err    error
	}
	firstDone := make(chan promotionResult, 1)
	go func() {
		report, err := journal.promoteSpool(context.Background(), spoolPromotionHooks{
			beforePublish: func(path string) error {
				validated <- path
				<-release
				return nil
			},
		})
		firstDone <- promotionResult{report: report, err: err}
	}()
	claimPath := <-validated

	competingReport, competingErr := competitor.PromoteSpool(context.Background())
	close(release)
	first := <-firstDone

	if competingErr != nil {
		t.Fatalf("competing promotion failed instead of skipping locked claim: %v", competingErr)
	}
	if competingReport.BusySpoolFiles != 1 || competingReport.PromotedFiles != 0 {
		t.Fatalf("competing promotion = %+v, want one busy claim", competingReport)
	}
	if first.err != nil {
		t.Fatalf("claim owner promotion: %v", first.err)
	}
	if first.report.PromotedFiles != 1 || first.report.PromotedRecords != 1 {
		t.Fatalf("claim owner promotion = %+v", first.report)
	}
	if _, err := os.Stat(claimPath); !os.IsNotExist(err) {
		t.Fatalf("published claim still exists: %v", err)
	}
	segments, err := filepath.Glob(filepath.Join(paths.JournalDir, "events-spool-*.jsonl"))
	if err != nil || len(segments) != 1 {
		t.Fatalf("published segments = %v, err = %v", segments, err)
	}
}

func TestSpoolAppendSurvivesConcurrentPromotion(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{LockTimeout: 2 * time.Second})
	firstJSON, err := json.Marshal(testEnvelope("spool.concurrent", "before-claim"))
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(testEnvelope("spool.concurrent", "blocked-writer"))
	if err != nil {
		t.Fatal(err)
	}
	firstJSON = append(firstJSON, '\n')
	secondJSON = append(secondJSON, '\n')

	spoolPath := filepath.Join(paths.SpoolDir, fmt.Sprintf("journal-%d.jsonl", os.Getpid()))
	if err := os.WriteFile(spoolPath, firstJSON, fileMode); err != nil {
		t.Fatal(err)
	}
	claimedFile, err := os.OpenFile(spoolPath, os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(claimedFile.Fd()), unix.LOCK_EX); err != nil {
		_ = claimedFile.Close()
		t.Fatal(err)
	}

	opened := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- journal.appendSpoolWithOpenHook(context.Background(), secondJSON, func() {
			close(opened)
			<-releaseWriter
		})
	}()
	<-opened

	if err := errors.Join(unix.Flock(int(claimedFile.Fd()), unix.LOCK_UN), claimedFile.Close()); err != nil {
		t.Fatal(err)
	}
	firstReport, err := journal.PromoteSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if firstReport.PromotedRecords != 1 || firstReport.PromotedFiles != 1 {
		t.Fatalf("first promotion = %+v", firstReport)
	}
	close(releaseWriter)

	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked spool writer did not retry the stable path")
	}

	report, err := journal.PromoteSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.PromotedRecords != 1 || report.PromotedFiles != 1 {
		t.Fatalf("second promotion = %+v", report)
	}
	records, err := journal.Read(context.Background(), Selector{Stages: []string{"spool.concurrent"}})
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, record := range records {
		counts[record.RequestID]++
	}
	if len(records) != 2 || counts["before-claim"] != 1 || counts["blocked-writer"] != 1 {
		t.Fatalf("records after concurrent reconciliation = %+v", records)
	}
}

func TestSpoolPromotionRestartBoundariesAreRetrySafe(t *testing.T) {
	injectedFailure := errors.New("injected crash")
	tests := []struct {
		name  string
		hooks spoolPromotionHooks
	}{
		{name: "after-claim", hooks: spoolPromotionHooks{afterClaim: func(string) error { return injectedFailure }}},
		{name: "after-validation", hooks: spoolPromotionHooks{afterValidation: func(string) error { return injectedFailure }}},
		{name: "after-publication", hooks: spoolPromotionHooks{afterPublish: func(string) error { return injectedFailure }}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal, paths := testJournal(t, JournalOptions{})
			record, err := json.Marshal(testEnvelope("spool.restart", test.name))
			if err != nil {
				t.Fatal(err)
			}
			spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
			if err := os.WriteFile(spoolPath, append(record, '\n'), fileMode); err != nil {
				t.Fatal(err)
			}
			if _, err := journal.promoteSpool(context.Background(), test.hooks); !errors.Is(err, injectedFailure) {
				t.Fatalf("injected promotion error = %v", err)
			}

			restarted, err := NewJournal(paths, JournalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.PromoteSpool(context.Background()); err != nil {
				t.Fatal(err)
			}
			snapshot, err := restarted.Snapshot(context.Background(), Selector{Stages: []string{"spool.restart"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Records) != 1 || snapshot.Records[0].RequestID != test.name {
				t.Fatalf("restart evidence = %+v", snapshot.Records)
			}
			claims, err := filepath.Glob(filepath.Join(paths.SpoolDir, "journal-*.claim"))
			if err != nil || len(claims) != 0 {
				t.Fatalf("claims after restart = %v, err = %v", claims, err)
			}
		})
	}
}

func TestLegacySpoolClaimDeduplicatesPartialCopies(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	first := testEnvelope("spool.legacy", "already-copied")
	second := testEnvelope("spool.legacy", "not-yet-copied")
	if err := journal.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	claim := filepath.Join(paths.SpoolDir, "journal-4242-reconciling-1.claim")
	data := append(append(append(firstJSON, '\n'), secondJSON...), '\n')
	if err := os.WriteFile(claim, data, fileMode); err != nil {
		t.Fatal(err)
	}
	report, err := journal.PromoteSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.PromotedRecords != 1 || report.PromotedFiles != 1 {
		t.Fatalf("legacy promotion = %+v", report)
	}
	snapshot, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"spool.legacy"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 2 || snapshot.Statistics.DuplicatesOmitted != 0 {
		t.Fatalf("legacy records = %+v statistics=%+v", snapshot.Records, snapshot.Statistics)
	}
}

func TestPromotionCancellationLeavesClaimForRestart(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	record, _ := json.Marshal(testEnvelope("spool.cancel", "preserved"))
	if err := os.WriteFile(filepath.Join(paths.SpoolDir, "journal-4242.jsonl"), append(record, '\n'), fileMode); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := journal.promoteSpool(ctx, spoolPromotionHooks{afterClaim: func(string) error {
		cancel()
		return nil
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled promotion error = %v", err)
	}
	claims, _ := filepath.Glob(filepath.Join(paths.SpoolDir, "journal-*.claim"))
	if len(claims) != 1 {
		t.Fatalf("cancelled promotion claims = %v", claims)
	}
	restarted, err := NewJournal(paths, JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.PromoteSpool(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestJournalRotationAgeAndQuotaRetention(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	journal, paths := testJournal(t, JournalOptions{
		KeepDays: 1, MaxBytes: 1 << 30, SegmentBytes: 700,
		Now: func() time.Time { return now },
	})
	for i := 0; i < 4; i++ {
		envelope := testEnvelope("rotation", strconv.Itoa(i))
		envelope.Evidence = map[string]string{"detail": strings.Repeat("x", 500)}
		if err := journal.Append(context.Background(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	rotated, _ := filepath.Glob(filepath.Join(paths.JournalDir, "events-*.jsonl"))
	if len(rotated) < 2 {
		t.Fatalf("rotated files = %v, want at least two", rotated)
	}
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(rotated[0], old, old); err != nil {
		t.Fatal(err)
	}
	report, err := journal.Maintain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedFiles < 1 {
		t.Fatalf("age retention report = %+v", report)
	}

	quotaJournal, quotaPaths := testJournal(t, JournalOptions{KeepDays: 30, MaxBytes: 900, SegmentBytes: 700})
	for i := 0; i < 5; i++ {
		envelope := testEnvelope("quota", strconv.Itoa(i))
		envelope.Evidence = map[string]string{"detail": strings.Repeat("q", 500)}
		if err := quotaJournal.Append(context.Background(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	quotaReport, err := quotaJournal.Maintain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quotaReport.RemovedFiles == 0 {
		t.Fatalf("quota retention removed no segments: %+v", quotaReport)
	}
	var total int64
	entries, _ := os.ReadDir(quotaPaths.JournalDir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			info, _ := entry.Info()
			total += info.Size()
		}
	}
	if total > 900 {
		t.Fatalf("retained journal size = %d, want <= 900", total)
	}
}

func TestJournalPrivatePermissionsHMACAndFailOpen(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	payload := []byte("raw secret payload")
	envelope := testEnvelope("privacy", "one")
	journal.ProtectPayload(&envelope, payload)
	if envelope.PayloadHMAC == "" || envelope.PayloadSize != int64(len(payload)) || strings.Contains(envelope.PayloadHMAC, string(payload)) {
		t.Fatalf("payload metadata = HMAC:%q size:%d", envelope.PayloadHMAC, envelope.PayloadSize)
	}
	if err := journal.Append(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Root, paths.JournalDir, paths.DaemonDir, paths.NativeDir, paths.CaptureDir, paths.SpoolDir} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != directoryMode {
			t.Fatalf("directory %s mode = %v, err = %v", path, info.Mode().Perm(), err)
		}
	}
	for _, path := range []string{paths.HMACKey, filepath.Join(paths.JournalDir, "events.jsonl"), filepath.Join(paths.JournalDir, "events.lock")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != fileMode {
			t.Fatalf("file %s mode = %v, err = %v", path, info.Mode().Perm(), err)
		}
	}
	data, err := os.ReadFile(filepath.Join(paths.JournalDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, payload) {
		t.Fatal("raw payload was persisted in journal")
	}

	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(paths.JournalDir, "events.jsonl")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, current); err != nil {
		t.Fatal(err)
	}
	if journal.TryAppend(context.Background(), testEnvelope("failure", "ignored")) {
		t.Fatal("TryAppend reported success for symlink target")
	}
	unchanged, _ := os.ReadFile(target)
	if string(unchanged) != "unchanged" {
		t.Fatalf("fail-open path modified symlink target: %q", unchanged)
	}
}

func TestJournalSelectorsSanitizedEvidenceAndLimit(t *testing.T) {
	journal, _ := testJournal(t, JournalOptions{})
	base := time.Now().UTC().Add(-time.Hour)
	for i, stage := range []string{"ingress", "decision", "decision"} {
		envelope := testEnvelope(stage, strconv.Itoa(i))
		envelope.TraceID = "trace"
		envelope.WindowID = 42
		envelope.ObservedAt = base.Add(time.Duration(i) * time.Minute)
		envelope.Evidence = map[string]string{"token_value": "secret", "safe": "ok\nvalue", "source_event_ids": "other,source-" + strconv.Itoa(i)}
		if err := journal.Append(context.Background(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	records, err := journal.Read(context.Background(), Selector{
		TraceID: "trace", WindowID: 42, Stages: []string{"decision"}, Limit: 1,
	})
	if err != nil || len(records) != 1 || records[0].RequestID != "2" {
		t.Fatalf("selected records = %+v, err = %v", records, err)
	}
	if records[0].Evidence["token_value"] != "[redacted]" || strings.Contains(records[0].Evidence["safe"], "\n") {
		t.Fatalf("sanitized evidence = %+v", records[0].Evidence)
	}
	bySourceEvent, err := journal.Read(context.Background(), Selector{SourceEventID: "source-2"})
	if err != nil || len(bySourceEvent) != 1 || bySourceEvent[0].RequestID != "2" {
		t.Fatalf("source-event records = %+v, err = %v", bySourceEvent, err)
	}
}

func TestJournalTolerantReadAndSpoolReconciliationPreserveLaterValidLines(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	first := testEnvelope("tail", "main-first")
	first.Evidence = map[string]string{"event": "PreToolUse"}
	if err := journal.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	current, err := os.OpenFile(paths.JournalFile, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	later := testEnvelope("tail", "main-later")
	later.Evidence = map[string]string{"event": "PreToolUse"}
	laterJSON, err := json.Marshal(later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(current, "{corrupt-main}\n%s\n", laterJSON); err != nil {
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}

	spoolPath := filepath.Join(paths.SpoolDir, "journal-99999.jsonl")
	spooledFirst := testEnvelope("tail", "spool-first")
	spooledFirst.Evidence = map[string]string{"event": "PreToolUse"}
	spooledLater := testEnvelope("tail", "spool-later")
	spooledLater.Evidence = map[string]string{"event": "PreToolUse"}
	spooledFirstJSON, _ := json.Marshal(spooledFirst)
	spooledLaterJSON, _ := json.Marshal(spooledLater)
	spoolData := append(append(append(spooledFirstJSON, '\n'), []byte("{corrupt-spool}\n")...), spooledLaterJSON...)
	spoolData = append(spoolData, '\n')
	if err := os.WriteFile(spoolPath, spoolData, fileMode); err != nil {
		t.Fatal(err)
	}

	maintenance, err := journal.ReconcileSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.ReconciledRecords != 2 || maintenance.CorruptLines != 1 || len(maintenance.QuarantinedFiles) != 1 {
		t.Fatalf("reconciliation = %+v", maintenance)
	}
	report, err := journal.ReadTolerant(context.Background(), Selector{Event: "PreToolUse"})
	if err != nil {
		t.Fatal(err)
	}
	if report.CorruptLines != 1 || len(report.Records) != 4 {
		t.Fatalf("tolerant report = %+v", report)
	}
	requests := make([]string, 0, len(report.Records))
	for _, record := range report.Records {
		requests = append(requests, record.RequestID)
	}
	for _, want := range []string{"main-first", "main-later", "spool-first", "spool-later"} {
		if !slices.Contains(requests, want) {
			t.Fatalf("requests = %v, missing %q", requests, want)
		}
	}
	health, err := journal.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.CorruptLines != 1 || health.QuarantinedFiles != 1 || health.SpoolFiles != 0 {
		t.Fatalf("health = %+v", health)
	}
}

func TestDaemonLogWriterRotatesStructuredRecordsAndBootstrapIsPrivate(t *testing.T) {
	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	writer, err := NewDaemonLogWriter(paths, DaemonLogOptions{SegmentBytes: 500, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(writer, nil))
	for i := 0; i < 6; i++ {
		logger.Info("daemon test record", "sequence", i, "detail", strings.Repeat("x", 220))
	}
	files, _ := filepath.Glob(filepath.Join(paths.DaemonDir, "*.jsonl"))
	if len(files) < 2 {
		t.Fatalf("daemon log files = %v, want rotation", files)
	}
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				t.Fatalf("invalid structured log %s: %v", path, err)
			}
		}
		_ = file.Close()
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
	}
	bootstrap, bootstrapPath, err := OpenBootstrapLog(paths, "launch:test/unsafe")
	if err != nil {
		t.Fatal(err)
	}
	_ = bootstrap.Close()
	if !strings.Contains(filepath.Base(bootstrapPath), "launchtestunsafe") {
		t.Fatalf("bootstrap filename was not sanitized: %s", bootstrapPath)
	}
	info, err := os.Stat(bootstrapPath)
	if err != nil || info.Mode().Perm() != fileMode {
		t.Fatalf("bootstrap mode = %v, err = %v", info.Mode().Perm(), err)
	}
}
