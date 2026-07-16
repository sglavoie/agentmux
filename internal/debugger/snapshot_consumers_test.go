package debugger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestCollectStatusUsesOneSnapshotForHealthAndLatestEvents(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	segment := snapshotConsumerRecord("segment", now.Add(-time.Minute), 1)
	if err := journal.Append(t.Context(), segment); err != nil {
		t.Fatal(err)
	}
	spool := snapshotConsumerRecord("spool", now.Add(-30*time.Second), 2)
	spoolPath := filepath.Join(paths.SpoolDir, "journal-123.jsonl")
	writeSnapshotConsumerRecords(t, spoolPath, spool)
	if err := os.Chtimes(spoolPath, now.Add(-30*time.Second), now.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}

	report, err := CollectStatus(t.Context(), StatusOptions{
		Config: enabledDiagnosticsConfig(256), Paths: paths, Journal: journal,
		Daemon: testHealthClient{}, Now: now, CLIBuild: buildinfo.Info{Version: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != StatusSchemaVersion || StatusSchemaVersion != 2 {
		t.Fatalf("status schema = %d", report.SchemaVersion)
	}
	if report.Journal.Health.Records != 2 || report.Journal.Health.SpoolFiles != 1 || report.Journal.Health.SpoolBytes == 0 {
		t.Fatalf("journal health does not include spool evidence: %+v", report.Journal.Health)
	}
	if report.JournalSnapshot.SegmentFilesRead != 1 || report.JournalSnapshot.SpoolFilesRead != 1 ||
		report.JournalSnapshot.RecordsScanned != 2 || report.JournalSnapshot.RecordsMatched != 2 {
		t.Fatalf("journal snapshot = %+v", report.JournalSnapshot)
	}
	if len(report.LastSeenEvents) != 2 || report.LastSeenEvents[0].TraceID != "segment" || report.LastSeenEvents[1].TraceID != "spool" {
		t.Fatalf("last seen events = %+v", report.LastSeenEvents)
	}
	if report.Journal.Verdict != VerdictHealthy || hasFinding(report.Findings, "journal_spool_stale") {
		t.Fatalf("young spool evidence was treated as stale: journal=%+v findings=%+v", report.Journal, report.Findings)
	}
	if _, err := os.Stat(spoolPath); err != nil {
		t.Fatalf("status mutated spool evidence: %v", err)
	}
}

func TestCollectStatusWarnsForStaleOrLegacySpoolAndCountsSpoolQuota(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name        string
		fileName    string
		modTime     time.Time
		legacy      int
		wantWarning bool
	}{
		{name: "stale", fileName: "journal-123.jsonl", modTime: now.Add(-2 * time.Minute), wantWarning: true},
		{name: "legacy", fileName: "journal-123-reconciling-1.claim", modTime: now.Add(-time.Second), legacy: 1, wantWarning: true},
		{name: "young-promoting", fileName: "journal-123-promoting-1.claim", modTime: now.Add(-time.Second)},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
			journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			spoolPath := filepath.Join(paths.SpoolDir, test.fileName)
			writeSnapshotConsumerRecords(t, spoolPath, snapshotConsumerRecord(test.name, now, 1))
			if err := os.Chtimes(spoolPath, test.modTime, test.modTime); err != nil {
				t.Fatal(err)
			}
			report, err := CollectStatus(t.Context(), StatusOptions{
				Config: enabledDiagnosticsConfig(256), Paths: paths, Journal: journal,
				Daemon: testHealthClient{}, Now: now, CLIBuild: buildinfo.Info{Version: "test"},
			})
			if err != nil {
				t.Fatal(err)
			}
			warning := hasFinding(report.Findings, "journal_spool_stale")
			if warning != test.wantWarning || (report.Journal.Verdict == VerdictWarning) != test.wantWarning ||
				report.Journal.Health.LegacyClaimFiles != test.legacy {
				t.Fatalf("spool warning = journal %+v findings %+v", report.Journal, report.Findings)
			}
		})
	}

	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "quota"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spoolPath := filepath.Join(paths.SpoolDir, "journal-quota.jsonl")
	data := append([]byte(strings.Repeat(" ", 1<<20)), '\n')
	if err := os.WriteFile(spoolPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(spoolPath, now, now); err != nil {
		t.Fatal(err)
	}
	report, err := CollectStatus(t.Context(), StatusOptions{
		Config: enabledDiagnosticsConfig(1), Paths: paths, Journal: journal,
		Daemon: testHealthClient{}, Now: now, CLIBuild: buildinfo.Info{Version: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(report.Findings, "journal_quota") || hasFinding(report.Findings, "journal_spool_stale") {
		t.Fatalf("spool quota findings = %+v", report.Findings)
	}
}

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

func TestBundleSnapshotsSpoolAndCapsAfterEventTimeOrdering(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var data strings.Builder
	unrelated := snapshotConsumerRecord("unrelated-newest", base.Add(time.Duration(maxBundleRecords+2)*time.Second), uint64(maxBundleRecords+3))
	unrelated.Stage = "other"
	encoded, err := json.Marshal(unrelated)
	if err != nil {
		t.Fatal(err)
	}
	data.Write(encoded)
	data.WriteByte('\n')
	for i := maxBundleRecords; i >= 0; i-- {
		record := snapshotConsumerRecord(fmt.Sprintf("segment-%05d", i), base.Add(time.Duration(i)*time.Second), uint64(i+1))
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data.Write(encoded)
		data.WriteByte('\n')
	}
	if err := os.WriteFile(paths.JournalFile, []byte(data.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	spool := snapshotConsumerRecord("spool-latest", base.Add(time.Duration(maxBundleRecords+1)*time.Second), maxBundleRecords+2)
	writeSnapshotConsumerRecords(t, filepath.Join(paths.SpoolDir, "journal-bundle.jsonl"), spool)
	output := filepath.Join(t.TempDir(), "bundle.tar.gz")
	result, err := CreateBundle(t.Context(), BundleOptions{
		Paths: paths, OutputPath: output, Mode: BundleModeForensic, Journal: journal,
		Now: base, Build: buildinfo.Info{Version: "test"}, Selector: Selector{Stages: []string{"snapshot.consumer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 2 || result.Manifest.SchemaVersion != 2 || !result.Manifest.Truncated {
		t.Fatalf("bundle result = %+v", result)
	}
	members := readBundleMembers(t, output)
	var causal struct {
		SchemaVersion   int                            `json:"schema_version"`
		JournalSnapshot diagnostics.SnapshotStatistics `json:"journal_snapshot"`
		Records         []diagnostics.TraceEnvelope    `json:"records"`
	}
	if err := json.Unmarshal(members["causal-records.json"], &causal); err != nil {
		t.Fatal(err)
	}
	if causal.SchemaVersion != 2 || causal.JournalSnapshot.SpoolFilesRead != 1 || causal.JournalSnapshot.RecordsScanned != maxBundleRecords+3 ||
		causal.JournalSnapshot.RecordsMatched != maxBundleRecords+2 || len(causal.Records) != maxBundleRecords {
		t.Fatalf("causal records metadata = schema %d snapshot %+v records %d", causal.SchemaVersion, causal.JournalSnapshot, len(causal.Records))
	}
	if !causal.Records[0].ObservedAt.Equal(base.Add(2*time.Second)) ||
		!causal.Records[len(causal.Records)-1].ObservedAt.Equal(base.Add(time.Duration(maxBundleRecords+1)*time.Second)) {
		t.Fatalf("causal record bounds = first %+v last %+v", causal.Records[0], causal.Records[len(causal.Records)-1])
	}
}

func snapshotConsumerRecord(traceID string, observedAt time.Time, sequence uint64) diagnostics.TraceEnvelope {
	record := diagnostics.NewEnvelope("snapshot.consumer")
	record.TraceID = traceID
	record.SpanID = "span-" + traceID
	record.SourceID = "source"
	record.SourceSequence = sequence
	record.ObservedAt = observedAt
	return record
}

func writeSnapshotConsumerRecords(t *testing.T, path string, records ...diagnostics.TraceEnvelope) {
	t.Helper()
	var data []byte
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, encoded...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func enabledDiagnosticsConfig(maxMiB int) *config.Config {
	enabled := true
	return &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		JournalEnabled: &enabled, JournalMaxMiB: maxMiB,
	}}}
}

func hasFinding(findings []Finding, check string) bool {
	for _, finding := range findings {
		if finding.Check == check {
			return true
		}
	}
	return false
}

func receiveFollowRecord(t *testing.T, records <-chan TailFollowRecord) TailFollowRecord {
	t.Helper()
	select {
	case record := <-records:
		return record
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for follow record")
		return TailFollowRecord{}
	}
}

func assertNoFollowRecord(t *testing.T, records <-chan TailFollowRecord, duration time.Duration) {
	t.Helper()
	select {
	case record := <-records:
		t.Fatalf("unexpected follow record: %+v", record)
	case <-time.After(duration):
	}
}

func assertFollowTransitionNoReplay(t *testing.T, journal *diagnostics.Journal, traceID string, transition func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	emitted := make(chan TailFollowRecord, 4)
	done := make(chan error, 1)
	go func() {
		done <- FollowWithMetadata(ctx, journal, TailOptions{PollInterval: 5 * time.Millisecond}, func(record TailFollowRecord) error {
			emitted <- record
			return nil
		})
	}()
	if record := receiveFollowRecord(t, emitted); record.Record.TraceID != traceID {
		t.Fatalf("initial follow record = %+v", record)
	}
	if err := transition(); err != nil {
		t.Fatal(err)
	}
	select {
	case duplicate := <-emitted:
		t.Fatalf("transition replayed record: %+v", duplicate)
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("follow returned %v", err)
	}
}
