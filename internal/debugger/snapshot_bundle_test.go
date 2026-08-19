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

func acceptanceSegmentFiles(t *testing.T, paths diagnostics.Paths) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(paths.JournalDir, "events-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func acceptanceHookStatusDigest(report HookReport) map[string]string {
	digest := make(map[string]string, len(report.Profiles))
	for _, profile := range report.Profiles {
		lastSeen := ""
		if profile.LastSeen != nil {
			lastSeen = profile.LastSeen.UTC().Format(time.RFC3339Nano)
		}
		digest[profile.Harness+"/"+profile.Profile] = fmt.Sprintf("%s|%s|%t", profile.Verdict, lastSeen, profile.Silent)
	}
	return digest
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
