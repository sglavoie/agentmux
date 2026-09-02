package daemon

import (
	"encoding/json"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestHandler_CopilotLateInitialSessionStartPreservesPromptAndAllowsStop(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 0
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	h := newTestHandler()
	h.SetStopConfirm(0, 0, false) // pin the immediate-stop path, not the confirmed-idle hold
	fake := &fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "", false), []kitty.Window{{
		ID:       42,
		IsActive: true,
		UserVars: kitty.UserVars{Profile: "copilot", Harness: "copilot"},
	}})}
	h.SetKittyClient(fake)

	promptPayload := hookPayloadJSON(t, hookevent.HookPayload{SessionIDCamel: "session-1"})
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "userPromptSubmitted", PayloadJSON: promptPayload}); !resp.OK {
		t.Fatalf("userPromptSubmitted response = %+v", resp)
	}

	startPayload := hookPayloadJSON(t, hookevent.HookPayload{SessionIDCamel: "session-1", Source: "new"})
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "sessionStart", PayloadJSON: startPayload}); !resp.OK {
		t.Fatalf("sessionStart response = %+v", resp)
	}
	h.stateMu.Lock()
	if got := h.store[42].DesiredIcon; got != "bolt" {
		h.stateMu.Unlock()
		t.Fatalf("late initial sessionStart desired icon = %q, want bolt", got)
	}
	h.stateMu.Unlock()

	stopPayload := hookPayloadJSON(t, hookevent.HookPayload{SessionIDCamel: "session-1", StopReasonCamel: "end_turn"})
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "agentStop", PayloadJSON: stopPayload}); !resp.OK {
		t.Fatalf("agentStop response = %+v", resp)
	}
	h.stateMu.Lock()
	if got := h.store[42].DesiredIcon; got != "stop" {
		h.stateMu.Unlock()
		t.Fatalf("agentStop desired icon = %q, want stop", got)
	}
	h.stateMu.Unlock()

	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 3 {
		t.Fatalf("events = %+v, want prompt/start/stop", detail.Events)
	}
	if detail.Events[1].ResetFlow || detail.Events[1].ResultIconState != protocol.IconStatePreserve {
		t.Fatalf("late sessionStart event = %+v, want non-reset preserve", detail.Events[1])
	}
}

// TestHandler_HookEventClaudePermissionRequestSurfacesKey pins the corrected
// behavior: a Claude PreToolUse → PermissionRequest is a real, user-blocking
// prompt, not an auto-approved tool. The PermissionRequest event is applied (not
// suppressed) and surfaces the 🔑 icon.
func TestHandler_HookEventClaudePermissionRequestSurfacesKey(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
		ID:       42,
		UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
	}})}
	h.SetKittyClient(fake)

	h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse"})
	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "PermissionRequest"})
	if !resp.OK {
		t.Fatalf("PermissionRequest response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 2 {
		t.Fatalf("events = %+v; want two events", detail.Events)
	}
	ev := detail.Events[1]
	if ev.Suppressed || ev.Action != "permission" || ev.ResultIconState != "key" {
		t.Fatalf("event = %+v; want applied permission surfacing the key", ev)
	}
}

// hookPayloadJSON encodes a hook payload the way its producer would send it.
//
// An agent-session-loop payload is stamped with the compatibility block ASL puts
// on every lifecycle boundary, because that is part of what the producer sends
// rather than something the test is asserting. `source` is the discriminator the
// contract itself uses: it is a const in the schema, so a lifecycle payload
// without it is not one. Tests that exercise the gate build their own JSON.
func hookPayloadJSON(t *testing.T, payload hookevent.HookPayload) string {
	t.Helper()
	if payload.Source == events.ASLHookSource {
		payload.SchemaVersion = events.ASLHookSchemaVersion
		payload.ContractSetID = aslcontract.ContractSetID
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal hook payload: %v", err)
	}
	return string(data)
}

func hookEventWindows(tabTitle string, windows []kitty.Window) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:      100,
			Title:   tabTitle,
			Windows: windows,
		}},
	}}
}

// stubDaemonLoopDiscover makes every sweep report candidates as present runs of
// an otherwise healthy state root. Tests that need confirmed-absent or
// indeterminate evidence build a DiscoveryResult directly with
// stubDaemonDiscoverPlans.
func stubDaemonLoopDiscover(t *testing.T, candidates []agentloop.Candidate, err error) {
	t.Helper()
	result := agentloop.DiscoveryResult{StateRoot: "/state", RootPresent: true}
	for _, candidate := range candidates {
		result.Observations = append(result.Observations, agentloop.PlanObservation{
			StateDir:  candidate.StateDir,
			PlanDir:   candidate.PlanDir,
			RunID:     candidate.RunID,
			Presence:  agentloop.PresencePresent,
			Reason:    agentloop.ReasonLiveProcess,
			Candidate: candidate,
		})
	}
	stubDaemonDiscoverPlans(t, result, err)
}

// stubDaemonDiscoverPlans replaces the discovery seam with a fixed sweep.
func stubDaemonDiscoverPlans(t *testing.T, result agentloop.DiscoveryResult, err error) {
	t.Helper()
	old := daemonLoopDiscoverPlans
	daemonLoopDiscoverPlans = func(agentloop.DiscoverOptions) (agentloop.DiscoveryResult, error) {
		out := result
		out.Observations = append([]agentloop.PlanObservation(nil), result.Observations...)
		return out, err
	}
	t.Cleanup(func() {
		daemonLoopDiscoverPlans = old
	})
}

type errHookEventGetTextShouldNotRun struct{}

func (errHookEventGetTextShouldNotRun) Error() string { return "GetText should not run" }
