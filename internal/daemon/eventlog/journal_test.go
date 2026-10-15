package eventlog

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestDecisionEnvelopeCarriesTheIdentityHeaderAndReplayInputs(t *testing.T) {
	before := protocol.EffectiveStateEvidence{Icon: "bolt", SourceKind: "harness", SourceKey: "claude:s1"}
	after := protocol.EffectiveStateEvidence{Icon: "bell", Looping: true, SourceKind: "harness", SourceKey: "claude:s1"}
	event := protocol.SessionEvent{
		Event: "PreToolUse", Action: "active", TraceID: "trace-1", RequestID: "request-1",
		ProducerHarness: "claude", Profile: "work", SessionID: "s1", RunID: "run-1",
		TargetWindowID: 7, DaemonSeq: 3, SourceSeq: 9, Attempt: 2,
		Origin:          &protocol.HookOrigin{RuntimeSessionID: "runtime-1"},
		ReducerDecision: protocol.DecisionApplied, DecisionReason: protocol.DecisionReasonAccepted,
		EffectiveBefore: &before, EffectiveAfter: &after,
		ReducerInput: &protocol.ReducerInputEvidence{EmittedAt: 11, OriginWindowID: 7, GuardDuration: 1},
	}

	envelope := DecisionEnvelope(event)

	if envelope.Stage != decisionRecord {
		t.Fatalf("stage = %q; want %q", envelope.Stage, decisionRecord)
	}
	if envelope.TraceID != "trace-1" || envelope.RequestID != "request-1" {
		t.Fatalf("identity = %q/%q; want trace-1/request-1", envelope.TraceID, envelope.RequestID)
	}
	if envelope.WindowID != 7 || envelope.SessionID != "s1" || envelope.RunID != "run-1" {
		t.Fatalf("envelope routing identity = %+v", envelope)
	}
	if envelope.Outcome != string(protocol.DecisionApplied) || envelope.Reason != protocol.DecisionReasonAccepted {
		t.Fatalf("outcome/reason = %q/%q", envelope.Outcome, envelope.Reason)
	}

	want := map[string]string{
		"event":                    "PreToolUse",
		"runtime_session_id":       "runtime-1",
		"action":                   "active",
		"source_sequence":          "9",
		"daemon_sequence":          "3",
		"attempt":                  "2",
		"replay.version":           "1",
		"replay.before_icon":       "bolt",
		"replay.after_icon":        "bell",
		"replay.after_looping":     "true",
		"replay.before_source_key": "claude:s1",
		"replay.emitted_at":        "11",
		"replay.guard_duration":    "1",
	}
	for key, value := range want {
		if got := envelope.Evidence[key]; got != value {
			t.Fatalf("evidence[%q] = %q; want %q", key, got, value)
		}
	}
	if got := envelope.Evidence["effective_after"]; got == "" {
		t.Fatal("effective_after must carry the serialized evidence")
	}
	if _, ok := envelope.Evidence["route.chosen"]; ok {
		t.Fatal("a row without routing evidence must not synthesize route keys")
	}
}

func TestDecisionEnvelopeOmitsAbsentOptionalEvidence(t *testing.T) {
	envelope := DecisionEnvelope(protocol.SessionEvent{Event: "Stop"})

	if _, ok := envelope.Evidence["replay.emitted_at"]; ok {
		t.Fatal("a row without reducer input must not synthesize replay inputs")
	}
	if _, ok := envelope.Evidence["replay.before_icon"]; ok {
		t.Fatal("a row without before-state evidence must not synthesize one")
	}
	if _, ok := envelope.Evidence["attempt"]; ok {
		t.Fatal("a first attempt must not be recorded")
	}
	if got := envelope.Evidence["replay.version"]; got != "1" {
		t.Fatalf("replay.version = %q; want 1 on every record", got)
	}
	if got := envelope.Evidence["effective_before"]; got != "" {
		t.Fatalf("effective_before = %q; want empty when absent", got)
	}
}

func TestDecisionEnvelopeRecordsRoutingCandidatesAndAlternatives(t *testing.T) {
	event := protocol.SessionEvent{
		Event: "Stop",
		RouteEvidence: &protocol.RoutingEvidence{
			SnapshotReady: true, SourceWindowID: 3, TargetWindowID: 7,
			ChosenRoute: "source", SelectionReason: "direct", ConsideredWindowCount: 2,
			ConsideredWindows:    []protocol.RoutingWindowEvidence{{WindowID: 3}, {WindowID: 7}},
			RejectedAlternatives: []protocol.RejectedRouteEvidence{{WindowID: 9, Reason: "owner_mismatch"}},
		},
	}

	evidence := DecisionEnvelope(event).Evidence

	if evidence["route.chosen"] != "source" || evidence["route.target_window_id"] != "7" {
		t.Fatalf("route evidence = %v", evidence)
	}
	if evidence["route.considered_count"] != "2" {
		t.Fatalf("route.considered_count = %q; want 2", evidence["route.considered_count"])
	}
	if evidence["route.candidate.00"] == "" || evidence["route.candidate.01"] == "" {
		t.Fatalf("considered candidates must be indexed and serialized: %v", evidence)
	}
	if evidence["route.rejected.00"] == "" {
		t.Fatalf("rejected alternatives must be indexed and serialized: %v", evidence)
	}
}

func TestStableReducerReasonPinsThePersistedVocabulary(t *testing.T) {
	cases := []struct {
		reason string
		want   string
	}{
		{"duplicate sequence", protocol.DecisionReasonDuplicateSequence},
		{"older sequence arrived after a newer one", protocol.DecisionReasonReorderedSequence},
		{"idle preserves attention/terminal icon", protocol.DecisionReasonIdlePreservesTerminal},
		{"same identified turn after terminal", protocol.DecisionReasonTerminalTurnSuppression},
		{"stopped subagent never restarts", protocol.DecisionReasonTerminalTurnSuppression},
		{"same-source same-tool terminal suppression", protocol.DecisionReasonTerminalTurnSuppression},
		{"same-source same-turn terminal suppression", protocol.DecisionReasonTerminalTurnSuppression},
		{"legacy terminal grace window", protocol.DecisionReasonTerminalGraceSuppression},
		{"terminal trailing-event grace", protocol.DecisionReasonTerminalGraceSuppression},
		{"user acknowledged visible facts; ASL leases preserved", protocol.DecisionReasonUserAcknowledged},
		{"session end cleared its remaining subagent facts", protocol.DecisionReasonSessionAgentsCleared},
		{"  something the reducer newly worded  ", "something the reducer newly worded"},
	}
	for _, tc := range cases {
		got := StableReducerReason(state.ReducerTrace{Decision: protocol.DecisionSuppressed, Reason: tc.reason})
		if got != tc.want {
			t.Fatalf("StableReducerReason(%q) = %q; want %q", tc.reason, got, tc.want)
		}
	}

	if got := StableReducerReason(state.ReducerTrace{Decision: protocol.DecisionApplied}); got != string(protocol.DecisionApplied) {
		t.Fatalf("a traceless decision = %q; want its own decision name", got)
	}
}
