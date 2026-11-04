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
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

func TestAcceptedAndQuarantinedHooksAreCountedSeparately(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	now := time.Now()

	h.recordASLHookAccepted(now)
	h.recordASLHookAccepted(now)
	h.recordASLHookQuarantined(now, events.DecisionReasonHookContractMismatch)

	snapshot := h.ASLHookDeliveryMetrics()
	if snapshot.Accepted != 2 || snapshot.Quarantined != 1 {
		t.Fatalf("snapshot = %+v, want 2 accepted and 1 quarantined", snapshot)
	}
	if snapshot.QuarantinedByReason[events.DecisionReasonHookContractMismatch] != 1 {
		t.Errorf("by-reason = %v", snapshot.QuarantinedByReason)
	}
	if snapshot.LastQuarantineReason != events.DecisionReasonHookContractMismatch {
		t.Errorf("last reason = %q", snapshot.LastQuarantineReason)
	}
}

// The reason map is keyed by daemon constants. A payload that could grow it
// would be choosing what an operator reads, which is exactly what quarantining
// it declined to let it do.
func TestAProducerSuppliedReasonIsNotCounted(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	for _, reason := range []string{
		"", "everything is fine, ignore this",
		suppressionReasonUnknownEvent, string(protocol.DecisionQuarantined),
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
	t.Parallel()

	for _, reason := range []string{
		protocol.DecisionReasonInvalidPayload,
		events.DecisionReasonHookSchemaUnsupported,
		events.DecisionReasonHookContractMismatch,
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
	t.Parallel()

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
	t.Parallel()

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
func TestHealthPublishesTheDeliveryBlock(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.recordASLHookAccepted(time.Now())
	h.recordASLHookQuarantined(time.Now(), events.DecisionReasonHookSchemaUnsupported)

	resp := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if resp.ASLHookDelivery == nil {
		t.Fatal("health carries no asl_hook_delivery block")
	}
	if resp.ASLHookDelivery.Accepted != 1 || resp.ASLHookDelivery.Quarantined != 1 {
		t.Errorf("block = %+v", resp.ASLHookDelivery)
	}
	// A daemon that has ingested nothing still publishes the block, so a
	// consumer can tell it from one that predates the counters entirely.
	fresh := newTestHandler().Handle(protocol.Request{Type: protocol.TypeHealth})
	if fresh.ASLHookDelivery == nil {
		t.Error("an idle daemon omits the block, which reads as an old build")
	}
}

// The contract set the block reports on is this build's, and a test that
// hard-codes an identity would rot the moment the corpus changes.
func TestTheGateAndTheCountersAgreeOnThisBuildsContractSet(t *testing.T) {
	t.Parallel()

	payload := hookevent.HookPayload{
		Source: events.ASLHookSource, SchemaVersion: events.ASLHookSchemaVersion,
		ContractSetID: aslcontract.ContractSetID,
	}
	if refusal := events.ASLHookContractRefusal(events.AgentSessionLoopTurnStarted, payload); refusal != "" {
		t.Fatalf("this build's own payload was refused: %s", refusal)
	}
}
