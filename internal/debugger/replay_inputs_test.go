package debugger

// Which inputs replay accepts and which it refuses. Replay is an offline
// reader: it takes a journal, a bundle, or an events export, and every schema
// it claims to support has to round-trip without the caller supplying evidence
// the daemon never recorded — a v8 export is missing enough to be reported
// incomplete rather than guessed at, while v9 and newer replay fully. The
// refusals are equally load-bearing: an unsupported schema, a symlinked path,
// and an archive over the member-count limit are all rejected before anything
// is read, and replay.go must never reach for a live client.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
)

func TestReplaySchemaV8UsesOnlySufficientEvidenceAndReportsLimits(t *testing.T) {
	input := filepath.Join(t.TempDir(), "events-v8.json")
	data := []byte(`{"schema_version":8,"windows":[{"window_id":42,"events":[{"at":100,"event":"PreToolUse","action":"active","result_icon_state":"bolt","source_kind":"harness","source_key":"claude:legacy","source_seq":1}]}]}`)
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := Replay(t.Context(), input, ReplayOptions{Selector: Selector{WindowID: 42}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Input.SchemaVersion != daemon.EventsExportSchemaV8 || !report.Success || report.Complete || len(report.Comparisons) != 1 {
		t.Fatalf("v8 report = %+v", report)
	}
	comparison := report.Comparisons[0]
	if comparison.StateMatch == nil || !*comparison.StateMatch || comparison.Status != "incomplete" {
		t.Fatalf("v8 comparison = %+v", comparison)
	}
}

func TestReplayAcceptsCompleteEventsExportSchemasV9AndNewer(t *testing.T) {
	payload := daemon.ExportEventsPayload{Windows: []daemon.ExportWindow{{
		WindowID: 42,
		Shadow: daemon.WindowDebugShadow{Facts: []daemon.SourceFactSnapshot{{
			Kind: daemon.SourceHarness, Key: "claude:s1", Action: "active", Icon: "bolt", Seq: 1,
			HarnessProcessHarness: "claude", HarnessProcessPID: 4321,
			HarnessProcessStartToken: "Mon Jul 13 10:11:12 2026",
		}}},
		Events: []daemon.SessionEvent{{
			At: 100, Event: "PreToolUse", Action: "active", ResultIconState: "bolt",
			TraceID: "trace-1", RequestID: "request-1", SourceEventID: "source-1",
			ProducerHarness: "claude", SourceKind: string(daemon.SourceHarness), SourceKey: "claude:s1",
			SourceSeq: 1, DaemonSeq: 1, ReducerInputAction: "active",
			ReducerInput:    &daemon.ReducerInputEvidence{EmittedAt: 100, GuardDuration: 5},
			ReducerDecision: daemon.DecisionApplied, ReducerReason: string(daemon.DecisionApplied),
			EffectiveBefore: &daemon.EffectiveStateEvidence{},
			EffectiveAfter: &daemon.EffectiveStateEvidence{
				Icon: "bolt", SourceKind: string(daemon.SourceHarness), SourceKey: "claude:s1",
			},
			RouteEvidence: &daemon.RoutingEvidence{
				SnapshotReady: true, LookupOutcome: "source_found", SourceWindowID: 42, TargetWindowID: 42,
				SourceMatched: true, SelectionReason: "source", ChosenRoute: "source",
			},
		}},
	}}}
	for _, schema := range []int{daemon.EventsExportSchemaV9, daemon.EventsExportSchemaV10, daemon.EventsExportSchemaV11, daemon.EventsExportSchemaVersion} {
		t.Run(strconv.Itoa(schema), func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "events.json")
			projected, err := daemon.ProjectExportEventsPayload(payload, schema)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(projected)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(input, data, 0o600); err != nil {
				t.Fatal(err)
			}
			report, err := Replay(t.Context(), input, ReplayOptions{})
			if err != nil {
				t.Fatalf("Replay(schema %d): %v", schema, err)
			}
			if report.Input.Format != "events-export" || report.Input.SchemaVersion != schema || !report.Success || !report.Complete ||
				len(report.Comparisons) != 1 || report.Comparisons[0].Status != "matched" {
				t.Fatalf("schema %d report = %+v", schema, report)
			}
		})
	}
}

func TestReplayRejectsUnsupportedEventsExportSchema(t *testing.T) {
	input := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(input, []byte(`{"schema_version":13,"windows":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(t.Context(), input, ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "unsupported events export schema 13") {
		t.Fatalf("unsupported events export error = %v", err)
	}
}

func TestReplayRejectsLiveAndUnsupportedInputsWithoutMutation(t *testing.T) {
	if _, err := Replay(t.Context(), filepath.Join(t.TempDir(), "missing"), ReplayOptions{}); err == nil {
		t.Fatal("missing input was accepted")
	}
	unsupported := filepath.Join(t.TempDir(), "unsupported.jsonl")
	if err := os.WriteFile(unsupported, []byte(`{"schema_version":99,"stage":"daemon.decision"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(t.Context(), unsupported, ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported schema error = %v", err)
	}
	link := filepath.Join(t.TempDir(), "journal-link")
	if err := os.Symlink(unsupported, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(t.Context(), link, ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink error = %v", err)
	}
	source, err := os.ReadFile("replay.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "internal/kitty") || strings.Contains(string(source), "NewClient(") {
		t.Fatal("offline replay source imports or constructs a live client")
	}
}

func TestReplayRejectsExcessiveMembers(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for i := 0; i <= maxReplayMembers; i++ {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: "empty-" + strconv.Itoa(i), Mode: 0o600, Size: 0, Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	input := filepath.Join(t.TempDir(), "excessive-members.tar.gz")
	if err := os.WriteFile(input, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(t.Context(), input, ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "member-count limit") {
		t.Fatalf("excessive member error = %v", err)
	}
}
