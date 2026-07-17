package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestJournalCompactThousandsOfTinySegmentsInChronologicalOrder(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	active := testEnvelope("compaction.tiny", "active")
	active.ObservedAt = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if err := journal.Append(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	activeBefore, err := os.ReadFile(paths.JournalFile)
	if err != nil {
		t.Fatal(err)
	}

	const segmentCount = 2000
	base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < segmentCount; i++ {
		record := compactionTestEnvelope("compaction.tiny", fmt.Sprintf("%04d", i), "tiny-source", uint64(i+1), base.Add(time.Duration(segmentCount-i)*time.Second))
		writeCompactionSegment(t, paths, fmt.Sprintf("events-tiny-%04d.jsonl", i), marshalCompactionRecord(t, record))
	}

	report, err := journal.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.CompactionInputFiles != segmentCount || report.CompactionOutputFiles != 1 || report.CompactionDuplicatesOmitted != 0 {
		t.Fatalf("compaction report = %+v", report)
	}
	activeAfter, err := os.ReadFile(paths.JournalFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(activeBefore, activeAfter) {
		t.Fatal("compaction modified the active journal")
	}

	segments := compactedSegments(t, paths)
	if len(segments) != 1 {
		t.Fatalf("compacted segments = %v", segments)
	}
	previous := time.Time{}
	count := 0
	if err := readJSONL(segments[0], func(record TraceEnvelope) {
		if !previous.IsZero() && record.ObservedAt.Before(previous) {
			t.Errorf("record %d is out of order: %s before %s", count, record.ObservedAt, previous)
		}
		previous = record.ObservedAt
		count++
	}); err != nil {
		t.Fatal(err)
	}
	if count != segmentCount {
		t.Fatalf("compacted record count = %d, want %d", count, segmentCount)
	}
	snapshot, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"compaction.tiny"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != segmentCount+1 || snapshot.Statistics.DuplicatesOmitted != 0 {
		t.Fatalf("snapshot after compaction = records:%d stats:%+v", len(snapshot.Records), snapshot.Statistics)
	}
}

func TestJournalCompactBoundsInputAndSizesOutputs(t *testing.T) {
	t.Run("oldest-input-prefix-is-capped-at-64-MiB", func(t *testing.T) {
		journal, paths := testJournal(t, JournalOptions{})
		base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 5; i++ {
			path := filepath.Join(paths.JournalDir, fmt.Sprintf("events-cap-%d.jsonl", i))
			file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(MaxSegmentBytes); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			modTime := base.Add(time.Duration(i) * time.Second)
			if err := os.Chtimes(path, modTime, modTime); err != nil {
				t.Fatal(err)
			}
		}
		candidates, err := journal.captureCompactionCandidates(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		for _, candidate := range candidates {
			total += candidate.size
		}
		if len(candidates) != 4 || total != maxCompactionInputBytes {
			t.Fatalf("candidates = %d bytes=%d, want 4 and %d", len(candidates), total, maxCompactionInputBytes)
		}
		for i, candidate := range candidates {
			if candidate.name != fmt.Sprintf("events-cap-%d.jsonl", i) {
				t.Fatalf("candidate %d = %s", i, candidate.name)
			}
		}
	})

	t.Run("input-file-count-is-capped", func(t *testing.T) {
		journal, paths := testJournal(t, JournalOptions{})
		for i := 0; i < maxCompactionInputFiles+1; i++ {
			path := filepath.Join(paths.JournalDir, fmt.Sprintf("events-count-cap-%04d.jsonl", i))
			if err := os.WriteFile(path, nil, fileMode); err != nil {
				t.Fatal(err)
			}
		}
		candidates, err := journal.captureCompactionCandidates(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != maxCompactionInputFiles {
			t.Fatalf("candidates = %d, want file-count cap %d", len(candidates), maxCompactionInputFiles)
		}
	})

	t.Run("outputs-target-16-MiB-without-splitting-records", func(t *testing.T) {
		journal, paths := testJournal(t, JournalOptions{})
		const recordCount = 40
		const payloadBytes = 640 << 10
		base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
		for segment := 0; segment < 4; segment++ {
			lines := make([][]byte, 0, recordCount/4)
			for within := 0; within < recordCount/4; within++ {
				i := segment*(recordCount/4) + within
				record := compactionTestEnvelope("compaction.sizing", strconv.Itoa(i), "sizing-source", uint64(i+1), base.Add(time.Duration(i)*time.Second))
				record.Evidence = map[string]string{"detail": strings.Repeat("x", payloadBytes)}
				lines = append(lines, marshalCompactionRecord(t, record))
			}
			writeCompactionSegment(t, paths, fmt.Sprintf("events-sizing-%d.jsonl", segment), lines...)
		}

		report, err := journal.Compact(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		segments := compactedSegments(t, paths)
		if report.CompactionInputFiles != 4 || len(segments) < 2 || len(segments) >= 4 {
			t.Fatalf("sizing report = %+v segments=%v", report, segments)
		}
		for i, path := range segments {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() > MaxSegmentBytes {
				t.Fatalf("output %d size = %d, exceeds %d", i, info.Size(), MaxSegmentBytes)
			}
			if i < len(segments)-1 && info.Size() < MaxSegmentBytes-int64(payloadBytes)-4096 {
				t.Fatalf("non-final output %d is undersized: %d", i, info.Size())
			}
		}
		snapshot, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"compaction.sizing"}}, nil)
		if err != nil || len(snapshot.Records) != recordCount {
			t.Fatalf("sizing snapshot records=%d err=%v", len(snapshot.Records), err)
		}
	})
}

func TestJournalCompactAdvancesPastPackedPrefix(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < int(maxCompactionInputBytes/MaxSegmentBytes); i++ {
		record := compactionTestEnvelope("compaction.packed", fmt.Sprintf("packed-%d", i), "packed-source", uint64(i+1), base.Add(time.Duration(i)*time.Second))
		path := writePackedCompactionSegment(t, paths, fmt.Sprintf("events-packed-%d.jsonl", i), record)
		modTime := base.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	const tinyCount = 3
	for i := 0; i < tinyCount; i++ {
		record := compactionTestEnvelope("compaction.packed", fmt.Sprintf("tiny-%d", i), "tiny-source", uint64(i+1), base.Add(time.Hour+time.Duration(i)*time.Second))
		path := writeCompactionSegment(t, paths, fmt.Sprintf("events-tiny-newer-%d.jsonl", i), marshalCompactionRecord(t, record))
		modTime := base.Add(time.Hour + time.Duration(i)*time.Second)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	preparedBytes := int64(0)
	hooks := compactionHooks{afterPreparation: func(paths []string) error {
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			preparedBytes += info.Size()
		}
		return nil
	}}
	report, err := journal.compact(context.Background(), hooks)
	if err != nil {
		t.Fatal(err)
	}
	if report.CompactionInputFiles != tinyCount || report.CompactionOutputFiles != 1 || preparedBytes == 0 {
		t.Fatalf("packed-prefix compaction report=%+v prepared_bytes=%d", report, preparedBytes)
	}
	for i := 0; i < int(maxCompactionInputBytes/MaxSegmentBytes); i++ {
		if _, err := os.Stat(filepath.Join(paths.JournalDir, fmt.Sprintf("events-packed-%d.jsonl", i))); err != nil {
			t.Fatalf("packed prefix %d was modified: %v", i, err)
		}
	}

	beforeNoop := preparedBytes
	report, err = journal.compact(context.Background(), hooks)
	if err != nil {
		t.Fatal(err)
	}
	if report.CompactionInputFiles != 0 || report.CompactionOutputFiles != 0 || preparedBytes != beforeNoop {
		t.Fatalf("no-op pass wrote temporary output: report=%+v before=%d after=%d", report, beforeNoop, preparedBytes)
	}
}

func TestJournalCompactUsesSnapshotDuplicateIdentities(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	firstSource := compactionTestEnvelope("compaction.dedupe", "source-first", "same-source", 7, base)
	secondSource := compactionTestEnvelope("compaction.dedupe", "source-second", "same-source", 7, base.Add(time.Second))
	legacy := compactionTestEnvelope("compaction.dedupe", "legacy", "", 0, base.Add(2*time.Second))
	unique := compactionTestEnvelope("compaction.dedupe", "unique", "unique-source", 1, base.Add(3*time.Second))
	legacyRaw := marshalCompactionRecord(t, legacy)
	writeCompactionSegment(t, paths, "events-dedupe-1.jsonl", marshalCompactionRecord(t, firstSource), legacyRaw, marshalCompactionRecord(t, unique))
	writeCompactionSegment(t, paths, "events-dedupe-2.jsonl", marshalCompactionRecord(t, secondSource), legacyRaw)

	report, err := journal.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.CompactionDuplicatesOmitted != 2 {
		t.Fatalf("duplicates omitted = %d, want 2", report.CompactionDuplicatesOmitted)
	}
	snapshot, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"compaction.dedupe"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(map[string]bool)
	for _, record := range snapshot.Records {
		requests[record.RequestID] = true
	}
	if len(snapshot.Records) != 3 || !requests["source-first"] || requests["source-second"] || !requests["legacy"] || !requests["unique"] {
		t.Fatalf("deduplicated records = %+v", snapshot.Records)
	}
}

func TestJournalCompactPublishesValidRecordsBeforeQuarantiningCorruptInput(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	first := marshalCompactionRecord(t, compactionTestEnvelope("compaction.corrupt", "first", "corrupt-source", 1, base))
	second := marshalCompactionRecord(t, compactionTestEnvelope("compaction.corrupt", "second", "corrupt-source", 2, base.Add(time.Second)))
	third := marshalCompactionRecord(t, compactionTestEnvelope("compaction.corrupt", "third", "corrupt-source", 3, base.Add(2*time.Second)))
	corruptPath := filepath.Join(paths.JournalDir, "events-corrupt-1.jsonl")
	original := append(append(append(append([]byte{}, first...), '\n'), []byte("{broken}\n")...), second...)
	if err := os.WriteFile(corruptPath, original, fileMode); err != nil {
		t.Fatal(err)
	}
	writeCompactionSegment(t, paths, "events-corrupt-2.jsonl", third)

	report, err := journal.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.CorruptLines != 1 || len(report.QuarantinedFiles) != 1 || report.CompactionOutputFiles != 1 {
		t.Fatalf("corrupt compaction report = %+v", report)
	}
	quarantined, err := os.ReadFile(report.QuarantinedFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(quarantined, original) {
		t.Fatal("corrupt source was rewritten instead of moved intact")
	}
	if _, err := os.Stat(corruptPath); !os.IsNotExist(err) {
		t.Fatalf("corrupt input remains at source path: %v", err)
	}
	snapshot, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"compaction.corrupt"}}, nil)
	if err != nil || len(snapshot.Records) != 3 || snapshot.Health.QuarantinedFiles != 1 {
		t.Fatalf("corrupt snapshot records=%d health=%+v err=%v", len(snapshot.Records), snapshot.Health, err)
	}
}

func TestJournalCompactAbandonsPreparedOutputOnRaceAndCancellation(t *testing.T) {
	for _, race := range []string{"candidate-size-race", "candidate-inode-race"} {
		t.Run(race, func(t *testing.T) {
			journal, paths := compactionJournalWithRecords(t, "compaction.race", 2)
			first := filepath.Join(paths.JournalDir, "events-input-0.jsonl")
			report, err := journal.compact(context.Background(), compactionHooks{afterPreparation: func([]string) error {
				if race == "candidate-size-race" {
					file, err := os.OpenFile(first, os.O_WRONLY|os.O_APPEND, fileMode)
					if err != nil {
						return err
					}
					_, writeErr := file.WriteString("{}\n")
					return errors.Join(writeErr, file.Close())
				}
				data, err := os.ReadFile(first)
				if err != nil {
					return err
				}
				replacement := first + ".replacement"
				if err := os.WriteFile(replacement, data, fileMode); err != nil {
					return err
				}
				return os.Rename(replacement, first)
			}})
			if err != nil {
				t.Fatal(err)
			}
			if report.CompactionInputFiles != 0 || report.CompactionInputBytes != 0 ||
				report.CompactionDuplicatesOmitted != 0 || report.CompactionOutputFiles != 0 ||
				len(compactedSegments(t, paths)) != 0 {
				t.Fatalf("race published output: report=%+v segments=%v", report, compactedSegments(t, paths))
			}
			assertNoCompactionTemps(t, paths)
		})
	}

	t.Run("context-cancelled-after-preparation", func(t *testing.T) {
		journal, paths := compactionJournalWithRecords(t, "compaction.cancel", 2)
		ctx, cancel := context.WithCancel(context.Background())
		_, err := journal.compact(ctx, compactionHooks{afterPreparation: func([]string) error {
			cancel()
			return nil
		}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled compaction error = %v", err)
		}
		if len(compactedSegments(t, paths)) != 0 {
			t.Fatal("cancelled compaction published output")
		}
		assertNoCompactionTemps(t, paths)
	})
}

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

func compactionJournalWithRecords(t *testing.T, stage string, count int) (*Journal, Paths) {
	t.Helper()
	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		record := compactionTestEnvelope(stage, strconv.Itoa(i), stage+"-source", uint64(i+1), base.Add(time.Duration(i)*time.Second))
		writeCompactionSegment(t, paths, fmt.Sprintf("events-input-%d.jsonl", i), marshalCompactionRecord(t, record))
	}
	return journal, paths
}

func compactionTestEnvelope(stage, request, source string, sequence uint64, observed time.Time) TraceEnvelope {
	record := testEnvelope(stage, request)
	record.SourceID = source
	record.SourceSequence = sequence
	record.ObservedAt = observed
	return record
}

func marshalCompactionRecord(t *testing.T, record TraceEnvelope) []byte {
	t.Helper()
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeCompactionSegment(t *testing.T, paths Paths, name string, lines ...[]byte) string {
	t.Helper()
	path := filepath.Join(paths.JournalDir, name)
	data := make([]byte, 0)
	for _, line := range lines {
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, fileMode); err != nil {
		t.Fatal(err)
	}
	return path
}

func writePackedCompactionSegment(t *testing.T, paths Paths, name string, record TraceEnvelope) string {
	t.Helper()
	record.Evidence = map[string]string{"detail": ""}
	base := marshalCompactionRecord(t, record)
	padding := int(MaxSegmentBytes) - len(base) - 1
	if padding < 0 {
		t.Fatalf("record base size %d exceeds packed segment target", len(base))
	}
	record.Evidence["detail"] = strings.Repeat("x", padding)
	raw := marshalCompactionRecord(t, record)
	if int64(len(raw)+1) != MaxSegmentBytes {
		t.Fatalf("packed record size = %d, want %d", len(raw)+1, MaxSegmentBytes)
	}
	path := filepath.Join(paths.JournalDir, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(raw)
	if writeErr == nil {
		_, writeErr = file.Write([]byte{'\n'})
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	return path
}

func compactedSegments(t *testing.T, paths Paths) []string {
	t.Helper()
	segments, err := filepath.Glob(filepath.Join(paths.JournalDir, "events-compacted-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(segments)
	return segments
}

func assertNoCompactionTemps(t *testing.T, paths Paths) {
	t.Helper()
	temps, err := filepath.Glob(filepath.Join(paths.JournalDir, compactionTempPrefix+"*.tmp"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("compaction temps = %v err=%v", temps, err)
	}
}

func assertCompactionRequests(t *testing.T, journal *Journal, stage string, count int) {
	t.Helper()
	snapshot, err := journal.Snapshot(context.Background(), Selector{Stages: []string{stage}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, count)
	for _, record := range snapshot.Records {
		seen[record.RequestID] = true
	}
	if len(snapshot.Records) != count || len(seen) != count {
		t.Fatalf("observable records = %+v stats=%+v, want %d unique", snapshot.Records, snapshot.Statistics, count)
	}
	for i := 0; i < count; i++ {
		if !seen[strconv.Itoa(i)] {
			t.Fatalf("observable records missing request %d: %+v", i, snapshot.Records)
		}
	}
}
