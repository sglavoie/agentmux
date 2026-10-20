package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestHandler_ElicitationShowsQuestionIcon(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "elicitation"})
	if !resp.OK || !resp.Allowed || resp.IconState != "question" {
		t.Fatalf("want {ok:true, allowed:true, icon_state:question}, got %+v", resp)
	}
}

func TestHandler_ElicitationDuringActiveNotAutoApproved(t *testing.T) {
	t.Parallel()

	// When an active guard is live, elicitation is NOT treated as auto-approved
	// (isAutoApprovedPerm checks for "permission", not "elicitation").
	// The ❓ icon must appear.
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "elicitation"})
	if !resp.OK || !resp.Allowed || resp.IconState != "question" {
		t.Fatalf("elicitation during active guard: want icon_state=question, got %+v", resp)
	}
}

func TestHandler_ElicitationResultClearsGuard(t *testing.T) {
	t.Parallel()

	// After elicitation the guard clears immediately when active arrives,
	// so the bolt icon returns without waiting for guard expiry.
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "elicitation"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "ElicitationResult"})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("active after elicitation: want icon_state=bolt, got %+v", resp)
	}
}

func TestHandler_PendingElicitationSuppressesOrdinaryActive(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "elicitation"})

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		Event:    "PreToolUse",
		ToolName: "Bash",
	})
	if !resp.OK || resp.Allowed {
		t.Fatalf("ordinary active while question pending: want allowed=false, got %+v", resp)
	}
	if got := h.guard.Current(1); got != "elicitation" {
		t.Fatalf("guard.Current = %q, want elicitation", got)
	}

	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 1})
	if len(detail.Events) != 2 {
		t.Fatalf("events = %d, want 2 (%+v)", len(detail.Events), detail.Events)
	}
	ev := detail.Events[1]
	if !ev.Suppressed || ev.SuppressionReason != suppressionReasonElicitationPending {
		t.Fatalf("second event = %+v, want suppressed %q", ev, suppressionReasonElicitationPending)
	}
	if ev.ResultIconState != protocol.IconStatePreserve || ev.CurrentIconState != "question" {
		t.Fatalf("second event icons = current %q result %q, want question -> preserve", ev.CurrentIconState, ev.ResultIconState)
	}
}

func TestHandler_RequestUserInputCompletionClearsElicitation(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "elicitation"})

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		Event:    "PostToolUse",
		ToolName: "request_user_input",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("request_user_input completion should restore active icon, got %+v", resp)
	}
	if got := h.guard.Current(1); got != "active" {
		t.Fatalf("guard.Current = %q, want active", got)
	}
}

func TestHandler_IdleAfterElicitationPreservesIcon(t *testing.T) {
	t.Parallel()

	// idle after elicitation must NOT clear the ❓ — IconState("question") falls
	// through to the default branch in the idle switch, returning IconStatePreserve.
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "elicitation"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "idle"})
	if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
		t.Fatalf("idle after elicitation: want icon_state=%q, got %+v", protocol.IconStatePreserve, resp)
	}
}
