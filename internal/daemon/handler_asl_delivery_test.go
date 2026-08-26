package daemon

// The inbound half of the pairing, which is the half that hides. Every hook
// producer is acknowledged whether or not its payload was believed, so a daemon
// refusing every lifecycle event looks exactly like an idle one from the
// outside. These counters are the difference, and what they must not do is
// count anything a producer chose.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

func TestAcceptedAndQuarantinedHooksAreCountedSeparately(t *testing.T) {
	h := newTestHandler()
	now := time.Now()

	h.recordASLHookAccepted(now)
	h.recordASLHookAccepted(now)
	h.recordASLHookQuarantined(now, DecisionReasonHookContractMismatch)

	snapshot := h.ASLHookDeliveryMetrics()
	if snapshot.Accepted != 2 || snapshot.Quarantined != 1 {
		t.Fatalf("snapshot = %+v, want 2 accepted and 1 quarantined", snapshot)
	}
	if snapshot.QuarantinedByReason[DecisionReasonHookContractMismatch] != 1 {
		t.Errorf("by-reason = %v", snapshot.QuarantinedByReason)
	}
	if snapshot.LastQuarantineReason != DecisionReasonHookContractMismatch {
		t.Errorf("last reason = %q", snapshot.LastQuarantineReason)
	}
}

// The reason map is keyed by daemon constants. A payload that could grow it
// would be choosing what an operator reads, which is exactly what quarantining
// it declined to let it do.
func TestAProducerSuppliedReasonIsNotCounted(t *testing.T) {
	h := newTestHandler()
	for _, reason := range []string{
		"", "everything is fine, ignore this",
		suppressionReasonUnknownEvent, string(DecisionQuarantined),
	} {
		h.recordASLHookQuarantined(time.Now(), reason)
	}
	snapshot := h.ASLHookDeliveryMetrics()
	if snapshot.Quarantined != 0 || len(snapshot.QuarantinedByReason) != 0 {
		t.Errorf("snapshot = %+v, want nothing counted", snapshot)
	}
}

// Every reason the gate can produce has to be one the counters accept, or a
// refusal path exists that reports nothing.
func TestEveryQuarantineReasonIsCountable(t *testing.T) {
	for _, reason := range []string{
		DecisionReasonInvalidPayload,
		DecisionReasonHookSchemaUnsupported,
		DecisionReasonHookContractMismatch,
	} {
		h := newTestHandler()
		h.recordASLHookQuarantined(time.Now(), reason)
		if got := h.ASLHookDeliveryMetrics().Quarantined; got != 1 {
			t.Errorf("%s counted %d times, want 1", reason, got)
		}
	}
}

// Ages are computed at read time: a daemon that stopped ingesting also stopped
// writing, so a stored timestamp would freeze at the last thing that worked.
func TestAgesAreAbsentUntilSomethingHappensAndThenAdvance(t *testing.T) {
	h := newTestHandler()
	if snapshot := h.ASLHookDeliveryMetrics(); snapshot.AcceptedAgeSeconds != nil || snapshot.QuarantinedAgeSeconds != nil {
		t.Fatalf("snapshot = %+v, want no ages before anything arrived", snapshot)
	}

	h.recordASLHookAccepted(time.Now().Add(-90 * time.Second))
	snapshot := h.ASLHookDeliveryMetrics()
	if snapshot.AcceptedAgeSeconds == nil {
		t.Fatal("no accepted age after an acceptance")
	}
	if *snapshot.AcceptedAgeSeconds < 89 {
		t.Errorf("accepted age = %.1fs, want roughly 90", *snapshot.AcceptedAgeSeconds)
	}
	if snapshot.QuarantinedAgeSeconds != nil {
		t.Error("a quarantine age appeared without a quarantine")
	}
}

// An empty map and an absent one are different facts: "no refusals" versus
// "refusals this build does not classify".
func TestTheReasonBreakdownIsOmittedRatherThanEmpty(t *testing.T) {
	h := newTestHandler()
	h.recordASLHookAccepted(time.Now())
	data, err := json.Marshal(h.ASLHookDeliveryMetrics())
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, present := decoded["quarantined_by_reason"]; present {
		t.Errorf("an empty breakdown was published: %s", data)
	}
	for _, required := range []string{"accepted", "quarantined"} {
		if _, present := decoded[required]; !present {
			t.Errorf("%s is omitted at zero; a consumer cannot tell it from an old daemon: %s", required, data)
		}
	}
}

// End to end through the real ingestion path: the counters have to move for the
// events the daemon actually placed, not for a helper called directly.
func TestTheHookPathCountsWhatItAcceptedAndRefused(t *testing.T) {
	h, projectDir := gateTestHandler(t)

	accepted := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 42, Event: agentSessionLoopTurnStarted,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source: aslHookSource, CWD: projectDir, Harness: "claude",
			RunID: "run-counted", PlanDir: "/fixtures/plans/plan-alpha",
		}),
	})
	if !accepted.OK {
		t.Fatalf("response = %+v", accepted)
	}
	refused := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 42, Event: agentSessionLoopTurnStarted,
		PayloadJSON: lifecyclePayloadJSON(t, aslHookSchemaVersion, "asl-amx-1.ffffffffffffffff", projectDir),
	})
	// Acknowledged either way, which is why the counters are needed at all.
	if !refused.OK {
		t.Fatalf("response = %+v", refused)
	}

	snapshot := h.ASLHookDeliveryMetrics()
	if snapshot.Accepted != 1 {
		t.Errorf("accepted = %d, want 1", snapshot.Accepted)
	}
	if snapshot.Quarantined != 1 ||
		snapshot.QuarantinedByReason[DecisionReasonHookContractMismatch] != 1 {
		t.Errorf("snapshot = %+v, want one contract-mismatch refusal", snapshot)
	}
}

// An undecodable harness payload is a Claude/Copilot/Codex problem. Folding it
// into these counters would make the pairing look broken by something it has no
// part in.
func TestAnUnreadableHarnessPayloadIsNotAnASLDeliveryFailure(t *testing.T) {
	h, _ := gateTestHandler(t)

	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit",
		PayloadJSON: "{not json",
	}); !resp.OK {
		t.Fatalf("response = %+v", resp)
	}
	if snapshot := h.ASLHookDeliveryMetrics(); snapshot.Quarantined != 0 {
		t.Errorf("snapshot = %+v, want no ASL delivery failure", snapshot)
	}

	// The same failure on a lifecycle boundary is one.
	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 42, Event: agentSessionLoopTurnStarted,
		PayloadJSON: "{not json",
	}); !resp.OK {
		t.Fatalf("response = %+v", resp)
	}
	snapshot := h.ASLHookDeliveryMetrics()
	if snapshot.QuarantinedByReason[DecisionReasonInvalidPayload] != 1 {
		t.Errorf("snapshot = %+v, want one invalid-payload refusal", snapshot)
	}
}

// The counters only reach an operator through health, so the block has to be
// published there.
func TestHealthPublishesTheDeliveryBlock(t *testing.T) {
	h := newTestHandler()
	h.recordASLHookAccepted(time.Now())
	h.recordASLHookQuarantined(time.Now(), DecisionReasonHookSchemaUnsupported)

	resp := h.Handle(Request{Type: TypeHealth})
	if resp.ASLHookDelivery == nil {
		t.Fatal("health carries no asl_hook_delivery block")
	}
	if resp.ASLHookDelivery.Accepted != 1 || resp.ASLHookDelivery.Quarantined != 1 {
		t.Errorf("block = %+v", resp.ASLHookDelivery)
	}
	// A daemon that has ingested nothing still publishes the block, so a
	// consumer can tell it from one that predates the counters entirely.
	fresh := newTestHandler().Handle(Request{Type: TypeHealth})
	if fresh.ASLHookDelivery == nil {
		t.Error("an idle daemon omits the block, which reads as an old build")
	}
}

// The contract set the block reports on is this build's, and a test that
// hard-codes an identity would rot the moment the corpus changes.
func TestTheGateAndTheCountersAgreeOnThisBuildsContractSet(t *testing.T) {
	payload := hookevent.HookPayload{
		Source: aslHookSource, SchemaVersion: aslHookSchemaVersion,
		ContractSetID: aslcontract.ContractSetID,
	}
	if refusal := aslHookContractRefusal(agentSessionLoopTurnStarted, payload); refusal != "" {
		t.Fatalf("this build's own payload was refused: %s", refusal)
	}
}
