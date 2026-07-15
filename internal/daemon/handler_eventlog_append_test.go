package daemon

import (
	"testing"
)

// TestHandler_EventLog_AppendOnAllowed verifies that an allowed PreToolUse →
// active transition appends a non-suppressed SessionEvent carrying the request's
// tool_name through to the ring buffer.
func TestHandler_EventLog_AppendOnAllowed(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		Event:    "PreToolUse",
		ToolName: "Bash",
	})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d; want 1 (%+v)", len(resp.Events), resp.Events)
	}
	ev := resp.Events[0]
	if ev.Suppressed {
		t.Errorf("event suppressed=true; want false on allowed path")
	}
	if ev.Action != "active" || ev.Event != "PreToolUse" || ev.ToolName != "Bash" {
		t.Errorf("event = %+v; want action=active event=PreToolUse tool_name=Bash", ev)
	}
	if ev.ResultIconState != "bolt" {
		t.Errorf("event result_icon_state = %q; want bolt", ev.ResultIconState)
	}
	if ev.CurrentIconState != "" {
		t.Errorf("event current_icon_state = %q; want empty without restored daemon state", ev.CurrentIconState)
	}
	if ev.At <= 0 {
		t.Errorf("event At = %d; want > 0", ev.At)
	}
}

func TestHandler_EventLog_CarriesRoutedOriginWindow(t *testing.T) {
	h := newTestHandler()
	normalized := NormalizedEvent{
		Source: SourceID{Kind: SourceHarness, Key: "claude:s1"},
		Action: "active", Seq: 1, At: 100, OriginWindowID: 91,
	}
	h.Handle(Request{
		Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "PreToolUse",
		normalized: &normalized, routeDecision: routeDecisionOwner,
	})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 || resp.Events[0].OriginWindowID != 91 ||
		resp.Events[0].RouteDecision != routeDecisionOwner {
		t.Fatalf("routed event row = %+v; want origin 91 and owner route", resp.Events)
	}
}

// TestHandler_EventLog_AppendOnGuardSuppressed exercises the guard-suppressed
// return at the top of handleStateUpdate: a subagent guard suppresses a
// follow-up "active" (priority 1 < subagent priority 2), and the suppressed
// transition is still recorded in the buffer with Suppressed=true.
func TestHandler_EventLog_AppendOnGuardSuppressed(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse", ToolName: "Read"})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.Events), resp.Events)
	}
	if resp.Events[0].Suppressed {
		t.Errorf("first event suppressed=true; want false (subagent allowed)")
	}
	if !resp.Events[1].Suppressed {
		t.Errorf("second event suppressed=false; want true (active suppressed by subagent guard)")
	}
	if resp.Events[1].Action != "active" || resp.Events[1].ToolName != "Read" {
		t.Errorf("second event = %+v; want action=active tool_name=Read", resp.Events[1])
	}
	if resp.Events[1].ResultIconState != IconStatePreserve || resp.Events[1].SuppressionReason != suppressionReasonGuardBlocked {
		t.Errorf("second event = %+v; want result_icon_state=preserve suppression_reason=%q", resp.Events[1], suppressionReasonGuardBlocked)
	}
	if resp.Events[1].CurrentIconState != "subagent" {
		t.Errorf("second event current_icon_state = %q; want subagent", resp.Events[1].CurrentIconState)
	}
}

// TestHandler_EventLog_AppendOnCopilotMidSubagentSuppression confirms that the
// Copilot agentStop-during-subagent gate also records its suppression — without
// this, the user has no signal in the detail pane explaining why the bolt did
// not remain under the parent subagent icon.
func TestHandler_EventLog_AppendOnCopilotMidSubagentSuppression(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "agentStop"})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.Events), resp.Events)
	}
	if resp.Events[1].Event != "agentStop" || !resp.Events[1].Suppressed {
		t.Errorf("second event = %+v; want event=agentStop suppressed=true", resp.Events[1])
	}
	if resp.Events[1].SuppressionReason != suppressionReasonCopilotMidSubagent || resp.Events[1].ResultIconState != IconStatePreserve {
		t.Errorf("second event = %+v; want suppression_reason=%q result_icon_state=preserve", resp.Events[1], suppressionReasonCopilotMidSubagent)
	}
	if resp.Events[1].CurrentIconState != "subagent" {
		t.Errorf("second event current_icon_state = %q; want subagent", resp.Events[1].CurrentIconState)
	}
}

func TestHandler_EventLog_AppendOnCopilotPendingPromptSuppression(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "elicitation",
		Event:            "notification",
		NotificationType: "elicitation_dialog",
	})
	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "stop",
		Event:    "agentStop",
	})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.Events), resp.Events)
	}
	ev := resp.Events[1]
	if ev.Event != "agentStop" || !ev.Suppressed {
		t.Fatalf("second event = %+v; want event=agentStop suppressed=true", ev)
	}
	if ev.SuppressionReason != suppressionReasonCopilotPendingPrompt {
		t.Fatalf("suppression_reason = %q; want %q", ev.SuppressionReason, suppressionReasonCopilotPendingPrompt)
	}
	if ev.ResultIconState != IconStatePreserve {
		t.Fatalf("result_icon_state = %q; want %q", ev.ResultIconState, IconStatePreserve)
	}
	if ev.CurrentIconState != "question" {
		t.Fatalf("current_icon_state = %q; want question", ev.CurrentIconState)
	}
}

func TestHandler_EventLog_AppendOnActiveAfterTerminalSuppression(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "Stop"})
	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
		Event:    "PostToolUse",
	})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.Events), resp.Events)
	}
	ev := resp.Events[1]
	if !ev.Suppressed {
		t.Fatalf("active-after-terminal event suppressed=false; want true (%+v)", ev)
	}
	if ev.SuppressionReason != suppressionReasonActiveAfterTerminal {
		t.Fatalf("suppression_reason = %q; want %q", ev.SuppressionReason, suppressionReasonActiveAfterTerminal)
	}
	if ev.ResultIconState != IconStatePreserve {
		t.Fatalf("result_icon_state = %q; want %q", ev.ResultIconState, IconStatePreserve)
	}
	if ev.CurrentIconState != "stop" {
		t.Fatalf("current_icon_state = %q; want stop", ev.CurrentIconState)
	}
}

func TestHandler_EventLog_CarriesNormalizedDebugContext(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "stop_failure",
		Event:    "SessionEnd",
		Source:   "clear",

		NotificationType: "agent_idle",
		StopReason:       "context_limit_reached",
		Reason:           "error",
		ContextLimit:     true,
	})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d; want 1 (%+v)", len(resp.Events), resp.Events)
	}
	ev := resp.Events[0]
	if ev.ResultIconState != "error" {
		t.Fatalf("result_icon_state = %q; want error", ev.ResultIconState)
	}
	if ev.CurrentIconState != "" {
		t.Fatalf("current_icon_state = %q; want empty without restored daemon state", ev.CurrentIconState)
	}
	if ev.NotificationType != "agent_idle" {
		t.Fatalf("notification_type = %q; want agent_idle", ev.NotificationType)
	}
	if ev.StopReason != "context_limit_reached" {
		t.Fatalf("stop_reason = %q; want context_limit_reached", ev.StopReason)
	}
	if ev.Reason != "error" {
		t.Fatalf("reason = %q; want error", ev.Reason)
	}
	if ev.Source != "clear" {
		t.Fatalf("source = %q; want clear", ev.Source)
	}
	if !ev.ContextLimit {
		t.Fatal("context_limit = false; want true")
	}
}

func TestHandler_EventLog_LoopFinishedCarriesSourceAndReason(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "idle",
		Event:    "AgentSessionLoopFinished",
		Source:   "agent_session_loop",
		Reason:   "run_finished",
	})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.Events), resp.Events)
	}
	ev := resp.Events[1]
	if ev.Event != "AgentSessionLoopFinished" || ev.Action != "idle" {
		t.Fatalf("finish event identity = (%q, %q); want (AgentSessionLoopFinished, idle)", ev.Event, ev.Action)
	}
	if ev.Source != "agent_session_loop" {
		t.Fatalf("source = %q; want agent_session_loop", ev.Source)
	}
	if ev.Reason != "run_finished" {
		t.Fatalf("reason = %q; want run_finished", ev.Reason)
	}
	if ev.ResultIconState != "" {
		t.Fatalf("result_icon_state = %q; want empty icon", ev.ResultIconState)
	}
	if ev.CurrentIconState != "bolt" {
		t.Fatalf("current_icon_state = %q; want bolt", ev.CurrentIconState)
	}
}

func TestHandler_EventLog_RewrittenResetStopCarriesResetFlow(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:      TypeStateUpdate,
		WindowID:  1,
		Harness:   "copilot",
		Action:    "active",
		Event:     "userPromptSubmitted",
		IsPrompt:  true,
		ResetFlow: true,
	})
	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Harness:  "copilot",
		Action:   "stop",
		Event:    "agentStop",
	})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.Events), resp.Events)
	}
	ev := resp.Events[1]
	if ev.Action != "user_clear" {
		t.Fatalf("action = %q; want user_clear", ev.Action)
	}
	if !ev.ResetFlow {
		t.Fatal("reset_flow = false; want true")
	}
	if ev.ResultIconState != "" {
		t.Fatalf("result_icon_state = %q; want empty icon", ev.ResultIconState)
	}
}

// TestHandler_EventLog_TrimsAtMax pushes maxEventsPerWindow + 5 events through
// the allowed path and asserts the buffer retains exactly maxEventsPerWindow
// (oldest dropped from the front).
func TestHandler_EventLog_TrimsAtMax(t *testing.T) {
	h := newTestHandler()

	total := maxEventsPerWindow + 5
	for i := 0; i < total; i++ {
		h.Handle(Request{
			Type:     TypeStateUpdate,
			WindowID: 1,
			Action:   "active",
			Event:    "PreToolUse",
			ToolName: "tool",
		})
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != maxEventsPerWindow {
		t.Fatalf("events = %d; want %d", len(resp.Events), maxEventsPerWindow)
	}
}

// TestHandler_EventLog_PreservedAcrossTerminalActions pushes two non-terminal
// events and then a terminal "stop"; the buffer must retain all three
// (including the terminal hook) so the detail pane can show the session's
// history after it ends.
func TestHandler_EventLog_PreservedAcrossTerminalActions(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "Stop"})

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 3 {
		t.Fatalf("events after stop = %d; want 3 (%+v)", len(resp.Events), resp.Events)
	}
	if resp.Events[0].Event != "PreToolUse" || resp.Events[1].Event != "PostToolUse" || resp.Events[2].Event != "Stop" {
		t.Errorf("events out of order: %+v", resp.Events)
	}
	if resp.Events[2].Action != "stop" {
		t.Errorf("terminal event action = %q; want %q", resp.Events[2].Action, "stop")
	}
}

// TestHandler_EventLog_CapAt5000 hard-codes the 5000-event cap so a future
// change to maxEventsPerWindow trips this assertion and forces an explicit
// decision about whether the contract still holds.
func TestHandler_EventLog_CapAt5000(t *testing.T) {
	h := newTestHandler()

	const cap5k = 5000
	for i := 0; i < cap5k+1; i++ {
		h.Handle(Request{
			Type:     TypeStateUpdate,
			WindowID: 1,
			Action:   "active",
			Event:    "PreToolUse",
			ToolName: "tool",
		})
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != cap5k {
		t.Fatalf("events = %d; want %d (drop-oldest cap)", len(resp.Events), cap5k)
	}
}
