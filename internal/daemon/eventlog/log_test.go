package eventlog

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestSequenceIsZeroValueReadyAndMonotonic(t *testing.T) {
	var seq Sequence
	if got := seq.Value(); got != 0 {
		t.Fatalf("initial Value() = %d; want 0", got)
	}
	for want := uint64(1); want <= 3; want++ {
		if got := seq.next(); got != want {
			t.Fatalf("next() = %d; want %d", got, want)
		}
		if got := seq.Value(); got != want {
			t.Fatalf("Value() = %d; want %d", got, want)
		}
	}
}

func TestAppendStampsReceiptPositionAndResolvesReference(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}
	ref := &protocol.SessionEventReference{}

	appended := Append(st, &seq, protocol.SessionEvent{Event: "PreToolUse", EventRef: ref})

	if appended.DaemonSeq != 1 {
		t.Fatalf("DaemonSeq = %d; want 1", appended.DaemonSeq)
	}
	if ref.DaemonSeq != 1 {
		t.Fatalf("reference DaemonSeq = %d; want the appended row's position", ref.DaemonSeq)
	}
	if appended.EventRef != nil {
		t.Fatal("appended row must not retain the producer's reference pointer")
	}
	if st.EventLog[0].EventRef != nil {
		t.Fatal("stored row must not retain the producer's reference pointer")
	}
	if appended.ReceivedAtNs == 0 {
		t.Fatal("append must stamp a receipt time when the producer did not set one")
	}
	if got := ByReference(st, ref); got == nil || got.Event != "PreToolUse" {
		t.Fatalf("ByReference did not resolve the row it just stamped: %+v", got)
	}
}

func TestAppendPreservesProducerReceiptTime(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}

	appended := Append(st, &seq, protocol.SessionEvent{Event: "Stop", ReceivedAtNs: 42})

	if appended.ReceivedAtNs != 42 {
		t.Fatalf("ReceivedAtNs = %d; want the producer's 42", appended.ReceivedAtNs)
	}
}

func TestAppendTrimsAtMaxEventsPerWindowKeepingNewest(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}

	total := MaxEventsPerWindow + 5
	for i := 0; i < total; i++ {
		Append(st, &seq, protocol.SessionEvent{Event: "PreToolUse", At: int64(i)})
	}

	if len(st.EventLog) != MaxEventsPerWindow {
		t.Fatalf("ring size = %d; want %d", len(st.EventLog), MaxEventsPerWindow)
	}
	if st.EventLog[0].At != 5 {
		t.Fatalf("oldest retained row At = %d; want 5 (front dropped)", st.EventLog[0].At)
	}
	if last := Latest(st); last == nil || last.At != int64(total-1) {
		t.Fatalf("newest retained row = %+v; want At=%d", last, total-1)
	}
	if last := Latest(st); last.DaemonSeq != uint64(total) {
		t.Fatalf("newest DaemonSeq = %d; want %d", last.DaemonSeq, total)
	}
}

func TestLookupsReturnNilForAbsentRows(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}
	Append(st, &seq, protocol.SessionEvent{Event: "PreToolUse", RequestID: "request-1"})

	if got := Latest(nil); got != nil {
		t.Fatal("Latest(nil) must be nil")
	}
	if got := Latest(&state.WindowState{}); got != nil {
		t.Fatal("Latest of an empty ring must be nil")
	}
	if got := ByReference(st, nil); got != nil {
		t.Fatal("ByReference with no reference must be nil")
	}
	if got := ByReference(st, &protocol.SessionEventReference{}); got != nil {
		t.Fatal("ByReference with an unresolved reference must be nil")
	}
	if got := ByReference(st, &protocol.SessionEventReference{DaemonSeq: 99}); got != nil {
		t.Fatal("ByReference with an aged-out position must be nil")
	}
	if got := ByRequestID(st, ""); got != nil {
		t.Fatal("an empty request ID must never match a row")
	}
	if got := ByRequestID(st, "request-2"); got != nil {
		t.Fatal("ByRequestID must be nil for an unknown request")
	}
	if got := ByRequestID(st, "request-1"); got == nil {
		t.Fatal("ByRequestID must find the row its request appended")
	}
}

func TestByRequestIDReturnsTheNewestMatch(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}
	Append(st, &seq, protocol.SessionEvent{Event: "PreToolUse", RequestID: "request-1", At: 1})
	Append(st, &seq, protocol.SessionEvent{Event: "Stop", RequestID: "request-1", At: 2})

	got := ByRequestID(st, "request-1")
	if got == nil || got.At != 2 {
		t.Fatalf("ByRequestID = %+v; want the newest row (At=2)", got)
	}
}

func duplicateReplayRow(requestID, traceID string) protocol.SessionEvent {
	return protocol.SessionEvent{
		Event:           "PreToolUse",
		RequestID:       requestID,
		TraceID:         traceID,
		ReducerDecision: protocol.DecisionDuplicate,
		DecisionReason:  protocol.DecisionReasonRequestReplayed,
	}
}

func TestIsDuplicateReplayNeedsBothTheDecisionAndTheReason(t *testing.T) {
	replay := duplicateReplayRow("request-1", "trace-1")
	if !IsDuplicateReplay(&replay) {
		t.Fatal("a replayed duplicate row must be recognized")
	}
	reordered := replay
	reordered.ReducerDecision = protocol.DecisionReordered
	if IsDuplicateReplay(&reordered) {
		t.Fatal("a non-duplicate decision must not read as a replay")
	}
	other := replay
	other.DecisionReason = protocol.DecisionReasonDuplicateSequence
	if IsDuplicateReplay(&other) {
		t.Fatal("a duplicate that was not a request replay must not read as a replay")
	}
	if IsDuplicateReplay(nil) {
		t.Fatal("a nil row must not read as a replay")
	}
}

func TestFinishRenderStampsCausesAndSkipsReplayedRows(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}
	Append(st, &seq, protocol.SessionEvent{Event: "PreToolUse", TraceID: "trace-1", RequestID: "request-1"})
	Append(st, &seq, duplicateReplayRow("request-1", "trace-2"))
	Append(st, &seq, protocol.SessionEvent{Event: "Stop", TraceID: "trace-3", RequestID: "request-3"})

	FinishRender(st, protocol.RenderGenerationTrace{
		Generation: 7, Stage: "rendered", Reason: "ok",
		TraceIDs: []string{"trace-1"}, RequestIDs: []string{"request-1"},
	})

	if got := st.EventLog[0]; got.RenderGeneration != 7 || got.RenderOutcome != "rendered" || got.RenderReason != "ok" {
		t.Fatalf("causing row = %+v; want generation 7 stamped", got)
	}
	if got := st.EventLog[1]; got.RenderGeneration != 0 {
		t.Fatalf("replayed row = %+v; want no manufactured causality", got)
	}
	if got := st.EventLog[2]; got.RenderGeneration != 0 {
		t.Fatalf("unrelated row = %+v; want no generation", got)
	}
}

func TestLinkProbeTraceAttachesToNewestNonReplayCause(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}
	Append(st, &seq, protocol.SessionEvent{Event: "PreToolUse", TraceID: "trace-1"})
	Append(st, &seq, duplicateReplayRow("request-1", "trace-1"))

	LinkProbeTrace(st, "trace-1", "probe-1")
	LinkProbeTrace(st, "trace-1", "probe-1")
	LinkProbeTrace(st, "trace-1", "probe-2")

	if got := st.EventLog[1].ProbeTraceIDs; len(got) != 0 {
		t.Fatalf("replayed row probe IDs = %v; want none", got)
	}
	want := []string{"probe-1", "probe-2"}
	got := st.EventLog[0].ProbeTraceIDs
	if len(got) != len(want) {
		t.Fatalf("probe IDs = %v; want %v (deduplicated)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("probe IDs = %v; want %v", got, want)
		}
	}
}

func TestLinkProbeTraceIgnoresIncompleteIdentities(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}
	Append(st, &seq, protocol.SessionEvent{Event: "PreToolUse", TraceID: "trace-1"})

	LinkProbeTrace(st, "", "probe-1")
	LinkProbeTrace(st, "trace-1", "")
	LinkProbeTrace(nil, "trace-1", "probe-1")
	LinkProbeTrace(st, "trace-unknown", "probe-1")

	if got := st.EventLog[0].ProbeTraceIDs; len(got) != 0 {
		t.Fatalf("probe IDs = %v; want none", got)
	}
}

func TestRecordEventMaintainsDurableCounters(t *testing.T) {
	var stats protocol.WindowDebugStats

	RecordEvent(&stats, protocol.SessionEvent{At: 10})
	RecordEvent(&stats, protocol.SessionEvent{At: 20, Suppressed: true, SuppressionReason: "guard_blocked"})
	RecordEvent(&stats, protocol.SessionEvent{At: 30, Suppressed: true})

	if stats.EventCount != 3 {
		t.Fatalf("EventCount = %d; want 3", stats.EventCount)
	}
	if stats.FirstEventAt != 10 || stats.LastEventAt != 30 {
		t.Fatalf("first/last = %d/%d; want 10/30", stats.FirstEventAt, stats.LastEventAt)
	}
	if stats.SuppressionReasons["guard_blocked"] != 1 {
		t.Fatalf("suppression histogram = %v; want one guard_blocked", stats.SuppressionReasons)
	}
	if len(stats.SuppressionReasons) != 1 {
		t.Fatalf("suppression histogram = %v; a blank reason must not be counted", stats.SuppressionReasons)
	}

	RecordSuppression(&stats, "guard_blocked")
	RecordSuppression(&stats, "")
	if stats.SuppressionReasons["guard_blocked"] != 2 {
		t.Fatalf("suppression histogram = %v; want a late verdict to reach the same bucket", stats.SuppressionReasons)
	}
	if stats.EventCount != 3 {
		t.Fatalf("EventCount = %d; a late suppression verdict must not re-count the row", stats.EventCount)
	}
}
