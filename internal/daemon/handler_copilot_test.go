package daemon

import (
	"testing"
)

func TestHandler_CopilotCompactCleanupSynthesizesStop(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "compact",
		Event:    "preCompact",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "compact" {
		t.Fatalf("Copilot preCompact should show compact icon, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "idle",
		Event:            "notification",
		CurrentIconState: "compact",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
		t.Fatalf("Copilot compaction cleanup should synthesize stop icon, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "active",
		Event:            "postToolUse",
		CurrentIconState: "stop",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("Copilot post-compaction stop should block non-prompt active follow-up, got %+v", resp)
	}
}

// TestHandler_CopilotTurnEndClearsBolt pins the user-visible fix: when Copilot
// fires agentStop with no subagents in flight, the daemon must transition the
// tab from the bolt to the stop icon, not preserve the existing bolt.
// Regression source: commit 5858318.
func TestHandler_CopilotTurnEndClearsBolt(t *testing.T) {
	h := newTestHandler()
	// Seed the guard at "active" (bolt visible).
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Account: "work",
		Event: "preToolUse", Action: "active"})
	// agentStop arrives — now mapped to action "stop" with PreservesIcon=false.
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Account: "work",
		Event: "agentStop", Action: "stop", PreservesIcon: false})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("agentStop should land: %+v", resp)
	}
	if resp.IconState != "stop" {
		t.Errorf("IconState = %q, want stop", resp.IconState)
	}
}

// TestHandler_CopilotTurnEndDuringSubagentPreserves pins the mid-subagent
// suppression branch in handleStateUpdate (handler_state_update.go:152): while a
// subagent is running, an agentStop must not surface a terminal stop icon — the
// reference-counted 🤖 guard remains the source of truth until the last
// subagent drains. subagentCount is seeded directly to isolate the gate from
// the unrelated SubagentStart bookkeeping.
func TestHandler_CopilotTurnEndDuringSubagentPreserves(t *testing.T) {
	h := newTestHandler()
	h.stateMu.Lock()
	h.subagentCount[42] = 1
	h.stateMu.Unlock()

	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Account: "work",
		Event: "agentStop", Action: "stop", PreservesIcon: false})
	if !resp.OK {
		t.Fatalf("agentStop should be handled OK: %+v", resp)
	}
	if resp.Allowed {
		t.Errorf("Allowed = true; want false (gate must suppress)")
	}
	if resp.IconState == "stop" {
		t.Errorf("IconState = stop; want preserve/empty (gate must not transition)")
	}
}

func TestHandler_CopilotResetFlowUsesUserClear(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{
		Type:      TypeStateUpdate,
		WindowID:  42,
		Harness:   "copilot",
		Action:    "active",
		Event:     "userPromptSubmitted",
		IsPrompt:  true,
		ResetFlow: true,
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("Copilot reset prompt should still show activity, got %+v", resp)
	}

	for _, req := range []Request{
		{
			Type:             TypeStateUpdate,
			WindowID:         42,
			Harness:          "copilot",
			Action:           "stop",
			Event:            "agentStop",
			CurrentIconState: "bolt",
		},
		{
			Type:             TypeStateUpdate,
			WindowID:         42,
			Harness:          "copilot",
			Action:           "stop",
			Event:            "sessionEnd",
			CurrentIconState: "",
		},
		{
			Type:             TypeStateUpdate,
			WindowID:         42,
			Harness:          "copilot",
			Action:           "idle",
			Event:            "sessionStart",
			CurrentIconState: "",
		},
	} {
		resp = h.Handle(req)
		if !resp.OK || !resp.Allowed || resp.IconState != "" {
			t.Fatalf("%s should be rewritten to user_clear during reset flow, got %+v", req.Event, resp)
		}
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if _, ok := h.lastAction[42]; ok {
		t.Fatal("lastAction should be cleared after Copilot reset flow finishes")
	}
}

func TestHandler_ResetFlowDoesNotLeakIntoLaterTurn(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:      TypeStateUpdate,
		WindowID:  42,
		Harness:   "copilot",
		Action:    "active",
		Event:     "userPromptSubmitted",
		IsPrompt:  true,
		ResetFlow: true,
	})

	resp := h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Harness:          "copilot",
		Action:           "stop",
		Event:            "agentStop",
		CurrentIconState: "bolt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "" {
		t.Fatalf("reset stop should clear icon, got %+v", resp)
	}

	for _, req := range []Request{
		{
			Type:             TypeStateUpdate,
			WindowID:         42,
			Harness:          "copilot",
			Action:           "stop",
			Event:            "sessionEnd",
			CurrentIconState: "",
		},
		{
			Type:             TypeStateUpdate,
			WindowID:         42,
			Harness:          "copilot",
			Action:           "idle",
			Event:            "sessionStart",
			CurrentIconState: "",
		},
		{
			Type:             TypeStateUpdate,
			WindowID:         42,
			Harness:          "copilot",
			Action:           "stop",
			Event:            "notification",
			NotificationType: "agent_idle",
			CurrentIconState: "",
		},
	} {
		resp = h.Handle(req)
		if !resp.OK || !resp.Allowed || resp.IconState != "" {
			t.Fatalf("%s during reset follow-up should stay cleared, got %+v", req.Event, resp)
		}
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Harness:          "copilot",
		Action:           "active",
		Event:            "userPromptSubmitted",
		IsPrompt:         true,
		CurrentIconState: "",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("new non-reset prompt should start normal turn, got %+v", resp)
	}

	h.stateMu.Lock()
	if _, ok := h.pendingReset[42]; ok {
		h.stateMu.Unlock()
		t.Fatal("pendingReset marker should be cleared once the next normal turn begins")
	}
	h.stateMu.Unlock()

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Harness:          "copilot",
		Action:           "stop",
		Event:            "agentStop",
		CurrentIconState: "bolt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
		t.Fatalf("ordinary stop after reset flow should remain stop, got %+v", resp)
	}
}
