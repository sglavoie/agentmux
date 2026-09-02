package debugger

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
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
		{"invalid payload", explanationRecord("daemon.decision", "quarantined", protocol.DecisionReasonInvalidPayload), CauseInvalidPayload},
		{"delivery failure", explanationRecord("hook.failure", "error", "daemon_unavailable"), CauseDeliveryFailure},
		{"duplicate", explanationRecord("daemon.decision", "duplicate", protocol.DecisionReasonRequestReplayed), CauseDuplicate},
		{"owner mismatch", explanationRecord("daemon.decision", "suppressed", protocol.DecisionReasonOwnerMismatch), CauseOwnerMismatch},
		{"quarantine", explanationRecord("daemon.decision", "quarantined", protocol.DecisionReasonUntrustedIdentityFallback), CauseQuarantine},
		{"reducer suppression", explanationRecord("daemon.decision", "suppressed", protocol.DecisionReasonGuardBlocked), CauseReducerSuppression},
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
	lifecycle := explanationRecord("daemon.process_lifecycle", "settled", state.HarnessProcessExitReasonInteractive)
	lifecycle.RequestID = ""
	lifecycle.Evidence = map[string]string{
		"event": protocol.EventHarnessProcessExited, "terminal_hook_evidence": "absent",
		"render_generation": "7", "process_observation": string(probes.HarnessProcessMissing),
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
