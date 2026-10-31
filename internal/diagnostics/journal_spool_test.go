package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestJournalSpoolsOnLockTimeoutAndReconciles(t *testing.T) {
	t.Parallel()

	// This test exercises fallback and reconciliation rather than the precise
	// lock-wait budget. Leave enough scheduling headroom for the repository-wide
	// package gate while still forcing the held journal lock to time out.
	// Append is guarded only by append.lock, so that is the sidecar held here.
	journal, paths := testJournal(t, JournalOptions{LockTimeout: 100 * time.Millisecond})
	lockFile, err := os.OpenFile(filepath.Join(paths.JournalDir, "append.lock"), os.O_CREATE|os.O_RDWR, fileMode)
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
	t.Parallel()

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
	t.Parallel()

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

func TestAppendSpoolAttemptsLockAfterSlowOpen(t *testing.T) {
	t.Parallel()

	for _, busy := range []bool{false, true} {
		t.Run(fmt.Sprintf("busy=%t", busy), func(t *testing.T) {
			t.Parallel()
			journal, paths := testJournal(t, JournalOptions{LockTimeout: time.Millisecond})
			spoolPath := filepath.Join(paths.SpoolDir, fmt.Sprintf("journal-%d.jsonl", os.Getpid()))
			if busy {
				lockFile, err := os.OpenFile(spoolPath, os.O_CREATE|os.O_RDWR, fileMode)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := errors.Join(unix.Flock(int(lockFile.Fd()), unix.LOCK_UN), lockFile.Close()); err != nil {
						t.Errorf("release spool fixture: %v", err)
					}
				})
				if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
					t.Fatal(err)
				}
			}

			// Force the open to consume the wait budget. An available lock still
			// permits one immediate attempt; a busy lock must fail without waiting.
			err := journal.appendSpoolWithOpenHook(context.Background(), []byte("{}\n"), func() {
				time.Sleep(2 * journal.opts.LockTimeout)
			})
			if busy && !errors.Is(err, ErrLockTimeout) {
				t.Fatalf("busy spool error = %v, want %v", err, ErrLockTimeout)
			}
			if !busy && err != nil {
				t.Fatalf("available spool error = %v", err)
			}
			data, readErr := os.ReadFile(spoolPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := "{}\n"
			if busy {
				want = ""
			}
			if string(data) != want {
				t.Fatalf("spool = %q, want %q", data, want)
			}
		})
	}
}

func TestAppendSpoolStopsRetryingPromotedFileAfterTimeout(t *testing.T) {
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{LockTimeout: time.Millisecond})
	spoolPath := filepath.Join(paths.SpoolDir, fmt.Sprintf("journal-%d.jsonl", os.Getpid()))
	promotedPath := filepath.Join(paths.JournalDir, "events-spool-slow-open.jsonl")
	err := journal.appendSpoolWithOpenHook(context.Background(), []byte("{}\n"), func() {
		if err := os.Rename(spoolPath, promotedPath); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * journal.opts.LockTimeout)
	})
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("promoted spool error = %v, want %v", err, ErrLockTimeout)
	}
	if data, err := os.ReadFile(promotedPath); err != nil || len(data) != 0 {
		t.Fatalf("promoted spool changed: %q, err = %v", data, err)
	}
	if _, err := os.Stat(spoolPath); !os.IsNotExist(err) {
		t.Fatalf("writer retried after timeout: %v", err)
	}
}

func TestPromoteSpoolSkipsBusyActiveWriter(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
