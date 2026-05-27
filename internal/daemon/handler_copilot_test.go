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
