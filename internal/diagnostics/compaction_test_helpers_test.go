package diagnostics

import (
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

func TestJournalCompactCommitWindowDoesNotBlockAppend(t *testing.T) {
	t.Parallel()

	journal, paths := compactionJournalWithRecords(t, "compaction.commitwindow", 2)
	appended := false
	hooks := compactionHooks{
		afterOutputRename: func(string) error {
			if appended {
				return nil
			}
			appended = true
			// The exclusive events.lock commit window is open right now. The
			// append path is guarded only by append.lock, so this must land in
			// the active segment without waiting for the commit to finish.
			done := make(chan error, 1)
			go func() {
				done <- journal.Append(context.Background(), testEnvelope("compaction.commitwindow.live", "during-commit"))
			}()
			select {
			case err := <-done:
				return err
			case <-time.After(5 * time.Second):
				return errors.New("Append blocked behind the exclusive compaction commit window")
			}
		},
	}
	if _, err := journal.compact(context.Background(), hooks); err != nil {
		t.Fatal(err)
	}
	if !appended {
		t.Fatal("commit window hook never ran")
	}
	spools, _ := filepath.Glob(filepath.Join(paths.SpoolDir, "journal-*.jsonl"))
	if len(spools) != 0 {
		t.Fatalf("append during commit fell back to spool: %v", spools)
	}
	records, err := journal.Read(context.Background(), Selector{Stages: []string{"compaction.commitwindow.live"}})
	if err != nil || len(records) != 1 || records[0].RequestID != "during-commit" {
		t.Fatalf("records appended during commit = %+v, err = %v", records, err)
	}
}

func TestJournalCompactionOutlastsSharedReaderBeyondAppendTimeout(t *testing.T) {
	t.Parallel()

	journal, paths := compactionJournalWithRecords(t, "compaction.sharedreader", 2)
	reader, err := os.OpenFile(filepath.Join(paths.JournalDir, "events.lock"), os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(reader.Fd()), unix.LOCK_SH); err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		// Hold the shared sealed-namespace lock well past Append's 25 ms
		// budget; the maintenance timeout must ride it out.
		time.Sleep(150 * time.Millisecond)
		_ = unix.Flock(int(reader.Fd()), unix.LOCK_UN)
		close(released)
	}()
	t.Cleanup(func() {
		<-released
		_ = reader.Close()
	})

	report, err := journal.Compact(context.Background())
	if err != nil {
		t.Fatalf("compaction under a long shared reader = %v", err)
	}
	if report.CompactionInputFiles != 2 || report.CompactionOutputFiles != 1 {
		t.Fatalf("compaction report = %+v", report)
	}
	assertCompactionRequests(t, journal, "compaction.sharedreader", 2)
}
