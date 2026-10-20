package debugger

import (
	"context"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestExplanationTraversalAppliesDepthNodeAndEdgeLimitsDeterministically(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	root := diagnostics.NewEnvelope("root")
	root.ObservedAt = base
	root.TraceID = "trace-0"
	root.RequestID = "request-root"
	root.Evidence = map[string]string{}
	chain := []diagnostics.TraceEnvelope{root}
	for i := 1; i <= 3; i++ {
		record := diagnostics.NewEnvelope("child")
		record.ObservedAt = base.Add(time.Duration(i) * time.Millisecond)
		record.TraceID = "trace-" + string(rune('0'+i))
		record.ParentTraceID = "trace-" + string(rune('0'+i-1))
		record.Evidence = map[string]string{}
		chain = append(chain, record)
	}
	depthReport := buildExplanation(ExplainOptions{
		Selector: Selector{RequestID: "request-root"}, Recent: 10, GraphDepth: 2, MaxNodes: 10, MaxEdges: 20, Now: base,
	}, chain)
	if len(depthReport.Graph.Nodes) != 3 || !depthReport.Truncation.Truncated ||
		!containsString(depthReport.Truncation.Reasons, TruncationGraphDepth) || depthReport.Truncation.PrunedNodeCandidates != 1 {
		t.Fatalf("depth-limited report = %+v", depthReport)
	}

	highFanout := make([]diagnostics.TraceEnvelope, 0, 401)
	seed := diagnostics.NewEnvelope("seed")
	seed.ObservedAt = base
	seed.TraceID = "fanout-seed"
	seed.RequestID = "fanout-request"
	seed.WindowID = 42
	seed.Evidence = map[string]string{"render_generation": "9"}
	highFanout = append(highFanout, seed)
	for i := 0; i < 400; i++ {
		record := diagnostics.NewEnvelope("context")
		record.ObservedAt = base.Add(time.Duration(i+1) * time.Microsecond)
		record.TraceID = "context-" + strconv.Itoa(i)
		record.RequestID = "context-request-" + strconv.Itoa(i)
		record.WindowID = 42
		record.Evidence = map[string]string{"render_generation": "9"}
		highFanout = append(highFanout, record)
	}
	options := ExplainOptions{Selector: Selector{RequestID: "fanout-request"}, Recent: 10, Now: base}
	first := buildExplanation(options, highFanout)
	for left, right := 0, len(highFanout)-1; left < right; left, right = left+1, right-1 {
		highFanout[left], highFanout[right] = highFanout[right], highFanout[left]
	}
	second := buildExplanation(options, highFanout)
	if len(first.Graph.Nodes) != DefaultExplainMaxNodes || len(first.Graph.Edges) > DefaultExplainMaxEdges ||
		first.Truncation.PrunedNodeCandidates != 151 || !containsString(first.Truncation.Reasons, TruncationMaxNodes) {
		t.Fatalf("default high-fan-out bounds = nodes %d edges %d truncation %+v", len(first.Graph.Nodes), len(first.Graph.Edges), first.Truncation)
	}
	if !reflect.DeepEqual(first.Graph, second.Graph) || !reflect.DeepEqual(first.Truncation, second.Truncation) {
		t.Fatal("bounded traversal changed when input order was reversed")
	}
	for left, right := 0, len(highFanout)-1; left < right; left, right = left+1, right-1 {
		highFanout[left], highFanout[right] = highFanout[right], highFanout[left]
	}

	edgeReport := buildExplanation(ExplainOptions{
		Selector: Selector{RequestID: "fanout-request"}, Recent: 10, GraphDepth: 1, MaxNodes: 10, MaxEdges: 2, Now: base,
	}, highFanout[:5])
	if len(edgeReport.Graph.Edges) != 2 || edgeReport.Truncation.PrunedEdgeCandidates == 0 ||
		!containsString(edgeReport.Truncation.Reasons, TruncationMaxEdges) {
		t.Fatalf("edge-limited report = edges %d truncation %+v", len(edgeReport.Graph.Edges), edgeReport.Truncation)
	}
}

func TestExplanationGraphJoinsAllStableIdentities(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	hookRecord := explanationRecord("hook.delivery", "accepted", "accepted")
	hookRecord.ObservedAt = base
	decision := explanationRecord("daemon.decision", "applied", "")
	decision.ObservedAt = base.Add(time.Millisecond)
	decision.SessionID = "session-1"
	decision.RunID = "run-1"
	decision.Evidence = map[string]string{
		"event": "PreToolUse", "source_event_id": "event-1", "source_kind": "harness",
		"source_key": "claude:session-1", "source_sequence": "2", "daemon_sequence": "9",
		"render_generation": "3", "turn_id": "turn-1", "tool_use_id": "tool-1",
	}
	rendered := explanationRecord("daemon.renderer.rendered", "success", "render_complete")
	rendered.ObservedAt = base.Add(2 * time.Millisecond)
	rendered.Evidence = map[string]string{"generation": "3", "source_event_ids": "event-1,event-2"}
	completion := explanationRecord("daemon.decision", "applied", "")
	completion.ObservedAt = base.Add(2500 * time.Microsecond)
	completion.TraceID = "trace-completion"
	completion.RequestID = "request-2"
	completion.SessionID = "session-1"
	completion.RunID = "run-1"
	completion.Evidence = map[string]string{
		"event": "PostToolUse", "source_event_id": "event-2", "source_kind": "harness",
		"source_key": "claude:session-1", "source_sequence": "3", "daemon_sequence": "10",
		"turn_id": "turn-1", "tool_use_id": "tool-1",
	}
	child := explanationRecord("daemon.probe.completed", "success", "marker_promoted")
	child.ObservedAt = base.Add(3 * time.Millisecond)
	child.TraceID = "trace-child"
	child.RequestID = ""
	child.ParentTraceID = "trace-1"
	launch := explanationRecord("session.launch", "success", "launched")
	launch.ObservedAt = base.Add(-time.Second)
	launch.TraceID = "launch-trace"
	launch.RequestID = ""
	launch.SessionID = "session-1"
	launch.RunID = "run-1"
	launch.LaunchID = "11111111-1111-4111-8111-111111111111"
	launch.Evidence = nil

	report := buildExplanation(ExplainOptions{Selector: Selector{RequestID: "request-1"}, Recent: 10, Now: base},
		[]diagnostics.TraceEnvelope{hookRecord, decision, rendered, completion, child, launch})
	if len(report.Graph.Nodes) != 6 {
		t.Fatalf("nodes = %d, want 6: %+v", len(report.Graph.Nodes), report.Graph.Nodes)
	}
	wantRelations := map[string]bool{
		"trace_id": false, "request_id": false, "source_event_id": false, "render_generation": false,
		"parent_trace_id": false, "session_id": false, "asl_run_id": false, "turn_id": false, "tool_use_id": false,
	}
	for _, edge := range report.Graph.Edges {
		if _, ok := wantRelations[edge.Relation]; ok {
			wantRelations[edge.Relation] = true
		}
	}
	for relation, found := range wantRelations {
		if !found {
			t.Errorf("missing %s edge: %+v", relation, report.Graph.Edges)
		}
	}
	foundDecision, foundLaunch := false, false
	for _, node := range report.Graph.Nodes {
		if node.Identity.DaemonSequence == 9 && node.Identity.NormalizedSourceID == "harness:claude:session-1" && node.Identity.ToolUseID == "tool-1" {
			foundDecision = true
		}
		if node.Identity.LaunchID == launch.LaunchID {
			foundLaunch = true
		}
	}
	if !foundDecision || !foundLaunch {
		t.Fatalf("identity projections incomplete: %+v", report.Graph.Nodes)
	}
}

func TestExpectedSequenceDiagnosticsUseStableScopesAndPreserveUncertainty(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	makeEvent := func(offset int, event, sourceEvent string, evidence map[string]string) diagnostics.TraceEnvelope {
		record := explanationRecord("daemon.decision", "applied", "")
		record.ObservedAt = base.Add(time.Duration(offset) * time.Millisecond)
		record.TraceID = "trace-" + sourceEvent
		record.SessionID = "session-1"
		record.Evidence = map[string]string{"event": event, "source_event_id": sourceEvent}
		for key, value := range evidence {
			record.Evidence[key] = value
		}
		return record
	}
	records := []diagnostics.TraceEnvelope{
		makeEvent(1, "SessionStart", "e1", nil), makeEvent(12, "SessionEnd", "e12", nil),
		makeEvent(2, "UserPromptSubmit", "e2", map[string]string{"turn_id": "turn-1"}),
		makeEvent(11, "Stop", "e11", map[string]string{"turn_id": "turn-1"}),
		makeEvent(3, "PreToolUse", "e3", map[string]string{"tool_use_id": "tool-1"}),
		makeEvent(4, "PostToolUse", "e4", map[string]string{"tool_use_id": "tool-1"}),
		makeEvent(5, "PreCompact", "e5", map[string]string{"turn_id": "turn-1"}),
		makeEvent(6, "PostCompactAuto", "e6", map[string]string{"turn_id": "turn-1"}),
		makeEvent(7, "SubagentStart", "e7", map[string]string{"source_kind": "subagent", "source_key": "agent-1"}),
		makeEvent(8, "SubagentStop", "e8", map[string]string{"source_kind": "subagent", "source_key": "agent-1"}),
		makeEvent(9, "PreToolUse", "e9", map[string]string{"tool_use_id": "tool-missing"}),
	}
	report := buildExplanation(ExplainOptions{Selector: Selector{SessionID: "session-1"}, Recent: 20, Now: base}, records)
	complete := map[string]bool{}
	uncertainTool := false
	for _, sequence := range report.ExpectedSequences {
		if sequence.Status == "complete" {
			complete[sequence.Kind] = true
		}
		if sequence.Kind == "tool" && sequence.Status == CertaintyUncertain && strings.Contains(sequence.Scope, "tool-missing") {
			uncertainTool = len(sequence.MissingSteps) == 1 && strings.Contains(sequence.Message, "does not prove")
		}
	}
	for _, kind := range []string{"session", "prompt_stop", "tool", "compaction", "subagent"} {
		if !complete[kind] {
			t.Errorf("missing complete %s sequence: %+v", kind, report.ExpectedSequences)
		}
	}
	if !uncertainTool {
		t.Fatalf("missing uncertain tool diagnostic: %+v", report.ExpectedSequences)
	}
}

func TestExplainReadsSourceEventGraphOffline(t *testing.T) {
	t.Parallel()

	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	decision := explanationRecord("daemon.decision", "applied", "")
	decision.Evidence["source_event_id"] = "source-selected"
	rendered := explanationRecord("daemon.renderer.rendered", "success", "render_complete")
	rendered.Evidence = map[string]string{"source_event_ids": "source-selected", "generation": "1"}
	for _, record := range []diagnostics.TraceEnvelope{decision, rendered} {
		if err := journal.Append(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Explain(context.Background(), journal, ExplainOptions{Selector: Selector{SourceEventID: "source-selected"}, Recent: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Cause.Classification != CauseSuccessfulCompletion || len(report.Graph.Nodes) != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestExplainSampledJournalStillReachesTerminalCause(t *testing.T) {
	t.Parallel()

	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A standard-detail (sampled) healthy event: intermediate hook success
	// stages are absent, only the anchors survive, and the renderer stage is
	// one coalesced record whose primary IDs belong to a different event.
	delivery := explanationRecord("hook.delivery", "accepted", "accepted")
	delivery.Evidence["retryable"] = "false"
	decision := explanationRecord("daemon.decision", "applied", "reducer_applied")
	rendered := explanationRecord("daemon.renderer.rendered", "success", "render_complete")
	rendered.TraceID = "trace-other"
	rendered.RequestID = "request-other"
	rendered.Evidence = map[string]string{
		"generation":       "3",
		"trace_ids":        "trace-other,trace-1",
		"request_ids":      "request-other,request-1",
		"source_event_ids": "event-other,event-1",
	}
	for _, record := range []diagnostics.TraceEnvelope{delivery, decision, rendered} {
		if err := journal.Append(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Explain(context.Background(), journal, ExplainOptions{Selector: Selector{TraceID: "trace-1"}, Recent: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Cause.Classification != CauseSuccessfulCompletion {
		t.Fatalf("sampled-fixture cause = %+v, want successful completion via coalesced renderer evidence", report.Cause)
	}
	if len(report.Graph.Nodes) != 3 {
		t.Fatalf("sampled-fixture nodes = %+v, want delivery, decision, and coalesced render", report.Graph.Nodes)
	}
}

func TestExplainCorrelatesASLSourceDeliveryDaemonAndRenderEvidence(t *testing.T) {
	t.Parallel()

	delivery := explanationRecord("hook.delivery", "accepted", "accepted")
	delivery.TraceID = "trace-asl-delivery"
	delivery.RequestID = "df487220-3ef5-5d50-b7a4-d1b79756f8a5"
	delivery.RunID = "asl-run-1"
	delivery.Evidence = map[string]string{
		"event":     "AgentSessionLoopTurnStarted",
		"retryable": "false",
	}

	decision := explanationRecord("daemon.decision", "applied", "reducer_applied")
	decision.TraceID = delivery.TraceID
	decision.RequestID = delivery.RequestID
	decision.RunID = delivery.RunID
	decision.Evidence = map[string]string{
		"event":           "AgentSessionLoopTurnStarted",
		"source_event_id": "asl-run-1:2",
		"source_kind":     "asl",
		"source_key":      "asl-run-1",
		"source_sequence": "2",
	}

	rendered := explanationRecord("daemon.renderer.rendered", "success", "render_complete")
	rendered.TraceID = "trace-render"
	rendered.RequestID = ""
	rendered.RunID = delivery.RunID
	rendered.Evidence = map[string]string{
		"generation":       "7",
		"source_event_ids": "asl-run-1:2",
	}

	report := buildExplanation(
		ExplainOptions{
			Selector: Selector{SourceEventID: "asl-run-1:2"},
			Recent:   10,
			Now:      time.Now(),
		},
		[]diagnostics.TraceEnvelope{delivery, decision, rendered},
	)
	if report.Cause.Classification != CauseSuccessfulCompletion {
		t.Fatalf("cause = %+v, want successful ASL delivery path", report.Cause)
	}
	if len(report.Graph.Nodes) != 3 {
		t.Fatalf("nodes = %+v, want delivery, daemon decision, and render", report.Graph.Nodes)
	}
	relations := map[string]bool{}
	for _, edge := range report.Graph.Edges {
		relations[edge.Relation] = true
	}
	if !relations["trace_id"] || !relations["request_id"] || !relations["source_event_id"] {
		t.Fatalf("correlation edges = %+v", report.Graph.Edges)
	}
}
