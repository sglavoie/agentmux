package diagnostics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestJournalRotationAgeAndQuotaRetention(t *testing.T) {
	t.Parallel()

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

func TestJournalRetentionPreservesUnrelatedEventsFiles(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	journal, paths := testJournal(t, JournalOptions{
		KeepDays: 1,
		Now:      func() time.Time { return now },
	})
	segment := filepath.Join(paths.JournalDir, "events-old.jsonl")
	unrelated := filepath.Join(paths.JournalDir, "events-notes.txt")
	for _, path := range []string{segment, unrelated} {
		if err := os.WriteFile(path, []byte("old contents"), fileMode); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-48 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	report, err := journal.RotateAndRetain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedFiles != 1 {
		t.Fatalf("removed %d files, want only the journal segment", report.RemovedFiles)
	}
	if _, err := os.Stat(segment); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old journal segment still exists: %v", err)
	}
	if contents, err := os.ReadFile(unrelated); err != nil || string(contents) != "old contents" {
		t.Fatalf("unrelated file changed: contents %q, error %v", contents, err)
	}
}

func TestRotationUnderAppendLockProceedsDuringSharedSnapshotCapture(t *testing.T) {
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{SegmentBytes: 1})
	reader, err := os.OpenFile(filepath.Join(paths.JournalDir, "events.lock"), os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	// A snapshot capture holds the sealed namespace shared; size-triggered
	// rotation runs under append.lock and must not wait for it.
	if err := unix.Flock(int(reader.Fd()), unix.LOCK_SH); err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	const appendCount = 5
	for i := 0; i < appendCount; i++ {
		if err := journal.Append(context.Background(), testEnvelope("rotation.shared", strconv.Itoa(i))); err != nil {
			t.Fatalf("append %d during shared capture = %v", i, err)
		}
	}
	spools, _ := filepath.Glob(filepath.Join(paths.SpoolDir, "journal-*.jsonl"))
	if len(spools) != 0 {
		t.Fatalf("rotation-triggering appends fell back to spool: %v", spools)
	}
	rotated, _ := filepath.Glob(filepath.Join(paths.JournalDir, "events-*.jsonl"))
	if len(rotated) != appendCount-1 {
		t.Fatalf("rotated segments = %v, want %d", rotated, appendCount-1)
	}
	_ = unix.Flock(int(reader.Fd()), unix.LOCK_UN)
	_ = reader.Close()

	snapshot, err := journal.Snapshot(context.Background(), Selector{Stages: []string{"rotation.shared"}}, nil)
	if err != nil || len(snapshot.Records) != appendCount || snapshot.Statistics.DuplicatesOmitted != 0 {
		t.Fatalf("snapshot after rotation = records:%d stats:%+v err=%v", len(snapshot.Records), snapshot.Statistics, err)
	}
}

func TestJournalSnapshotDoesNotOmitRotatedActiveSegment(t *testing.T) {
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{})
	const seeded = 4
	requests := make(map[string]struct{}, seeded)
	for i := 0; i < seeded; i++ {
		request := "seeded-" + strconv.Itoa(i)
		requests[request] = struct{}{}
		if err := journal.Append(context.Background(), testEnvelope("rotation.omission", request)); err != nil {
			t.Fatalf("seed append %d = %v", i, err)
		}
	}

	// Between the capture's directory listing and its opens, model the rotation
	// an in-flight append performs: rename events.jsonl into a sealed segment
	// the listing never saw and recreate a fresh active file. A real appender
	// rotates only under append.lock, so probing that lock exclusively is a
	// faithful stand-in — it succeeds precisely when a concurrent rotation
	// could interleave. The fix holds append.lock shared across enumerate+open,
	// so the exclusive probe must fail and no rotation may slip in.
	rotated := false
	hook := func() {
		probe, err := os.OpenFile(filepath.Join(paths.JournalDir, "append.lock"), os.O_RDWR, fileMode)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = probe.Close() }()
		lockErr := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if errors.Is(lockErr, unix.EWOULDBLOCK) || errors.Is(lockErr, unix.EAGAIN) {
			return // append.lock is held by the capture: rotation is excluded.
		}
		if lockErr != nil {
			t.Fatalf("probing append.lock during capture: %v", lockErr)
		}
		defer func() { _ = unix.Flock(int(probe.Fd()), unix.LOCK_UN) }()
		sealed := filepath.Join(paths.JournalDir, "events-rotated-during-capture.jsonl")
		if err := os.Rename(paths.JournalFile, sealed); err != nil {
			t.Fatalf("rotating active segment: %v", err)
		}
		fresh := snapshotJSONLine(t, testEnvelope("rotation.omission", "fresh-active"))
		if err := os.WriteFile(paths.JournalFile, fresh, fileMode); err != nil {
			t.Fatalf("recreating active segment: %v", err)
		}
		rotated = true
	}

	snapshot, err := journal.snapshotWithVisitor(
		context.Background(),
		Selector{Stages: []string{"rotation.omission"}},
		nil,
		nil,
		snapshotHooks{betweenEnumerateAndOpen: hook},
	)
	if err != nil {
		t.Fatal(err)
	}
	if rotated {
		t.Fatal("capture did not hold append.lock: a rotation interleaved between enumeration and open")
	}
	if snapshot.Statistics.DuplicatesOmitted != 0 {
		t.Fatalf("snapshot omitted duplicates = %d, want 0", snapshot.Statistics.DuplicatesOmitted)
	}
	if len(snapshot.Records) != seeded {
		t.Fatalf("snapshot records = %d, want all %d seeded records", len(snapshot.Records), seeded)
	}
	seen := make(map[string]int, seeded)
	for _, record := range snapshot.Records {
		seen[record.RequestID]++
	}
	for request := range requests {
		if seen[request] != 1 {
			t.Fatalf("record %q appeared %d times, want exactly once (%v)", request, seen[request], seen)
		}
	}
}
