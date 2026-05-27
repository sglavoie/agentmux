package daemon

import (
	"testing"
)

func TestHandler_ClaudeResetFlowUsesUserClear(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{
		Type:      TypeStateUpdate,
		WindowID:  42,
		Action:    "active",
		Event:     "UserPromptSubmit",
		IsPrompt:  true,
		ResetFlow: true,
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("reset prompt should still show activity, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Action:           "stop",
		Event:            "Stop",
		CurrentIconState: "bolt",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "" {
		t.Fatalf("Stop during reset flow should clear instead of stop, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Action:           "stop",
		Event:            "SessionEnd",
		CurrentIconState: "",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "" {
		t.Fatalf("SessionEnd during reset cleanup should stay cleared, got %+v", resp)
	}

	resp = h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Action:           "idle",
		Event:            "SessionStart",
		CurrentIconState: "",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "" {
		t.Fatalf("SessionStart cleanup should finish reset with cleared icon, got %+v", resp)
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if _, ok := h.lastAction[42]; ok {
		t.Fatal("lastAction should be cleared after reset flow finishes")
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
