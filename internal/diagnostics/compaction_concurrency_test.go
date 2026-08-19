package diagnostics

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestJournalCompactInterruptionPointsNeverLoseUniqueRecords(t *testing.T) {
	injected := errors.New("injected crash")
	points := []string{"after-preparation", "after-output-rename", "after-publication", "after-input-cleanup"}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			journal, paths := compactionJournalWithRecords(t, "compaction.crash", 3)
			hooks := compactionHooks{}
			switch point {
			case "after-preparation":
				hooks.afterPreparation = func([]string) error { return injected }
			case "after-output-rename":
				hooks.afterOutputRename = func(string) error { return injected }
			case "after-publication":
				hooks.afterPublication = func([]string) error { return injected }
			case "after-input-cleanup":
				hooks.afterInputCleanup = func(string) error { return injected }
			}
			if _, err := journal.compact(context.Background(), hooks); !errors.Is(err, injected) {
				t.Fatalf("injected error = %v", err)
			}
			assertCompactionRequests(t, journal, "compaction.crash", 3)

			restarted, err := NewJournal(paths, JournalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 4; pass++ {
				report, err := restarted.Compact(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if report.CompactionInputFiles == 0 {
					break
				}
			}
			assertCompactionRequests(t, restarted, "compaction.crash", 3)
			assertNoCompactionTemps(t, paths)
			if len(compactedSegments(t, paths)) != 1 {
				t.Fatalf("segments after restart convergence = %v", compactedSegments(t, paths))
			}
		})
	}
}

func TestJournalCompactIsAtomicForConcurrentReadersAndWriters(t *testing.T) {
	journal, _ := compactionJournalWithRecords(t, "compaction.concurrent", 3)
	published := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := journal.compact(context.Background(), compactionHooks{afterPublication: func([]string) error {
			close(published)
			<-release
			return nil
		}})
		done <- err
	}()
	<-published

	during, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"compaction.concurrent"}}, nil)
	if err != nil || len(during.Records) != 3 || during.Statistics.DuplicatesOmitted != 0 {
		t.Fatalf("snapshot during publication records=%d stats=%+v err=%v", len(during.Records), during.Statistics, err)
	}
	writer := compactionTestEnvelope("compaction.concurrent", "writer", "writer-source", 1, time.Now().UTC())
	if err := journal.Append(context.Background(), writer); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	after, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"compaction.concurrent"}}, nil)
	if err != nil || len(after.Records) != 4 || after.Statistics.DuplicatesOmitted != 0 {
		t.Fatalf("snapshot after cleanup records=%d stats=%+v err=%v", len(after.Records), after.Statistics, err)
	}
}

func TestJournalCompactConcurrentCompactorsDoNotDuplicateRecords(t *testing.T) {
	options := JournalOptions{LockTimeout: 5 * time.Second}
	first, paths := testJournal(t, options)
	base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		record := compactionTestEnvelope("compaction.multiprocess", strconv.Itoa(i), "multiprocess-source", uint64(i+1), base.Add(time.Duration(i)*time.Second))
		writeCompactionSegment(t, paths, fmt.Sprintf("events-input-%d.jsonl", i), marshalCompactionRecord(t, record))
	}
	second, err := NewJournal(paths, options)
	if err != nil {
		t.Fatal(err)
	}

	prepared := make(chan struct{}, 2)
	release := make(chan struct{})
	hooks := compactionHooks{afterPreparation: func([]string) error {
		prepared <- struct{}{}
		<-release
		return nil
	}}
	type result struct {
		report MaintenanceReport
		err    error
	}
	results := make(chan result, 2)
	for _, journal := range []*Journal{first, second} {
		go func(journal *Journal) {
			report, err := journal.compact(context.Background(), hooks)
			results <- result{report: report, err: err}
		}(journal)
	}
	<-prepared
	<-prepared
	close(release)

	committed := 0
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.report.CompactionInputFiles > 0 {
			committed++
		} else if got.report.CompactionInputBytes != 0 || got.report.CompactionOutputFiles != 0 || got.report.CompactionDuplicatesOmitted != 0 {
			t.Fatalf("aborted compactor reported work: %+v", got.report)
		}
	}
	if committed != 1 {
		t.Fatalf("committed compactors = %d, want 1", committed)
	}
	snapshot, err := first.Snapshot(context.Background(), Selector{Stages: []string{"compaction.multiprocess"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 3 || snapshot.Statistics.DuplicatesOmitted != 0 {
		t.Fatalf("concurrent compaction left duplicate records: records=%d stats=%+v", len(snapshot.Records), snapshot.Statistics)
	}
	if segments := compactedSegments(t, paths); len(segments) != 1 {
		t.Fatalf("concurrent compaction outputs = %v, want one", segments)
	}
}

func TestJournalCompactCleansOnlyAbandonedCompactionTemps(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	abandoned := filepath.Join(paths.JournalDir, compactionTempPrefix+"abandoned.tmp")
	active := filepath.Join(paths.JournalDir, compactionTempPrefix+"active.tmp")
	for _, path := range []string{abandoned, active} {
		if err := os.WriteFile(path, []byte("temporary"), fileMode); err != nil {
			t.Fatal(err)
		}
	}
	activeFile, err := os.OpenFile(active, os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(activeFile.Fd()), unix.LOCK_EX); err != nil {
		_ = activeFile.Close()
		t.Fatal(err)
	}
	if _, err := journal.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatalf("abandoned temp remains: %v", err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("locked active temp was removed: %v", err)
	}
	if err := errors.Join(unix.Flock(int(activeFile.Fd()), unix.LOCK_UN), activeFile.Close()); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Fatalf("unlocked abandoned temp remains: %v", err)
	}
}

func TestJournalCompactLeavesOtherStorageNamespacesUntouched(t *testing.T) {
	journal, paths := compactionJournalWithRecords(t, "compaction.namespaces", 2)
	active := []byte("active-journal\n")
	spool := []byte("spool-evidence\n")
	quarantine := []byte("damaged-source\n")
	if err := os.WriteFile(paths.JournalFile, active, fileMode); err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
	if err := os.WriteFile(spoolPath, spool, fileMode); err != nil {
		t.Fatal(err)
	}
	quarantinePath := filepath.Join(paths.JournalDir, "events-old.jsonl.partial-corrupt")
	if err := os.WriteFile(quarantinePath, quarantine, fileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{paths.JournalFile: active, spoolPath: spool, quarantinePath: quarantine} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("unrelated file %s = %q err=%v", path, got, err)
		}
	}
}

func TestJournalMaintainDoesNotScheduleCompaction(t *testing.T) {
	journal, paths := compactionJournalWithRecords(t, "compaction.unscheduled", 2)
	report, err := journal.Maintain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.CompactionInputFiles != 0 || report.CompactionOutputFiles != 0 {
		t.Fatalf("Maintain scheduled compaction: %+v", report)
	}
	inputs, err := filepath.Glob(filepath.Join(paths.JournalDir, "events-input-*.jsonl"))
	if err != nil || len(inputs) != 2 {
		t.Fatalf("unscheduled inputs = %v err=%v", inputs, err)
	}
}
