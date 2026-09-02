package daemon

// The lifecycle-hook gate checked from the daemon's side: a refused payload has
// to leave nothing behind — no reduced state, no loop lease, no producer text
// on an operator's screen — while harness hooks, which carry no contract block
// at all, keep working exactly as before. The vendored-corpus direction of the
// same gate lives beside the gate itself, in internal/daemon/events.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func gateTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	h, windows, projectDir := reconcileTestFixture(t)
	h.SetKittyClient(&fakeKittyClient{windows: windows})
	h.publishSnapshot(windows, time.Now())
	return h, projectDir
}

// lifecyclePayloadJSON builds a lifecycle payload with the compatibility block
// spelled explicitly, which is what a test of the gate needs: hookPayloadJSON
// stamps the correct block for every other ASL test, and these cases exist to
// send a wrong one.
func lifecyclePayloadJSON(t *testing.T, schemaVersion int, contractSetID, projectDir string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"schema_version":  schemaVersion,
		"contract_set_id": contractSetID,
		"source":          events.ASLHookSource,
		"cwd":             projectDir,
		"harness":         "claude",
		"run_id":          "run-gated",
		"plan_dir":        "/fixtures/plans/plan-alpha",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(data)
}

func TestAnIncompatibleLifecycleHookIsQuarantinedWithoutReduction(t *testing.T) {
	for _, tc := range []struct {
		name          string
		schemaVersion int
		contractSetID string
		want          string
	}{
		{"a retired schema", 1, aslcontract.ContractSetID, events.DecisionReasonHookSchemaUnsupported},
		{"no compatibility block at all", 0, "", events.DecisionReasonHookSchemaUnsupported},
		{"a schema from the future", 3, aslcontract.ContractSetID, events.DecisionReasonHookSchemaUnsupported},
		{"a foreign contract set", events.ASLHookSchemaVersion, "asl-amx-1.ffffffffffffffff", events.DecisionReasonHookContractMismatch},
		{"a set with no identity", events.ASLHookSchemaVersion, "", events.DecisionReasonHookContractMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, projectDir := gateTestHandler(t)

			resp := h.Handle(protocol.Request{
				Type:        protocol.TypeHookEvent,
				WindowID:    42,
				Event:       events.AgentSessionLoopTurnStarted,
				PayloadJSON: lifecyclePayloadJSON(t, tc.schemaVersion, tc.contractSetID, projectDir),
			})
			// The hook process is acknowledged either way. It cannot do anything with
			// a refusal, and a nonzero exit would turn an incompatibility into noise
			// in the operator's own loop output.
			if !resp.OK {
				t.Fatalf("response = %+v, want an acknowledged refusal", resp)
			}

			detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
			if len(detail.Events) != 1 {
				t.Fatalf("events = %+v, want the single quarantined event", detail.Events)
			}
			event := detail.Events[0]
			if !event.Quarantined || event.ReducerDecision != protocol.DecisionQuarantined ||
				event.IdentityConfidence != protocol.IdentityConfidenceInvalid ||
				event.NormalizationOutcome != protocol.NormalizationOutcomeQuarantined {
				t.Errorf("event = %+v, want a quarantined record", event)
			}
			if event.DecisionReason != tc.want || event.ReducerReason != tc.want {
				t.Errorf("reason = %q/%q, want %q", event.DecisionReason, event.ReducerReason, tc.want)
			}

			// Nothing the payload chose may reach an operator. A hook is free to put a
			// plan path in either compatibility field, so the offered values are
			// private diagnostics and the published record names neither.
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			for _, leaked := range []string{"ffffffffffffffff", projectDir, "plan-alpha"} {
				if strings.Contains(string(encoded), leaked) {
					t.Errorf("published event quotes %q: %s", leaked, encoded)
				}
			}

			if looping, icon := windowLoopState(t, h, 42); looping || icon != "" {
				t.Errorf("refused payload left looping=%v icon=%q; want untouched", looping, icon)
			}
			h.stateMu.Lock()
			st := h.store[42]
			h.stateMu.Unlock()
			if st == nil || st.Reducer != nil {
				t.Errorf("refused payload built reducer state: %+v", st)
			}
		})
	}
}

// TestTheHookRefusalIsStableAndNonRetryable covers the other half of what an
// operator needs from a compatibility failure: the same pairing must always
// produce the same words, and resending must never be the advice. Both reasons
// are daemon-authored constants for exactly that purpose.
func TestTheHookRefusalIsStableAndNonRetryable(t *testing.T) {
	payload := hookevent.HookPayload{Source: events.ASLHookSource, SchemaVersion: 1}
	first := events.ASLHookContractRefusal(events.AgentSessionLoopTurnStarted, payload)
	if first != events.ASLHookContractRefusal(events.AgentSessionLoopTurnStarted, payload) {
		t.Fatal("the refusal is not stable for one payload")
	}
	for _, reason := range []string{
		events.DecisionReasonHookSchemaUnsupported,
		events.DecisionReasonHookContractMismatch,
	} {
		if !events.IsQuarantineReason(reason) {
			t.Errorf("%q does not mark the event as quarantined", reason)
		}
		if strings.ContainsAny(reason, " \n\r\t") || strings.ToLower(reason) != reason {
			t.Errorf("reason %q is not a stable identifier", reason)
		}
	}
}

// TestHarnessHooksAreUnaffectedByTheLifecycleGate is the availability half of
// the contract: refusing an ASL event must never cost Agent Mux its core
// function. A harness hook carries no compatibility block, is not asked to, and
// reduces normally.
func TestHarnessHooksAreUnaffectedByTheLifecycleGate(t *testing.T) {
	h := newTestHandler()
	windows := hookEventWindows(palette.BuildTitle("api", "", false), []kitty.Window{{
		ID:       42,
		IsActive: true,
		UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
	}})
	h.SetKittyClient(&fakeKittyClient{windows: windows})
	h.publishSnapshot(windows, time.Now())

	// Deliberately zero-valued: this is exactly the payload the gate refuses when
	// the event is a lifecycle boundary.
	payload := hookevent.HookPayload{SessionID: "session-1"}
	if payload.SchemaVersion != 0 || payload.ContractSetID != "" {
		t.Fatal("the harness payload carries a compatibility block")
	}
	if refusal := events.ASLHookContractRefusal("UserPromptSubmit", payload); refusal != "" {
		t.Fatalf("a harness hook was gated: %s", refusal)
	}

	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit",
		Harness: harness.Claude, PayloadJSON: hookPayloadJSON(t, payload),
	})
	if !resp.OK {
		t.Fatalf("response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %+v", detail.Events)
	}
	if event := detail.Events[0]; event.Quarantined || event.ReducerDecision == protocol.DecisionQuarantined {
		t.Errorf("harness event = %+v, want an ordinary reduction", event)
	}
	h.stateMu.Lock()
	icon := h.store[42].DesiredIcon
	h.stateMu.Unlock()
	if icon != "bolt" {
		t.Errorf("harness prompt icon = %q, want bolt", icon)
	}
}
