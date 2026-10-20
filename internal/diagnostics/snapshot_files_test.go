package diagnostics

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestJournalSnapshotVisitorFailureAcrossFilesDoesNotAdvanceCursor(t *testing.T) {
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 16, 2, 55, 0, 0, time.UTC)
	firstFileRecords := []TraceEnvelope{
		snapshotEnvelope("first-file", 1, "first-one", base),
		snapshotEnvelope("first-file", 2, "first-two", base.Add(time.Minute)),
	}
	var firstFileData bytes.Buffer
	for _, envelope := range firstFileRecords {
		firstFileData.Write(snapshotJSONLine(t, envelope))
	}
	firstFilePath := filepath.Join(paths.JournalDir, "events-20260716T025500.000000000Z-1-000001.jsonl")
	if err := os.WriteFile(firstFilePath, firstFileData.Bytes(), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.JournalFile, snapshotJSONLine(t, snapshotEnvelope("second-file", 1, "second-one", base.Add(2*time.Minute))), fileMode); err != nil {
		t.Fatal(err)
	}

	visitorErr := errors.New("second-file visitor failed")
	var visited []string
	failed, err := journal.SnapshotWithVisitor(context.Background(), Selector{}, nil, func(envelope TraceEnvelope) error {
		visited = append(visited, envelope.RequestID)
		if envelope.RequestID == "second-one" {
			return visitorErr
		}
		return nil
	})
	if !errors.Is(err, visitorErr) {
		t.Fatalf("visitor error = %v, want %v", err, visitorErr)
	}
	if want := []string{"first-one", "first-two", "second-one"}; !reflect.DeepEqual(visited, want) {
		t.Fatalf("visited records before failure = %v, want %v", visited, want)
	}
	if failed.Statistics.SegmentFilesRead != 2 {
		t.Fatalf("segment files read before failure = %d, want 2", failed.Statistics.SegmentFilesRead)
	}
	if len(failed.Cursor.offsets) != 0 || len(failed.Cursor.missingCaptures) != 0 {
		t.Fatalf("failed traversal returned advancing cursor: %+v", failed.Cursor)
	}

	var retryVisited []string
	_, err = journal.SnapshotWithVisitor(context.Background(), Selector{}, &failed.Cursor, func(envelope TraceEnvelope) error {
		retryVisited = append(retryVisited, envelope.RequestID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first-one", "first-two", "second-one"}; !reflect.DeepEqual(retryVisited, want) {
		t.Fatalf("retry visitor records = %v, want first-file records redelivered as %v", retryVisited, want)
	}
}

func TestJournalSnapshotReadsOpenedFilesAfterRenameAndRemoval(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{})
	base := time.Date(2026, 7, 16, 4, 0, 0, 0, time.UTC)
	if err := os.WriteFile(paths.JournalFile, snapshotJSONLine(t, snapshotEnvelope("segment", 1, "segment", base)), 0o640); err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-6161.jsonl")
	spoolData := append([]byte("{broken}\n"), snapshotJSONLine(t, snapshotEnvelope("spool", 1, "spool", base))...)
	if err := os.WriteFile(spoolPath, spoolData, 0o644); err != nil {
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

	var visited []string
	snapshot, err := journal.SnapshotWithVisitor(context.Background(), Selector{}, nil, func(envelope TraceEnvelope) error {
		visited = append(visited, envelope.RequestID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Statistics.CorruptLines != 1 || len(snapshot.Records) != 2 {
		t.Fatalf("tolerant read = records %d statistics %+v", len(snapshot.Records), snapshot.Statistics)
	}
	if !reflect.DeepEqual(visited, []string{"segment", "spool"}) {
		t.Fatalf("visitor did not tolerate corrupt line: records %v", visited)
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
