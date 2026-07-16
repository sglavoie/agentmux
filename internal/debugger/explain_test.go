package debugger

import (
	"context"
	"path/filepath"
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
		{"notification suppression", explanationRecord("daemon.notification.decision", "suppressed", "config_disabled"), CauseNotificationSuppression},
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
	if err != nil || report.Cause.Classification != CauseSuccessfulCompletion || report.Journal != (ExplanationJournal{}) {
		t.Fatalf("pure report = %+v, err = %v", report, err)
	}
	if _, err := ExplainRecords(ExplainOptions{Recent: -1}, nil); err == nil {
		t.Fatal("negative pure explain limit was accepted")
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
