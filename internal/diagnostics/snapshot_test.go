package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestJournalSnapshotReleasesLocksBeforeSlowPrefixRead(t *testing.T) {
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

	captured := make(chan struct{})
	releaseRead := make(chan struct{})
	type snapshotResult struct {
		snapshot JournalSnapshot
		err      error
	}
	result := make(chan snapshotResult, 1)
	go func() {
		snapshot, err := journal.snapshot(context.Background(), Selector{}, nil, func() {
			close(captured)
			<-releaseRead
		})
		result <- snapshotResult{snapshot: snapshot, err: err}
	}()
	<-captured

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
	close(releaseRead)

	first := <-result
	if first.err != nil {
		t.Fatal(first.err)
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

	first, err := journal.Snapshot(context.Background(), Selector{Limit: 2}, nil)
	if err != nil {
		t.Fatal(err)
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
	delta, err := journal.Snapshot(context.Background(), Selector{Limit: 2}, &first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 1 || delta.Records[0].RequestID != "partial" {
		t.Fatalf("completed trailing record delta = %+v", delta.Records)
	}
	if delta.Statistics.RecordsScanned != 1 || delta.Statistics.CorruptLines != 0 {
		t.Fatalf("completed trailing record statistics = %+v", delta.Statistics)
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

func TestJournalSnapshotReadsOpenedFilesAfterRenameAndRemoval(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)
	segment := snapshotEnvelope("segment", 1, "segment", base)
	spooled := snapshotEnvelope("spool", 1, "spool", base.Add(time.Minute))
	if err := os.WriteFile(paths.JournalFile, snapshotJSONLine(t, segment), fileMode); err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-5150.jsonl")
	if err := os.WriteFile(spoolPath, snapshotJSONLine(t, spooled), fileMode); err != nil {
		t.Fatal(err)
	}

	snapshot, err := journal.snapshot(context.Background(), Selector{}, nil, func() {
		if renameErr := os.Rename(paths.JournalFile, filepath.Join(paths.JournalDir, "captured-removed")); renameErr != nil {
			t.Fatal(renameErr)
		}
		if removeErr := os.Remove(spoolPath); removeErr != nil {
			t.Fatal(removeErr)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{snapshot.Records[0].RequestID, snapshot.Records[1].RequestID}; !reflect.DeepEqual(got, []string{"segment", "spool"}) {
		t.Fatalf("opened-descriptor records = %v", got)
	}
}

func TestJournalSnapshotCarriesAndPrunesCursorOffsetsForMissingFiles(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	spoolPath := filepath.Join(paths.SpoolDir, "journal-5151.jsonl")
	write := snapshotJSONLine(t, snapshotEnvelope("spool", 1, "spool", time.Date(2026, 7, 16, 3, 15, 0, 0, time.UTC)))
	if err := os.WriteFile(spoolPath, write, fileMode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := snapshotIdentityFromInfo(info)
	if err != nil {
		t.Fatal(err)
	}

	first, err := journal.Snapshot(context.Background(), Selector{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hiddenPath := filepath.Join(paths.SpoolDir, "temporarily-hidden")
	if err := os.Rename(spoolPath, hiddenPath); err != nil {
		t.Fatal(err)
	}
	missing, err := journal.Snapshot(context.Background(), Selector{}, &first.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.Records) != 0 || missing.Cursor.offsets[identity] != int64(len(write)) ||
		missing.Cursor.missingCaptures[identity] != 1 {
		t.Fatalf("missing-file cursor = records %+v offsets %+v missing %+v",
			missing.Records, missing.Cursor.offsets, missing.Cursor.missingCaptures)
	}
	if err := os.Rename(hiddenPath, spoolPath); err != nil {
		t.Fatal(err)
	}
	restored, err := journal.Snapshot(context.Background(), Selector{}, &missing.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Records) != 0 || restored.Statistics.BytesScanned != 0 {
		t.Fatalf("temporarily missing file replayed: records=%+v statistics=%+v", restored.Records, restored.Statistics)
	}
	if _, missing := restored.Cursor.missingCaptures[identity]; missing {
		t.Fatalf("restored cursor retained missing count: %+v", restored.Cursor.missingCaptures)
	}

	if err := os.Rename(spoolPath, hiddenPath); err != nil {
		t.Fatal(err)
	}
	withinGrace, err := journal.Snapshot(context.Background(), Selector{}, &restored.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if withinGrace.Cursor.offsets[identity] != int64(len(write)) ||
		withinGrace.Cursor.missingCaptures[identity] != snapshotCursorMissingCaptureGrace {
		t.Fatalf("cursor was not carried through grace: offsets %+v missing %+v",
			withinGrace.Cursor.offsets, withinGrace.Cursor.missingCaptures)
	}
	pruned, err := journal.Snapshot(context.Background(), Selector{}, &withinGrace.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if _, retained := pruned.Cursor.offsets[identity]; retained {
		t.Fatalf("cursor survived beyond missing grace: offsets %+v missing %+v",
			pruned.Cursor.offsets, pruned.Cursor.missingCaptures)
	}
}

func TestJournalSnapshotResetsMisalignedReusedInodeCursor(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 16, 3, 20, 0, 0, time.UTC)
	data := append(
		snapshotJSONLine(t, snapshotEnvelope("reused", 1, "first", base)),
		snapshotJSONLine(t, snapshotEnvelope("reused", 2, "second", base.Add(time.Minute)))...,
	)
	if err := os.WriteFile(paths.JournalFile, data, fileMode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(paths.JournalFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := snapshotIdentityFromInfo(info)
	if err != nil {
		t.Fatal(err)
	}
	cursor := SnapshotCursor{offsets: map[snapshotFileIdentity]int64{identity: 1}}

	snapshot, err := journal.Snapshot(context.Background(), Selector{}, &cursor)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{snapshot.Records[0].RequestID, snapshot.Records[1].RequestID}; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("reused-inode records = %v, want [first second]", got)
	}
	if snapshot.Statistics.CorruptLines != 0 || snapshot.Statistics.BytesScanned != int64(len(data)) {
		t.Fatalf("reused-inode statistics = %+v", snapshot.Statistics)
	}
}

func TestJournalSnapshotDefersTrailingLineUntilActiveSpoolIsSealed(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 16, 3, 30, 0, 0, time.UTC)
	trailing := snapshotJSONLine(t, snapshotEnvelope("spool", 1, "sealed-tail", base))
	spoolPath := filepath.Join(paths.SpoolDir, "journal-7171.jsonl")
	if err := os.WriteFile(spoolPath, trailing[:len(trailing)-1], fileMode); err != nil {
		t.Fatal(err)
	}

	active, err := journal.Snapshot(context.Background(), Selector{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Records) != 0 || active.Statistics.CorruptLines != 0 {
		t.Fatalf("active trailing line was consumed: records=%+v statistics=%+v", active.Records, active.Statistics)
	}
	claimPath := filepath.Join(paths.SpoolDir, "journal-7171-reconciling-1.claim")
	if err := os.Rename(spoolPath, claimPath); err != nil {
		t.Fatal(err)
	}

	sealed, err := journal.Snapshot(context.Background(), Selector{}, &active.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed.Records) != 1 || sealed.Records[0].RequestID != "sealed-tail" {
		t.Fatalf("sealed trailing record = %+v", sealed.Records)
	}
}

func TestJournalSnapshotReadPathDoesNotChmodOrQuarantine(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 16, 4, 0, 0, 0, time.UTC)
	if err := os.WriteFile(paths.JournalFile, snapshotJSONLine(t, snapshotEnvelope("segment", 1, "segment", base)), 0o640); err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-6161.jsonl")
	if err := os.WriteFile(spoolPath, append(snapshotJSONLine(t, snapshotEnvelope("spool", 1, "spool", base)), []byte("{broken}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.JournalFile, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(spoolPath, 0o644); err != nil {
		t.Fatal(err)
	}
	beforeSegment, err := os.Stat(paths.JournalFile)
	if err != nil {
		t.Fatal(err)
	}
	beforeSpool, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := journal.Snapshot(context.Background(), Selector{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Statistics.CorruptLines != 1 || len(snapshot.Records) != 2 {
		t.Fatalf("tolerant read = records %d statistics %+v", len(snapshot.Records), snapshot.Statistics)
	}
	afterSegment, err := os.Stat(paths.JournalFile)
	if err != nil {
		t.Fatal(err)
	}
	afterSpool, err := os.Stat(spoolPath)
	if err != nil {
		t.Fatal(err)
	}
	if beforeSegment.Mode().Perm() != afterSegment.Mode().Perm() || beforeSpool.Mode().Perm() != afterSpool.Mode().Perm() {
		t.Fatalf("snapshot chmodded inputs: segment %o -> %o spool %o -> %o",
			beforeSegment.Mode().Perm(), afterSegment.Mode().Perm(), beforeSpool.Mode().Perm(), afterSpool.Mode().Perm())
	}
	quarantines, err := filepath.Glob(filepath.Join(paths.SpoolDir, "*.partial-*"))
	if err != nil || len(quarantines) != 0 {
		t.Fatalf("snapshot quarantined corrupt evidence: files=%v err=%v", quarantines, err)
	}
}
