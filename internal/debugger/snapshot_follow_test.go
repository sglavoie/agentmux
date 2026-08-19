package debugger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestTailSelectsLatestRecordsAfterMergedEventTimeOrdering(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []diagnostics.TraceEnvelope{
		snapshotConsumerRecord("newest", base.Add(3*time.Minute), 3),
		snapshotConsumerRecord("oldest", base.Add(time.Minute), 1),
	} {
		if err := journal.Append(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-delayed.jsonl")
	writeSnapshotConsumerRecords(t, spoolPath, snapshotConsumerRecord("middle", base.Add(2*time.Minute), 2))

	report, err := Tail(t.Context(), journal, TailOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 2 || report.JournalSnapshot.RecordsMatched != 3 || len(report.Records) != 2 ||
		report.Records[0].TraceID != "middle" || report.Records[1].TraceID != "newest" {
		t.Fatalf("tail report = %+v", report)
	}
	if _, err := os.Stat(spoolPath); err != nil {
		t.Fatalf("tail mutated spool evidence: %v", err)
	}
}

func TestFollowUsesCursorAndOrdersDelayedEvidenceOnce(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := journal.Append(t.Context(), snapshotConsumerRecord(fmt.Sprintf("initial-%d", i), base.Add(time.Duration(i)*time.Minute), uint64(i))); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	emitted := make(chan TailFollowRecord, 8)
	done := make(chan error, 1)
	go func() {
		done <- FollowWithMetadata(ctx, journal, TailOptions{Limit: 2, PollInterval: 5 * time.Millisecond}, func(record TailFollowRecord) error {
			emitted <- record
			return nil
		})
	}()
	first := receiveFollowRecord(t, emitted)
	second := receiveFollowRecord(t, emitted)
	if first.Record.TraceID != "initial-2" || second.Record.TraceID != "initial-3" {
		t.Fatalf("initial follow records = %s, %s", first.Record.TraceID, second.Record.TraceID)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-delayed.jsonl")
	writeSnapshotConsumerRecords(t, spoolPath,
		snapshotConsumerRecord("delayed-middle", base.Add(90*time.Second), 5),
		snapshotConsumerRecord("delayed-old", base.Add(30*time.Second), 4),
	)
	delayedOld := receiveFollowRecord(t, emitted)
	delayedMiddle := receiveFollowRecord(t, emitted)
	if delayedOld.Record.TraceID != "delayed-old" || delayedMiddle.Record.TraceID != "delayed-middle" ||
		delayedOld.SchemaVersion != 2 || delayedOld.JournalSnapshot.RecordsMatched != 2 {
		t.Fatalf("delayed follow records = %+v, %+v", delayedOld, delayedMiddle)
	}
	select {
	case duplicate := <-emitted:
		t.Fatalf("follow replayed record: %+v", duplicate)
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("follow returned %v", err)
	}
}

func TestFollowDoesNotReplayPromotionOrRotation(t *testing.T) {
	t.Run("promotion", func(t *testing.T) {
		base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
		journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
		if err != nil {
			t.Fatal(err)
		}
		writeSnapshotConsumerRecords(t, filepath.Join(paths.SpoolDir, "journal-promote.jsonl"), snapshotConsumerRecord("promoted", base, 1))
		assertFollowTransitionNoReplay(t, journal, "promoted", func() error {
			_, err := journal.PromoteSpool(t.Context())
			return err
		})
	})

	t.Run("rotation", func(t *testing.T) {
		base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
		journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{SegmentBytes: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := journal.Append(t.Context(), snapshotConsumerRecord("rotated", base, 1)); err != nil {
			t.Fatal(err)
		}
		assertFollowTransitionNoReplay(t, journal, "rotated", func() error {
			_, err := journal.RotateAndRetain(t.Context())
			return err
		})
	})
}

func TestFollowLimitDoesNotReplayHistoryAfterInodeReplacingTailRepair(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if err := journal.Append(t.Context(), snapshotConsumerRecord(
			fmt.Sprintf("initial-%d", sequence), base.Add(time.Duration(sequence)*time.Minute), sequence,
		)); err != nil {
			t.Fatal(err)
		}
	}
	active, err := os.OpenFile(paths.JournalFile, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := active.WriteString("{partial"); err != nil {
		_ = active.Close()
		t.Fatal(err)
	}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	beforeRepair, err := os.Stat(paths.JournalFile)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	emitted := make(chan TailFollowRecord, 8)
	done := make(chan error, 1)
	go func() {
		done <- FollowWithMetadata(ctx, journal, TailOptions{Limit: 1, PollInterval: 5 * time.Millisecond}, func(record TailFollowRecord) error {
			emitted <- record
			return nil
		})
	}()
	if initial := receiveFollowRecord(t, emitted); initial.Record.TraceID != "initial-3" {
		t.Fatalf("initial follow record = %+v", initial)
	}
	newRecord := snapshotConsumerRecord("after-repair", base.Add(4*time.Minute), 4)
	if err := journal.Append(t.Context(), newRecord); err != nil {
		t.Fatal(err)
	}
	afterRepair, err := os.Stat(paths.JournalFile)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(beforeRepair, afterRepair) {
		t.Fatal("tail repair did not replace the active journal inode")
	}
	if next := receiveFollowRecord(t, emitted); next.Record.TraceID != "after-repair" {
		t.Fatalf("tail repair emitted pre-limit history: %+v", next)
	}
	assertNoFollowRecord(t, emitted, 30*time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("follow returned %v", err)
	}
}

func TestFollowLimitDoesNotReplayHistoryAfterLegacyCorruptClaimPromotion(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	claimPath := filepath.Join(paths.SpoolDir, "journal-5152-reconciling-1.claim")
	writeSnapshotConsumerRecords(t, claimPath,
		snapshotConsumerRecord("claim-1", base.Add(time.Minute), 1),
		snapshotConsumerRecord("claim-2", base.Add(2*time.Minute), 2),
		snapshotConsumerRecord("claim-3", base.Add(3*time.Minute), 3),
	)
	claim, err := os.OpenFile(claimPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claim.WriteString("{corrupt}\n"); err != nil {
		_ = claim.Close()
		t.Fatal(err)
	}
	if err := claim.Close(); err != nil {
		t.Fatal(err)
	}
	beforePromotion, err := os.Stat(claimPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	emitted := make(chan TailFollowRecord, 8)
	done := make(chan error, 1)
	go func() {
		done <- FollowWithMetadata(ctx, journal, TailOptions{Limit: 1, PollInterval: 5 * time.Millisecond}, func(record TailFollowRecord) error {
			emitted <- record
			return nil
		})
	}()
	if initial := receiveFollowRecord(t, emitted); initial.Record.TraceID != "claim-3" {
		t.Fatalf("initial follow record = %+v", initial)
	}
	report, err := journal.PromoteSpool(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.PromotedFiles != 1 || report.CorruptLines != 1 {
		t.Fatalf("promotion report = %+v", report)
	}
	segments, err := filepath.Glob(filepath.Join(paths.JournalDir, "events-spool-*.jsonl"))
	if err != nil || len(segments) != 1 {
		t.Fatalf("promoted segments = %v, err = %v", segments, err)
	}
	afterPromotion, err := os.Stat(segments[0])
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(beforePromotion, afterPromotion) {
		t.Fatal("legacy corrupt promotion did not replace the claim inode")
	}
	assertNoFollowRecord(t, emitted, 50*time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("follow returned %v", err)
	}
}
