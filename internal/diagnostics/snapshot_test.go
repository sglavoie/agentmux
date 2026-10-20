package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func snapshotEnvelope(sourceID string, sourceSequence uint64, requestID string, observedAt time.Time) TraceEnvelope {
	envelope := testEnvelope("snapshot", requestID)
	envelope.SourceID = sourceID
	envelope.SourceSequence = sourceSequence
	envelope.ObservedAt = observedAt
	envelope.SpanID = "span-" + requestID
	envelope.TraceID = "trace-" + requestID
	return envelope
}

func snapshotJSONLine(t *testing.T, envelope TraceEnvelope) []byte {
	t.Helper()
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func TestJournalSnapshotTracksObservedHorizonAndPromotedSpoolCounts(t *testing.T) {
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 16, 1, 0, 0, 0, time.UTC)

	// Mixed layout: a sealed segment (earliest record), a maintenance-promoted
	// spool segment in the journal dir, the active journal, and one
	// not-yet-promoted spool-dir file (latest record).
	if err := os.WriteFile(filepath.Join(paths.JournalDir, "events-20260716-010000.jsonl"),
		snapshotJSONLine(t, snapshotEnvelope("sealed", 1, "sealed-1", base)), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.JournalDir, "events-spool-abc123-42-000001.jsonl"),
		snapshotJSONLine(t, snapshotEnvelope("promoted", 1, "promoted-1", base.Add(time.Hour))), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.JournalFile,
		snapshotJSONLine(t, snapshotEnvelope("active", 1, "active-1", base.Add(2*time.Hour))), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.SpoolDir, "journal-123.jsonl"),
		snapshotJSONLine(t, snapshotEnvelope("spool", 1, "spool-1", base.Add(3*time.Hour))), fileMode); err != nil {
		t.Fatal(err)
	}

	snapshot, err := journal.Snapshot(context.Background(), Selector{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stats := snapshot.Statistics
	if stats.SegmentFilesRead != 3 || stats.PromotedSpoolFilesRead != 1 || stats.SpoolFilesRead != 1 {
		t.Fatalf("statistics = %+v, want 3 segments (1 promoted) and 1 spool file", stats)
	}
	if !snapshot.Health.EarliestObservedAt.Equal(base) {
		t.Fatalf("earliest observed = %s, want %s", snapshot.Health.EarliestObservedAt, base)
	}
	if !snapshot.Health.LatestObservedAt.Equal(base.Add(3 * time.Hour)) {
		t.Fatalf("latest observed = %s, want %s", snapshot.Health.LatestObservedAt, base.Add(3*time.Hour))
	}

	health, err := journal.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Inspect reads journal-dir segments only; the spool record is excluded.
	if !health.EarliestObservedAt.Equal(base) || !health.LatestObservedAt.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("inspect horizon = [%s, %s], want [%s, %s]", health.EarliestObservedAt, health.LatestObservedAt, base, base.Add(2*time.Hour))
	}
}

func TestJournalSnapshotVisitorRunsAfterLocksAreReleased(t *testing.T) {
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{LockTimeout: 100 * time.Millisecond})
	base := time.Date(2026, 7, 16, 1, 0, 0, 0, time.UTC)
	var data bytes.Buffer
	for sequence := uint64(1); sequence <= 512; sequence++ {
		envelope := snapshotEnvelope("bulk", sequence, fmt.Sprintf("bulk-%03d", sequence), base)
		envelope.Evidence = map[string]string{"detail": string(bytes.Repeat([]byte{'x'}, 4096))}
		data.Write(snapshotJSONLine(t, envelope))
	}
	if err := os.WriteFile(paths.JournalFile, data.Bytes(), fileMode); err != nil {
		t.Fatal(err)
	}
	prefixBytes := int64(data.Len())

	visitorStarted := make(chan struct{})
	releaseVisitor := make(chan struct{})
	type snapshotResult struct {
		snapshot JournalSnapshot
		err      error
	}
	result := make(chan snapshotResult, 1)
	visited := 0
	go func() {
		snapshot, err := journal.SnapshotWithVisitor(context.Background(), Selector{}, nil, func(TraceEnvelope) error {
			visited++
			if visited == 1 {
				close(visitorStarted)
				<-releaseVisitor
			}
			return nil
		})
		result <- snapshotResult{snapshot: snapshot, err: err}
	}()
	<-visitorStarted

	writer := snapshotEnvelope("writer", 1, "during-snapshot", base.Add(time.Hour))
	writerDone := make(chan error, 1)
	go func() { writerDone <- journal.Append(context.Background(), writer) }()
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatalf("append during snapshot: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("append remained blocked while snapshot parsing was paused")
	}
	spools, err := filepath.Glob(filepath.Join(paths.SpoolDir, "journal-*.jsonl"))
	if err != nil || len(spools) != 0 {
		t.Fatalf("append fell back to spool during snapshot: files=%v err=%v", spools, err)
	}
	close(releaseVisitor)

	first := <-result
	if first.err != nil {
		t.Fatal(first.err)
	}
	if visited != 512 {
		t.Fatalf("visitor records = %d, want 512", visited)
	}
	if len(first.snapshot.Records) != 512 {
		t.Fatalf("fixed prefix records = %d, want 512", len(first.snapshot.Records))
	}
	if first.snapshot.Statistics.BytesScanned != prefixBytes {
		t.Fatalf("bytes scanned = %d, want captured prefix %d", first.snapshot.Statistics.BytesScanned, prefixBytes)
	}
	for _, record := range first.snapshot.Records {
		if record.RequestID == writer.RequestID {
			t.Fatal("snapshot read an append beyond its captured byte ceiling")
		}
	}

	delta, err := journal.Snapshot(context.Background(), Selector{}, &first.snapshot.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 1 || delta.Records[0].RequestID != writer.RequestID {
		t.Fatalf("incremental records = %+v, want the concurrent append once", delta.Records)
	}
}

func TestJournalSnapshotMergesOrdersAndAdvancesIncompleteCursor(t *testing.T) {
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 16, 2, 0, 0, 0, time.UTC)
	early := snapshotEnvelope("source-c", 3, "early", base)
	duplicate := snapshotEnvelope("source-a", 1, "duplicate", base.Add(time.Minute))
	late := snapshotEnvelope("source-b", 2, "late", base.Add(2*time.Minute))
	legacy := snapshotEnvelope("", 0, "legacy", base.Add(3*time.Minute))
	partial := snapshotEnvelope("source-d", 4, "partial", base.Add(4*time.Minute))

	duplicateLine := snapshotJSONLine(t, duplicate)
	legacyLine := snapshotJSONLine(t, legacy)
	segmentData := append(append(append([]byte(nil), duplicateLine...), snapshotJSONLine(t, late)...), legacyLine...)
	if err := os.WriteFile(paths.JournalFile, segmentData, fileMode); err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-4242.jsonl")
	spoolData := append(append(append(append([]byte(nil), snapshotJSONLine(t, early)...), duplicateLine...), []byte("{corrupt}\n")...), legacyLine...)
	partialLine := snapshotJSONLine(t, partial)
	spoolData = append(spoolData, partialLine[:len(partialLine)-1]...)
	if err := os.WriteFile(spoolPath, spoolData, fileMode); err != nil {
		t.Fatal(err)
	}

	var visited []string
	first, err := journal.SnapshotWithVisitor(context.Background(), Selector{Limit: 2}, nil, func(envelope TraceEnvelope) error {
		visited = append(visited, envelope.RequestID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"duplicate", "late", "legacy", "early"}; !reflect.DeepEqual(visited, want) {
		t.Fatalf("visitor journal order = %v, want %v", visited, want)
	}
	if got := []string{first.Records[0].RequestID, first.Records[1].RequestID}; !reflect.DeepEqual(got, []string{"late", "legacy"}) {
		t.Fatalf("limited event-time order = %v, want [late legacy]", got)
	}
	if first.Statistics.SegmentFilesRead != 1 || first.Statistics.SpoolFilesRead != 1 ||
		first.Statistics.RecordsScanned != 6 || first.Statistics.RecordsMatched != 4 ||
		first.Statistics.DuplicatesOmitted != 2 || first.Statistics.CorruptLines != 1 {
		t.Fatalf("snapshot statistics = %+v", first.Statistics)
	}
	if first.Health.Records != 4 || first.Health.CorruptLines != 1 || first.Health.SpoolFiles != 1 {
		t.Fatalf("snapshot health = %+v", first.Health)
	}

	var filteredVisited []string
	filtered, err := journal.SnapshotWithVisitor(context.Background(), Selector{RequestID: "late", Limit: 1}, nil, func(envelope TraceEnvelope) error {
		filteredVisited = append(filteredVisited, envelope.RequestID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Records) != 1 || filtered.Records[0].RequestID != "late" || filtered.Statistics.RecordsMatched != 1 {
		t.Fatalf("filtered snapshot = records %+v statistics %+v", filtered.Records, filtered.Statistics)
	}
	if !reflect.DeepEqual(filteredVisited, visited) {
		t.Fatalf("selector changed visitor records: got %v, want %v", filteredVisited, visited)
	}

	spool, err := os.OpenFile(spoolPath, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spool.WriteString("\n"); err != nil {
		_ = spool.Close()
		t.Fatal(err)
	}
	if err := spool.Close(); err != nil {
		t.Fatal(err)
	}
	var deltaVisited []string
	delta, err := journal.SnapshotWithVisitor(context.Background(), Selector{Limit: 2}, &first.Cursor, func(envelope TraceEnvelope) error {
		deltaVisited = append(deltaVisited, envelope.RequestID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 1 || delta.Records[0].RequestID != "partial" {
		t.Fatalf("completed trailing record delta = %+v", delta.Records)
	}
	if delta.Statistics.RecordsScanned != 1 || delta.Statistics.CorruptLines != 0 {
		t.Fatalf("completed trailing record statistics = %+v", delta.Statistics)
	}
	if !reflect.DeepEqual(deltaVisited, []string{"partial"}) {
		t.Fatalf("delta visitor records = %v, want [partial]", deltaVisited)
	}

	claimPath := filepath.Join(paths.SpoolDir, "journal-4242-reconciling-1.claim")
	if err := os.Rename(spoolPath, claimPath); err != nil {
		t.Fatal(err)
	}
	promoted, err := journal.Snapshot(context.Background(), Selector{}, &delta.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted.Records) != 0 || promoted.Statistics.BytesScanned != 0 {
		t.Fatalf("renamed spool replayed: records=%+v statistics=%+v", promoted.Records, promoted.Statistics)
	}

	rotatedPath := filepath.Join(paths.JournalDir, "events-20260716T020500.000000000Z-1-000001.jsonl")
	if err := os.Rename(paths.JournalFile, rotatedPath); err != nil {
		t.Fatal(err)
	}
	rotatedNew := snapshotEnvelope("source-e", 5, "after-rotation", base.Add(5*time.Minute))
	if err := os.WriteFile(paths.JournalFile, snapshotJSONLine(t, rotatedNew), fileMode); err != nil {
		t.Fatal(err)
	}
	rotated, err := journal.Snapshot(context.Background(), Selector{}, &promoted.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(rotated.Records) != 1 || rotated.Records[0].RequestID != "after-rotation" {
		t.Fatalf("rotation delta = %+v", rotated.Records)
	}
}

func TestJournalSnapshotVisitorErrorsDoNotAdvanceCursor(t *testing.T) {
	t.Parallel()

	t.Run("visitor error", func(t *testing.T) {
		journal, _ := testJournal(t, JournalOptions{})
		base := time.Date(2026, 7, 16, 2, 30, 0, 0, time.UTC)
		if err := journal.Append(context.Background(), snapshotEnvelope("source", 1, "baseline", base)); err != nil {
			t.Fatal(err)
		}
		baseline, err := journal.Snapshot(context.Background(), Selector{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for sequence, requestID := range []string{"delta-one", "delta-two"} {
			if err := journal.Append(context.Background(), snapshotEnvelope("source", uint64(sequence+2), requestID, base.Add(time.Duration(sequence+1)*time.Minute))); err != nil {
				t.Fatal(err)
			}
		}

		visitorErr := errors.New("visitor failed")
		visited := 0
		failed, err := journal.SnapshotWithVisitor(context.Background(), Selector{}, &baseline.Cursor, func(TraceEnvelope) error {
			visited++
			return visitorErr
		})
		if !errors.Is(err, visitorErr) {
			t.Fatalf("visitor error = %v, want %v", err, visitorErr)
		}
		if visited != 1 {
			t.Fatalf("visitor calls after failure = %d, want 1", visited)
		}
		if failed.Cursor.offsets != nil || failed.Cursor.missingCaptures != nil {
			t.Fatalf("failed traversal returned advancing cursor: %+v", failed.Cursor)
		}

		retry, err := journal.Snapshot(context.Background(), Selector{}, &baseline.Cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(retry.Records) != 2 {
			t.Fatalf("retry record count = %d, want 2", len(retry.Records))
		}
		if got := []string{retry.Records[0].RequestID, retry.Records[1].RequestID}; !reflect.DeepEqual(got, []string{"delta-one", "delta-two"}) {
			t.Fatalf("retry records = %v, want both deltas", got)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		journal, _ := testJournal(t, JournalOptions{})
		base := time.Date(2026, 7, 16, 2, 45, 0, 0, time.UTC)
		if err := journal.Append(context.Background(), snapshotEnvelope("source", 1, "baseline", base)); err != nil {
			t.Fatal(err)
		}
		baseline, err := journal.Snapshot(context.Background(), Selector{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for sequence, requestID := range []string{"delta-one", "delta-two"} {
			if err := journal.Append(context.Background(), snapshotEnvelope("source", uint64(sequence+2), requestID, base.Add(time.Duration(sequence+1)*time.Minute))); err != nil {
				t.Fatal(err)
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		visited := 0
		failed, err := journal.SnapshotWithVisitor(ctx, Selector{}, &baseline.Cursor, func(TraceEnvelope) error {
			visited++
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v, want %v", err, context.Canceled)
		}
		if visited != 1 {
			t.Fatalf("visitor calls after cancellation = %d, want 1", visited)
		}
		if failed.Cursor.offsets != nil || failed.Cursor.missingCaptures != nil {
			t.Fatalf("cancelled traversal returned advancing cursor: %+v", failed.Cursor)
		}

		retry, err := journal.Snapshot(context.Background(), Selector{}, &baseline.Cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(retry.Records) != 2 {
			t.Fatalf("retry record count = %d, want 2", len(retry.Records))
		}
		if got := []string{retry.Records[0].RequestID, retry.Records[1].RequestID}; !reflect.DeepEqual(got, []string{"delta-one", "delta-two"}) {
			t.Fatalf("retry records = %v, want both deltas", got)
		}
	})
}
