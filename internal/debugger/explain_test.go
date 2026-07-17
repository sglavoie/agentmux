package debugger

import (
	"context"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hooks"
)

func explanationRecord(stage, outcome, reason string) diagnostics.TraceEnvelope {
	record := diagnostics.NewEnvelope(stage)
	record.TraceID = "trace-1"
	record.RequestID = "request-1"
	record.WindowID = 42
	record.Harness = harness.Claude
	record.Outcome = outcome
	record.Reason = reason
	record.Evidence = map[string]string{"event": "PreToolUse", "source_event_id": "event-1"}
	return record
}

func TestExplanationClassifiesEveryInstrumentedBoundary(t *testing.T) {
	tests := []struct {
		name   string
		record diagnostics.TraceEnvelope
		want   string
	}{
		{"producer absence", explanationRecord("native.producer", "absent", "producer_not_invoked"), CauseProducerAbsence},
		{"outside kitty", explanationRecord("hook.kitty_detection", "rejected", "outside_kitty"), CauseOutsideKitty},
		{"invalid payload", explanationRecord("daemon.decision", "quarantined", daemon.DecisionReasonInvalidPayload), CauseInvalidPayload},
		{"delivery failure", explanationRecord("hook.failure", "error", "daemon_unavailable"), CauseDeliveryFailure},
		{"duplicate", explanationRecord("daemon.decision", "duplicate", daemon.DecisionReasonRequestReplayed), CauseDuplicate},
		{"owner mismatch", explanationRecord("daemon.decision", "suppressed", daemon.DecisionReasonOwnerMismatch), CauseOwnerMismatch},
		{"quarantine", explanationRecord("daemon.decision", "quarantined", daemon.DecisionReasonUntrustedIdentityFallback), CauseQuarantine},
		{"reducer suppression", explanationRecord("daemon.decision", "suppressed", daemon.DecisionReasonGuardBlocked), CauseReducerSuppression},
		{"render failure", explanationRecord("daemon.renderer.failed", "failed", "render_title"), CauseRenderFailure},
		{"notification dispatch failure", explanationRecord("daemon.notification.dispatch", "failed", "send_failed"), CauseNotificationFailure},
		{"notification backend timeout", explanationRecord("daemon.notification.dispatch", "failed", "backend_timeout"), CauseNotificationFailure},
		{"notification focus failure", explanationRecord("daemon.notification.focus", "failed", "focus_failed"), CauseNotificationFailure},
		{"notification suppression", explanationRecord("daemon.notification.decision", "suppressed", "config_disabled"), CauseNotificationSuppression},
		{"notification interaction timeout", explanationRecord("daemon.notification.focus", "suppressed", "interaction_timeout"), CauseNotificationSuppression},
		{"notification dismissal", explanationRecord("daemon.notification.focus", "suppressed", "dismissed"), CauseNotificationSuppression},
		{"notification daemon shutdown", explanationRecord("daemon.notification.dispatch", "cancelled", "daemon_shutdown"), CauseNotificationSuppression},
		{"success", explanationRecord("daemon.renderer.rendered", "success", "render_complete"), CauseSuccessfulCompletion},
	}
	saturated := explanationRecord("daemon.decision", "applied", "")
	saturated.Evidence["saturation"] = "ingress_queue"
	tests = append(tests, struct {
		name   string
		record diagnostics.TraceEnvelope
		want   string
	}{"saturation", saturated, CauseSaturation})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := buildExplanation(ExplainOptions{Recent: 10, Now: time.Now()}, []diagnostics.TraceEnvelope{test.record})
			if report.Cause.Classification != test.want || report.Cause.Certainty != CertaintyProven || len(report.Cause.EvidenceIDs) == 0 {
				t.Fatalf("cause = %+v, want %s with cited evidence", report.Cause, test.want)
			}
		})
	}
}

func TestExplanationClassifiesProcessExitRecoveryWithoutOverridingRendererFailures(t *testing.T) {
	lifecycle := explanationRecord("daemon.process_lifecycle", "settled", daemon.HarnessProcessExitReasonInteractive)
	lifecycle.RequestID = ""
	lifecycle.Evidence = map[string]string{
		"event": daemon.EventHarnessProcessExited, "terminal_hook_evidence": "absent",
		"render_generation": "7", "process_observation": string(daemon.HarnessProcessMissing),
	}
	report := buildExplanation(ExplainOptions{Selector: Selector{WindowID: 42}, Recent: 10, Now: time.Now()}, []diagnostics.TraceEnvelope{lifecycle})
	if report.Cause.Classification != CauseProcessLifecycleRecovery || report.Cause.Certainty != CertaintyProven ||
		!strings.Contains(report.Cause.Summary, "No terminal hook") || len(report.Cause.EvidenceIDs) != 1 {
		t.Fatalf("process lifecycle cause = %+v", report.Cause)
	}
	wantCommands := []string{
		"amx debug tail --stage daemon.process_lifecycle --window 42",
		"amx daemon export-events --window 42 --last 100",
	}
	if !reflect.DeepEqual(report.SuggestedCommands, wantCommands) {
		t.Fatalf("process lifecycle commands = %v, want %v", report.SuggestedCommands, wantCommands)
	}

	withTerminal := lifecycle
	withTerminal.Evidence = map[string]string{"terminal_hook_evidence": "observed"}
	nativeRendered := explanationRecord("daemon.renderer.rendered", "success", "render_complete")
	nativeRendered.RequestID = ""
	nativeRendered.Evidence = map[string]string{"generation": "7"}
	report = buildExplanation(ExplainOptions{Selector: Selector{WindowID: 42}, Recent: 10, Now: time.Now()}, []diagnostics.TraceEnvelope{withTerminal, nativeRendered})
	if report.Cause.Classification != CauseSuccessfulCompletion {
		t.Fatalf("native terminal completion was misclassified: %+v", report.Cause)
	}

	renderFailure := explanationRecord("daemon.renderer.failed", "failed", "render_title")
	renderFailure.RequestID = ""
	renderFailure.Evidence = map[string]string{"generation": "7"}
	report = buildExplanation(ExplainOptions{Selector: Selector{WindowID: 42}, Recent: 10, Now: time.Now()}, []diagnostics.TraceEnvelope{lifecycle, renderFailure})
	if report.Cause.Classification != CauseRenderFailure {
		t.Fatalf("renderer failure was hidden by lifecycle recovery: %+v", report.Cause)
	}

	inspectionFailure := explanationRecord("daemon.process_lifecycle", "degraded", "inspection_unknown")
	report = buildExplanation(ExplainOptions{Selector: Selector{WindowID: 42}, Recent: 10, Now: time.Now()}, []diagnostics.TraceEnvelope{inspectionFailure})
	if report.Cause.Classification != CauseInsufficientEvidence {
		t.Fatalf("inspection failure was misclassified as process recovery: %+v", report.Cause)
	}
}

func TestExplanationClassifiesDisabledAndUntrustedHookEvidence(t *testing.T) {
	for _, test := range []struct {
		name, code, want string
	}{
		{"disabled", "events_disabled", CauseHookDisabled},
		{"untrusted", "codex_hook_untrusted", CauseHookUntrusted},
	} {
		t.Run(test.name, func(t *testing.T) {
			hookReport := &HookReport{Profiles: []HookProfile{{
				Profile: "work", Harness: harness.Codex, Activation: hooks.ActivationFailure,
				Issues: []HookIssue{{Code: test.code, Message: "observed activation failure"}},
			}}}
			report := buildExplanation(ExplainOptions{
				Selector: Selector{Harness: harness.Codex, Profile: "work"}, Recent: 10, Hooks: hookReport, Now: time.Now(),
			}, nil)
			if report.Cause.Classification != test.want || len(report.Cause.EvidenceIDs) != 1 || len(report.Graph.Nodes) != 1 {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestExplanationDoesNotApplyAnotherEventsHookFailure(t *testing.T) {
	hookReport := &HookReport{Profiles: []HookProfile{{
		Profile: "work", Harness: harness.Codex, Activation: hooks.ActivationFailure,
		Issues: []HookIssue{{Code: "events_disabled", Message: "Agent Mux events disabled by profile: PostCompact"}},
	}}}
	report := buildExplanation(ExplainOptions{
		Selector: Selector{Harness: harness.Codex, Profile: "work", Event: "PreToolUse"}, Recent: 10, Hooks: hookReport, Now: time.Now(),
	}, nil)
	if report.Cause.Classification != CauseInsufficientEvidence {
		t.Fatalf("unrelated hook issue became cause: %+v", report.Cause)
	}
}

func TestExplanationRefusesToInferProducerAbsence(t *testing.T) {
	report := buildExplanation(ExplainOptions{Selector: Selector{RequestID: "missing"}, Recent: 10, Now: time.Now()}, nil)
	if report.Cause.Classification != CauseInsufficientEvidence || report.Cause.Certainty != CertaintyUncertain ||
		report.Cause.FirstMissingStage != "producer hook invocation" || !strings.Contains(report.Cause.Summary, "does not prove") {
		t.Fatalf("uncertainty = %+v", report.Cause)
	}
	accepted := explanationRecord("hook.delivery", "accepted", "accepted")
	decision := explanationRecord("daemon.decision", "applied", "")
	report = buildExplanation(ExplainOptions{Selector: Selector{RequestID: "request-1"}, Recent: 10, Now: time.Now()}, []diagnostics.TraceEnvelope{accepted, decision})
	if report.Cause.Classification != CauseInsufficientEvidence || report.Cause.FirstMissingStage != "terminal renderer evidence" {
		t.Fatalf("partial path = %+v", report.Cause)
	}
}

func TestExplainRecordsIsPureAndValidatesOptions(t *testing.T) {
	record := explanationRecord("daemon.renderer.rendered", "success", "render_complete")
	report, err := ExplainRecords(ExplainOptions{Selector: Selector{RequestID: "request-1"}}, []diagnostics.TraceEnvelope{record})
	if err != nil || report.Cause.Classification != CauseSuccessfulCompletion || report.JournalSnapshot != (diagnostics.SnapshotStatistics{}) {
		t.Fatalf("pure report = %+v, err = %v", report, err)
	}
	if report.SchemaVersion != 2 || report.Limits != (ExplanationLimits{
		GraphDepth: DefaultExplainGraphDepth, MaxNodes: DefaultExplainMaxNodes, MaxEdges: DefaultExplainMaxEdges,
	}) {
		t.Fatalf("defaults = schema %d limits %+v", report.SchemaVersion, report.Limits)
	}
	if _, err := ExplainRecords(ExplainOptions{Recent: -1}, nil); err == nil {
		t.Fatal("negative pure explain limit was accepted")
	}
	for _, options := range []ExplainOptions{
		{GraphDepth: MaxExplainGraphDepth + 1},
		{MaxNodes: MaxExplainMaxNodes + 1},
		{MaxEdges: MaxExplainMaxEdges + 1},
	} {
		if _, err := ExplainRecords(options, nil); err == nil {
			t.Fatalf("invalid graph options were accepted: %+v", options)
		}
	}
}

func TestExplanationTraversalDoesNotFanOutFromContext(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	record := func(offset int, traceID, requestID, generation string) diagnostics.TraceEnvelope {
		r := diagnostics.NewEnvelope("daemon.decision")
		r.ObservedAt = base.Add(time.Duration(offset) * time.Millisecond)
		r.TraceID = traceID
		r.RequestID = requestID
		r.WindowID = 42
		r.Evidence = map[string]string{}
		if generation != "" {
			r.Evidence["render_generation"] = generation
		}
		return r
	}
	seed := record(0, "trace-selected", "request-selected", "7")
	contextRecord := record(1, "trace-unrelated", "request-unrelated", "7")
	unrelatedHistory := record(2, "trace-unrelated", "request-unrelated-history", "")
	report := buildExplanation(ExplainOptions{
		Selector: Selector{RequestID: "request-selected"}, Recent: 10, Now: base,
	}, []diagnostics.TraceEnvelope{unrelatedHistory, contextRecord, seed})
	if len(report.Graph.Nodes) != 2 {
		t.Fatalf("contextual join recursively fanned out: %+v", report.Graph.Nodes)
	}
	for _, node := range report.Graph.Nodes {
		if node.Record != nil && node.Record.RequestID == "request-unrelated-history" {
			t.Fatalf("unrelated trace history was admitted through a contextual record: %+v", node)
		}
	}
}

func TestExplanationTraversalRetractsDepthPruningForContextuallyAdmittedRecord(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	record := func(offset int, stage, traceID, parentTraceID, requestID string) diagnostics.TraceEnvelope {
		r := diagnostics.NewEnvelope(stage)
		r.ObservedAt = base.Add(time.Duration(offset) * time.Millisecond)
		r.TraceID = traceID
		r.ParentTraceID = parentTraceID
		r.RequestID = requestID
		r.SessionID = "session-shared"
		r.Evidence = map[string]string{}
		return r
	}
	report := buildExplanation(ExplainOptions{
		Selector: Selector{RequestID: "request-root"}, Recent: 10, GraphDepth: 1, MaxNodes: 10, MaxEdges: 20, Now: base,
	}, []diagnostics.TraceEnvelope{
		record(0, "root", "trace-0", "", "request-root"),
		record(1, "middle", "trace-1", "trace-0", "request-middle"),
		record(2, "leaf", "trace-2", "trace-1", "request-leaf"),
	})

	if len(report.Graph.Nodes) != 3 || report.Truncation.Truncated || len(report.Truncation.Reasons) != 0 ||
		report.Truncation.PrunedNodeCandidates != 0 || report.Truncation.PrunedEdgeCandidates != 0 {
		t.Fatalf("contextually completed depth frontier = nodes %d truncation %+v", len(report.Graph.Nodes), report.Truncation)
	}
	foundLeafParent := false
	for _, edge := range report.Graph.Edges {
		if edge.Relation == "parent_trace_id" && edge.Value == "trace-1" {
			foundLeafParent = true
			break
		}
	}
	if !foundLeafParent {
		t.Fatalf("contextually admitted leaf is missing its parent trace edge: %+v", report.Graph.Edges)
	}
}

func TestExplanationTraversalReportsDepthBelowContextuallyAdmittedRecord(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	record := func(offset int, stage, traceID, parentTraceID, requestID, sessionID string) diagnostics.TraceEnvelope {
		r := diagnostics.NewEnvelope(stage)
		r.ObservedAt = base.Add(time.Duration(offset) * time.Millisecond)
		r.TraceID = traceID
		r.ParentTraceID = parentTraceID
		r.RequestID = requestID
		r.SessionID = sessionID
		r.Evidence = map[string]string{}
		return r
	}
	report := buildExplanation(ExplainOptions{
		Selector: Selector{RequestID: "request-root"}, Recent: 10, GraphDepth: 1, MaxNodes: 10, MaxEdges: 20, Now: base,
	}, []diagnostics.TraceEnvelope{
		record(0, "root", "trace-0", "", "request-root", "session-shared"),
		record(1, "first", "trace-1", "trace-0", "request-first", "session-shared"),
		record(2, "contextual", "trace-2", "trace-1", "request-contextual", "session-shared"),
		record(3, "excluded", "trace-3", "trace-2", "request-excluded", "session-isolated"),
	})

	if len(report.Graph.Nodes) != 3 || !report.Truncation.Truncated ||
		!containsString(report.Truncation.Reasons, TruncationGraphDepth) ||
		report.Truncation.PrunedNodeCandidates != 1 || report.Truncation.PrunedEdgeCandidates != 0 {
		t.Fatalf("depth below contextual admission = nodes %d truncation %+v", len(report.Graph.Nodes), report.Truncation)
	}
	for _, node := range report.Graph.Nodes {
		if node.Record != nil && node.Record.TraceID == "trace-3" {
			t.Fatalf("depth-pruned causal child was admitted: %+v", node)
		}
	}
}

func TestExplanationTraversalAppliesDepthNodeAndEdgeLimitsDeterministically(t *testing.T) {
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
