package debugger

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

type replayFixture struct {
	reducer   *daemon.Reducer
	journal   *diagnostics.Journal
	windowID  int
	daemonSeq uint64
	records   []diagnostics.TraceEnvelope
}

func newReplayFixture(t *testing.T) (*replayFixture, diagnostics.Paths) {
	t.Helper()
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &replayFixture{reducer: daemon.NewReducer(daemon.ReducerConfig{GuardDuration: 5}), journal: journal, windowID: 42}, paths
}

func (fixture *replayFixture) appendDecision(t *testing.T, traceID, requestID, event string, input daemon.ReducerEvent, fallback bool, renderGeneration uint64) diagnostics.TraceEnvelope {
	t.Helper()
	before := stateFromEffective(fixture.reducer.Reduce(input.At))
	decision := fixture.reducer.Apply(input)
	trace, _ := fixture.reducer.LatestTrace()
	reason := stableReplayReason(trace)
	after := stateFromEffective(fixture.reducer.Reduce(input.At))
	fixture.daemonSeq++
	record := diagnostics.NewEnvelope("daemon.decision")
	record.TraceID = traceID
	record.RequestID = requestID
	record.WindowID = fixture.windowID
	record.SessionID = input.SessionID
	record.RunID = input.RunID
	record.Outcome = string(decision)
	record.Reason = reason
	record.Evidence = replayFixtureEvidence(event, input, before, after, fixture.daemonSeq, reason, fallback, renderGeneration)
	if err := fixture.journal.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	fixture.records = append(fixture.records, record)
	return record
}

func replayFixtureEvidence(event string, input daemon.ReducerEvent, before, after ReplayState, daemonSeq uint64, reason string, fallback bool, renderGeneration uint64) map[string]string {
	sourceWindowID := input.OriginWindowID
	if sourceWindowID == 0 {
		sourceWindowID = 42
	}
	chosenRoute := "source"
	ownerMatched := false
	selectionReason := "source"
	if sourceWindowID != 42 {
		chosenRoute = "owner"
		ownerMatched = true
		selectionReason = "prompt_or_focus"
	}
	return map[string]string{
		"event": event, "action": input.Action, "reducer_action": input.Action,
		"source_event_id": "source-" + strconv.FormatUint(daemonSeq, 10),
		"source_kind":     string(input.Source.Kind), "source_key": input.Source.Key,
		"source_sequence": strconv.FormatUint(input.Seq, 10), "daemon_sequence": strconv.FormatUint(daemonSeq, 10),
		"render_generation": strconv.FormatUint(renderGeneration, 10), "turn_id": input.TurnID,
		"tool_use_id": input.ToolUse, "reducer_reason": reason,
		"used_fallback_identity": strconv.FormatBool(fallback), "normalization_outcome": "normalized",
		"replay.version": "1", "replay.emitted_at": strconv.FormatInt(input.At, 10),
		"replay.origin_window_id":   strconv.Itoa(input.OriginWindowID),
		"replay.is_user_submission": strconv.FormatBool(input.Prompt), "replay.pipe_mode": strconv.FormatBool(input.PipeMode),
		"replay.asl_phase": input.Phase, "replay.ends_session": strconv.FormatBool(input.EndsSession),
		"replay.subagent_delta": strconv.Itoa(input.SubagentDelta), "replay.guard_duration": "5",
		"replay.stale_active": "0", "replay.stale_subagent": "0", "replay.stale_compact": "0",
		"replay.before_icon": before.Icon, "replay.before_looping": strconv.FormatBool(before.Looping),
		"replay.before_source_kind": before.SourceKind, "replay.before_source_key": before.SourceKey,
		"replay.after_icon": after.Icon, "replay.after_looping": strconv.FormatBool(after.Looping),
		"replay.after_source_kind": after.SourceKind, "replay.after_source_key": after.SourceKey,
		"route.snapshot_ready": "true", "route.lookup_outcome": "source_found",
		"route.source_window_id": strconv.Itoa(sourceWindowID), "route.target_window_id": "42",
		"route.source_matched": "true", "route.owner_matched": strconv.FormatBool(ownerMatched),
		"route.selection_reason": selectionReason, "route.chosen": chosenRoute,
	}
}

func (fixture *replayFixture) appendRequestReplay(t *testing.T, original diagnostics.TraceEnvelope) {
	t.Helper()
	evidence := make(map[string]string, len(original.Evidence))
	for key, value := range original.Evidence {
		evidence[key] = value
	}
	fixture.daemonSeq++
	evidence["daemon_sequence"] = strconv.FormatUint(fixture.daemonSeq, 10)
	evidence["reducer_reason"] = daemon.DecisionReasonRequestReplayed
	current := stateFromEffective(fixture.reducer.Reduce(102))
	evidence["replay.before_icon"] = current.Icon
	evidence["replay.before_looping"] = strconv.FormatBool(current.Looping)
	evidence["replay.before_source_kind"] = current.SourceKind
	evidence["replay.before_source_key"] = current.SourceKey
	evidence["replay.after_icon"] = current.Icon
	evidence["replay.after_looping"] = strconv.FormatBool(current.Looping)
	evidence["replay.after_source_kind"] = current.SourceKind
	evidence["replay.after_source_key"] = current.SourceKey
	record := diagnostics.NewEnvelope("daemon.decision")
	record.TraceID = "trace-request-replay"
	record.RequestID = original.RequestID
	record.WindowID = fixture.windowID
	record.Outcome = string(daemon.DecisionDuplicate)
	record.Reason = daemon.DecisionReasonRequestReplayed
	record.Evidence = evidence
	if err := fixture.journal.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	fixture.records = append(fixture.records, record)
}

func (fixture *replayFixture) appendRender(t *testing.T, generation uint64, sources []uint64, decisions ...diagnostics.TraceEnvelope) {
	t.Helper()
	traceIDs := make([]string, 0, len(decisions))
	requestIDs := make([]string, 0, len(decisions))
	sourceIDs := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		traceIDs = append(traceIDs, decision.TraceID)
		requestIDs = append(requestIDs, decision.RequestID)
		sourceIDs = append(sourceIDs, decision.Evidence["source_event_id"])
	}
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		parts = append(parts, strconv.FormatUint(source, 10))
	}
	record := diagnostics.NewEnvelope("daemon.renderer.rendered")
	record.TraceID = traceIDs[0]
	record.RequestID = requestIDs[0]
	record.WindowID = fixture.windowID
	record.Outcome = "success"
	record.Reason = "render_complete"
	record.Evidence = map[string]string{
		"generation": strconv.FormatUint(generation, 10), "source_generations": strings.Join(parts, ","),
		"trace_ids": strings.Join(traceIDs, ","), "request_ids": strings.Join(requestIDs, ","),
		"source_event_ids": strings.Join(sourceIDs, ","),
	}
	if err := fixture.journal.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}

func TestReplayGoldenJournalCoversOrderingRestartsFallbackASLAndCoalescing(t *testing.T) {
	fixture, paths := newReplayFixture(t)
	legacy := fixture.appendDecision(t, "trace-fallback", "request-fallback", "PreToolUse", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceLegacy, Key: "window:42"}, Action: "active", Seq: 1, At: 100, OriginWindowID: 42,
	}, true, 10)
	harness := fixture.appendDecision(t, "trace-new", "request-new", "UserPromptSubmit", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceHarness, Key: "claude:session-1"}, Action: "active", Seq: 2,
		At: 101, OriginWindowID: 43, Prompt: true, TurnID: "turn-1", SessionID: "session-1",
	}, false, 11)
	fixture.appendDecision(t, "trace-reordered", "request-reordered", "Stop", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceHarness, Key: "claude:session-1"}, Action: "stop", Seq: 1,
		At: 102, OriginWindowID: 42, TurnID: "turn-1", SessionID: "session-1",
	}, false, 0)
	fixture.appendRequestReplay(t, harness)
	fixture.daemonSeq = 0 // a new daemon restored the same durable reducer facts
	fixture.appendDecision(t, "trace-asl-turn", "request-asl-turn", "AgentSessionLoopTurnStarted", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceASL, Key: "run-1"}, Seq: 1, At: 103,
		OriginWindowID: 42, RunID: "run-1", Phase: daemon.ASLPhaseTurn,
	}, false, 0)
	fixture.appendDecision(t, "trace-asl-finish", "request-asl-finish", "AgentSessionLoopFinished", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceASL, Key: "run-1"}, Seq: 2, At: 104,
		OriginWindowID: 42, RunID: "run-1", Phase: daemon.ASLPhaseFinished,
	}, false, 0)
	fixture.appendDecision(t, "trace-reset", "request-reset", "SessionEnd", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceHarness, Key: "claude:session-1"}, Action: "user_clear", Seq: 3,
		At: 105, OriginWindowID: 42, SessionID: "session-1", EndsSession: true,
	}, false, 0)
	fixture.appendRender(t, 11, []uint64{10, 11}, legacy, harness)

	report, err := Replay(t.Context(), paths.JournalDir, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || !report.Complete || report.Summary.Mismatched != 0 || report.Summary.Incomplete != 0 {
		t.Fatalf("replay report = %+v", report)
	}
	if report.Summary.DaemonRestarts != 1 || len(report.Renders) != 1 || report.Renders[0].Status != "matched" {
		t.Fatalf("restart/render report = %+v", report)
	}
	foundFallback, foundReordered, foundRequestReplay, foundASL := false, false, false, false
	for _, comparison := range report.Comparisons {
		foundFallback = foundFallback || comparison.UsedFallback && comparison.Status == "matched"
		foundReordered = foundReordered || comparison.RecomputedDecision == daemon.DecisionReordered && comparison.Status == "matched"
		foundRequestReplay = foundRequestReplay || comparison.RecomputedReason == daemon.DecisionReasonRequestReplayed && comparison.Status == "matched"
		foundASL = foundASL || comparison.Event == "AgentSessionLoopTurnStarted" && comparison.RecomputedAfter.Looping
	}
	if !foundFallback || !foundReordered || !foundRequestReplay || !foundASL {
		t.Fatalf("missing golden comparison: fallback=%t reordered=%t request-replay=%t asl=%t", foundFallback, foundReordered, foundRequestReplay, foundASL)
	}
	for _, route := range report.Routes {
		if route.TraceID == "trace-request-replay" {
			t.Fatalf("IPC request replay was treated as a fresh route decision: %+v", route)
		}
	}
}

func TestReplayShareBundleIsPortableAndPreservesReplaySafeEvidence(t *testing.T) {
	fixture, paths := newReplayFixture(t)
	fixture.appendDecision(t, "trace-portable", "request-portable", "UserPromptSubmit", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceHarness, Key: "claude:portable"}, Action: "active", Seq: 1,
		At: 200, OriginWindowID: 42, Prompt: true, TurnID: "turn-portable", SessionID: "portable",
	}, false, 0)
	output := filepath.Join(t.TempDir(), "share.tar.gz")
	if _, err := CreateBundle(t.Context(), BundleOptions{
		Paths: paths, OutputPath: output, Mode: BundleModeShare, Journal: fixture.journal,
		Now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), Build: buildinfo.Info{Version: "test"},
	}); err != nil {
		t.Fatal(err)
	}
	report, err := Replay(t.Context(), output, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Input.Format != "bundle" || report.Input.BundleMode != BundleModeShare || !report.Success || !report.Complete {
		t.Fatalf("portable replay report = %+v", report)
	}
	encoded, _ := json.Marshal(report)
	for _, rawID := range []string{"trace-portable", "request-portable", "claude:portable", "turn-portable"} {
		if strings.Contains(string(encoded), rawID) {
			t.Fatalf("portable replay exposed unhashed identifier %q: %s", rawID, encoded)
		}
	}
	legacy := filepath.Join(t.TempDir(), "share-v1.tar.gz")
	rewriteBundleAsV1(t, output, legacy)
	legacyReport, err := Replay(t.Context(), legacy, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if legacyReport.Input.Format != "bundle" || legacyReport.Input.SchemaVersion != 1 ||
		!legacyReport.Success || !legacyReport.Complete {
		t.Fatalf("legacy bundle replay report = %+v", legacyReport)
	}
}

func TestReplayASLRecordsExtractedFromV2Bundle(t *testing.T) {
	fixture, paths := newReplayFixture(t)
	fixture.appendDecision(t, "trace-asl-standalone", "request-asl-standalone", "AgentSessionLoopTurnStarted", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceASL, Key: "run-standalone"}, Seq: 1, At: 200,
		OriginWindowID: 42, RunID: "run-standalone", Phase: daemon.ASLPhaseTurn,
	}, false, 0)
	fixture.appendDecision(t, "trace-unrelated", "request-unrelated", "UserPromptSubmit", daemon.ReducerEvent{
		Source: daemon.SourceID{Kind: daemon.SourceHarness, Key: "claude:unrelated"}, Action: "active", Seq: 1,
		At: 201, OriginWindowID: 42, Prompt: true, TurnID: "turn-unrelated", SessionID: "unrelated",
	}, false, 0)
	bundlePath := filepath.Join(t.TempDir(), "share.tar.gz")
	if _, err := CreateBundle(t.Context(), BundleOptions{
		Paths: paths, OutputPath: bundlePath, Mode: BundleModeShare, Journal: fixture.journal,
		Now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), Build: buildinfo.Info{Version: "test"},
	}); err != nil {
		t.Fatal(err)
	}

	members := readBundleMembers(t, bundlePath)
	aslRecords := members["asl-records.json"]
	if !bytes.Contains(aslRecords, []byte(`"journal_snapshot"`)) {
		t.Fatalf("v2 ASL records omitted snapshot metadata: %s", aslRecords)
	}
	var aslWrapper struct {
		JournalSnapshotScope string                         `json:"journal_snapshot_scope"`
		JournalSnapshot      diagnostics.SnapshotStatistics `json:"journal_snapshot"`
		Records              []json.RawMessage              `json:"records"`
	}
	if err := json.Unmarshal(aslRecords, &aslWrapper); err != nil {
		t.Fatalf("decode v2 ASL records: %v\n%s", err, aslRecords)
	}
	if aslWrapper.JournalSnapshotScope != "source_snapshot" ||
		aslWrapper.JournalSnapshot.RecordsMatched != 2 || len(aslWrapper.Records) != 1 {
		t.Fatalf("v2 ASL snapshot scope = %q snapshot %+v records %d", aslWrapper.JournalSnapshotScope, aslWrapper.JournalSnapshot, len(aslWrapper.Records))
	}
	bundleReport, err := Replay(t.Context(), bundlePath, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if bundleReport.Input.Format != "bundle" || !bundleReport.Success || !bundleReport.Complete {
		t.Fatalf("full bundle replay report = %+v", bundleReport)
	}
	standalonePath := filepath.Join(t.TempDir(), "asl-records.json")
	if err := os.WriteFile(standalonePath, aslRecords, 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Replay(t.Context(), standalonePath, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Input.Format != "causal-records" || report.Input.SchemaVersion != BundleSchemaVersion ||
		report.Input.Records != 1 || !report.Success || !report.Complete {
		t.Fatalf("standalone ASL replay report = %+v", report)
	}
}

func rewriteBundleAsV1(t *testing.T, source, output string) {
	t.Helper()
	members := readBundleMembers(t, source)
	var causal struct {
		SchemaVersion   int                            `json:"schema_version"`
		Selector        json.RawMessage                `json:"selector"`
		JournalSnapshot diagnostics.SnapshotStatistics `json:"journal_snapshot"`
		Records         []json.RawMessage              `json:"records"`
	}
	if err := json.Unmarshal(members["causal-records.json"], &causal); err != nil {
		t.Fatal(err)
	}
	legacyCausal := struct {
		SchemaVersion int               `json:"schema_version"`
		Selector      json.RawMessage   `json:"selector"`
		CorruptLines  int               `json:"corrupt_lines"`
		Records       []json.RawMessage `json:"records"`
	}{
		SchemaVersion: 1, Selector: causal.Selector,
		CorruptLines: causal.JournalSnapshot.CorruptLines, Records: causal.Records,
	}
	causalData, err := json.MarshalIndent(legacyCausal, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	causalData = append(causalData, '\n')
	members["causal-records.json"] = causalData

	var manifest BundleManifest
	if err := json.Unmarshal(members["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.SchemaVersion = 1
	for index := range manifest.Entries {
		if manifest.Entries[index].Member == "causal-records.json" {
			manifest.Entries[index].Bytes = len(causalData)
		}
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	members["manifest.json"] = append(manifestData, '\n')

	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	bundleMembers := make([]bundleMember, 0, len(names))
	for _, name := range names {
		bundleMembers = append(bundleMembers, bundleMember{name: name, data: members[name]})
	}
	if err := writeBundleAtomic(output, time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), bundleMembers); err != nil {
		t.Fatal(err)
	}
}

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
