package daemon

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestHandler_EventLog_AppendOnAllowed verifies that an allowed PreToolUse →
// active transition appends a non-suppressed SessionEvent carrying the request's
// tool_name through to the ring buffer.
func TestHandler_EventLog_AppendOnAllowed(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Action:           "active",
		Event:            "PreToolUse",
		ToolName:         "Bash",
		CurrentIconState: "key",
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
	if ev.CurrentIconState != "key" {
		t.Errorf("event current_icon_state = %q; want key", ev.CurrentIconState)
	}
	if ev.At <= 0 {
		t.Errorf("event At = %d; want > 0", ev.At)
	}
}

// TestHandler_EventLog_AppendOnGuardSuppressed exercises the guard-suppressed
// return at the top of handleStateUpdate: a subagent guard suppresses a
// follow-up "active" (priority 1 < subagent priority 2), and the suppressed
// transition is still recorded in the buffer with Suppressed=true.
func TestHandler_EventLog_AppendOnGuardSuppressed(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse", ToolName: "Read", CurrentIconState: "subagent"})

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
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "agentStop", CurrentIconState: "subagent"})

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
		Action:           "permission",
		Event:            "notification",
		NotificationType: "elicitation_dialog",
		CurrentIconState: "bolt",
	})
	h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "stop",
		Event:            "agentStop",
		CurrentIconState: "key",
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
	if ev.CurrentIconState != "key" {
		t.Fatalf("current_icon_state = %q; want key", ev.CurrentIconState)
	}
}

func TestHandler_EventsExport_CopilotCompactCleanupSynthesizesStop(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "compact",
		Event:            "preCompact",
		SessionTitle:     "W:api",
		Account:          "work",
		CurrentIconState: "bolt",
	})
	h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "idle",
		Event:            "notification",
		SessionTitle:     "W:api",
		Account:          "work",
		CurrentIconState: "compact",
	})

	resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 1})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if len(resp.EventsByWindow[1]) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.EventsByWindow[1]), resp.EventsByWindow[1])
	}
	ev := resp.EventsByWindow[1][1]
	if ev.Event != "notification" {
		t.Fatalf("event = %q; want notification", ev.Event)
	}
	if ev.Action != "stop" {
		t.Fatalf("action = %q; want stop", ev.Action)
	}
	if ev.ResultIconState != "stop" {
		t.Fatalf("result_icon_state = %q; want stop", ev.ResultIconState)
	}
	if ev.CurrentIconState != "compact" {
		t.Fatalf("current_icon_state = %q; want compact", ev.CurrentIconState)
	}
	meta := resp.WindowMetadata[1]
	if meta.Durable.LastAction != "stop" {
		t.Fatalf("last_action = %q; want stop", meta.Durable.LastAction)
	}
	if meta.Durable.Harness != "copilot" {
		t.Fatalf("harness = %q; want copilot", meta.Durable.Harness)
	}
}

func TestHandler_EventLog_AppendOnActiveAfterTerminalSuppression(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "Stop"})
	h.Handle(Request{
		Type:             TypeStateUpdate,
		WindowID:         1,
		Action:           "active",
		Event:            "PostToolUse",
		CurrentIconState: "stop",
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
		Type:             TypeStateUpdate,
		WindowID:         1,
		Action:           "stop_failure",
		Event:            "SessionEnd",
		Source:           "clear",
		CurrentIconState: "bolt",
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
	if ev.CurrentIconState != "bolt" {
		t.Fatalf("current_icon_state = %q; want bolt", ev.CurrentIconState)
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
		Type:             TypeStateUpdate,
		WindowID:         1,
		Harness:          "copilot",
		Action:           "stop",
		Event:            "agentStop",
		CurrentIconState: "bolt",
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

// TestHandler_EventLog_PreservedAcrossStaleSweep verifies that SweepStale leaves
// the per-window ring untouched while the underlying Kitty window is still
// live: the detail pane must continue serving history after the bolt icon has
// been swept.
func TestHandler_EventLog_PreservedAcrossStaleSweep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, _ := newSweepTestHandler(10 * time.Minute)
		// Tell the fake Kitty client that window 1 is still alive so the new
		// window-existence GC at the top of SweepStale does not drop the buffer.
		fake.mu.Lock()
		fake.windows = fakeOSWindowsWithTab(1, "W:api")
		fake.mu.Unlock()

		h.Handle(Request{
			Type:         TypeStateUpdate,
			WindowID:     1,
			Action:       "active",
			Event:        "PreToolUse",
			ToolName:     "Bash",
			SessionTitle: "W:api",
		})

		// Backdate the activity timestamp so the sweep treats the window as stale.
		h.stateMu.Lock()
		h.activity[1] = time.Now().Add(-15 * time.Minute)
		h.stateMu.Unlock()

		h.SweepStale(time.Now())
		synctest.Wait()

		resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
		if !resp.OK {
			t.Fatalf("session detail: want OK=true, got %+v", resp)
		}
		if len(resp.Events) != 1 {
			t.Fatalf("events after SweepStale = %d; want 1 (%+v)", len(resp.Events), resp.Events)
		}
		if resp.Events[0].Event != "PreToolUse" {
			t.Errorf("event = %+v; want PreToolUse", resp.Events[0])
		}
	})
}

// TestHandler_EventLog_GCDropsClosedWindows seeds event-log entries for two
// windows, configures the fake Kitty client to report only one of them as
// live, runs SweepStale, and asserts that the closed window's entry is
// pruned while the live window's entry survives.
func TestHandler_EventLog_GCDropsClosedWindows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, _ := newSweepTestHandler(10 * time.Minute)
		fake.mu.Lock()
		fake.windows = fakeOSWindowsWithTab(1, "W:api")
		fake.mu.Unlock()

		now := time.Now()
		h.stateMu.Lock()
		h.eventLog[1] = []SessionEvent{{At: now.Unix(), Event: "PreToolUse", Action: "active"}}
		h.eventLog[2] = []SessionEvent{{At: now.Unix(), Event: "PreToolUse", Action: "active"}}
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		resp1 := h.Handle(Request{Type: TypeSessionDetail, WindowID: 1})
		if !resp1.OK {
			t.Fatalf("session detail w1: want OK=true, got %+v", resp1)
		}
		if len(resp1.Events) != 1 {
			t.Fatalf("window 1 events = %d; want 1 (%+v)", len(resp1.Events), resp1.Events)
		}

		resp2 := h.Handle(Request{Type: TypeSessionDetail, WindowID: 2})
		if !resp2.OK {
			t.Fatalf("session detail w2: want OK=true, got %+v", resp2)
		}
		if len(resp2.Events) != 0 {
			t.Fatalf("window 2 events = %d; want 0 — closed-window GC should drop it (%+v)", len(resp2.Events), resp2.Events)
		}
	})
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

// TestHandler_SessionDetail_UnknownWindow asserts that querying detail for a
// window that has never produced an event returns a non-nil empty slice. The
// TUI relies on this to distinguish "no daemon error" from "no events".
func TestHandler_SessionDetail_UnknownWindow(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 99})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if resp.Events == nil {
		t.Fatal("Events = nil; want non-nil empty slice")
	}
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d; want 0", len(resp.Events))
	}
}

// TestHandler_EventsExport_ReturnsAllWindows seeds three windows with distinct
// event counts and asserts the export returns every window keyed by its ID
// with the right per-window count. This pins the multi-window aggregation that
// the new `amx daemon export-events` CLI depends on.
func TestHandler_EventsExport_ReturnsAllWindows(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse", SessionTitle: "W:one"})

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "active", Event: "PreToolUse", SessionTitle: "W:two"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "stop", Event: "Stop", SessionTitle: "W:two"})

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 3, Action: "active", Event: "PreToolUse", SessionTitle: "W:three"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 3, Action: "active", Event: "PostToolUse", SessionTitle: "W:three"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 3, Action: "stop", Event: "Stop", SessionTitle: "W:three"})

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if got := len(resp.EventsByWindow); got != 3 {
		t.Fatalf("len(EventsByWindow) = %d; want 3 (%+v)", got, resp.EventsByWindow)
	}
	want := map[int]int{1: 1, 2: 2, 3: 3}
	for id, n := range want {
		got := len(resp.EventsByWindow[id])
		if got != n {
			t.Errorf("window %d events = %d; want %d", id, got, n)
		}
	}
	if got := len(resp.WindowMetadata); got != 3 {
		t.Fatalf("len(WindowMetadata) = %d; want 3 (%+v)", got, resp.WindowMetadata)
	}
}

func TestHandler_EventsExport_WindowFilterReturnsOneWindow(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse", SessionTitle: "W:one"})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     2,
		Action:       "active",
		Event:        "PreToolUse",
		SessionTitle: "W:two",
		Account:      "work",
		Harness:      "copilot",
	})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     2,
		Action:       "stop",
		Event:        "Stop",
		SessionTitle: "W:two",
		Account:      "work",
		Harness:      "copilot",
	})

	resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 2})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if got := len(resp.EventsByWindow); got != 1 {
		t.Fatalf("len(EventsByWindow) = %d; want 1 (%+v)", got, resp.EventsByWindow)
	}
	if got := len(resp.WindowMetadata); got != 1 {
		t.Fatalf("len(WindowMetadata) = %d; want 1 (%+v)", got, resp.WindowMetadata)
	}
	if got := len(resp.EventsByWindow[2]); got != 2 {
		t.Fatalf("window 2 events = %d; want 2", got)
	}
	if _, ok := resp.EventsByWindow[1]; ok {
		t.Fatalf("unexpected window 1 events in filtered export: %+v", resp.EventsByWindow)
	}
	if meta := resp.WindowMetadata[2]; meta.Durable.SessionTitle != "W:two" || meta.Durable.Harness != "copilot" {
		t.Fatalf("window 2 metadata = %+v; want durable session title/harness", meta)
	}
}

// TestHandler_EventsExport_EmptyWhenNoEvents asserts the export succeeds when
// no events have ever been buffered. The map may be nil or empty — both
// encode the same "no investigation surface yet" semantics on the wire.
func TestHandler_EventsExport_EmptyWhenNoEvents(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if len(resp.EventsByWindow) != 0 {
		t.Fatalf("EventsByWindow = %+v; want empty", resp.EventsByWindow)
	}
	if len(resp.WindowMetadata) != 0 {
		t.Fatalf("WindowMetadata = %+v; want empty", resp.WindowMetadata)
	}
}

// TestHandler_EventsExport_DoesNotMutateOriginal pins the defensive copy in
// handleEventsExport: a caller that mutates the returned slice must not be
// able to corrupt the daemon's internal ring buffer. Without the per-window
// `make+copy`, the returned slice header would alias the buffer and any
// in-place write would leak back into subsequent exports.
func TestHandler_EventsExport_DoesNotMutateOriginal(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "PreToolUse",
		ToolName:     "Bash",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "copilot",
	})

	first := h.Handle(Request{Type: TypeEventsExport})
	if !first.OK || len(first.EventsByWindow[1]) != 1 {
		t.Fatalf("first export: want one event for window 1, got %+v", first)
	}

	// Tamper with the returned slice. If the handler aliases its internal
	// buffer, the next export will reflect this mutation.
	first.EventsByWindow[1][0].Event = "TAMPERED"
	meta := first.WindowMetadata[1]
	meta.Durable.SessionTitle = "TAMPERED"
	first.WindowMetadata[1] = meta

	second := h.Handle(Request{Type: TypeEventsExport})
	if !second.OK || len(second.EventsByWindow[1]) != 1 {
		t.Fatalf("second export: want one event for window 1, got %+v", second)
	}
	if got := second.EventsByWindow[1][0].Event; got != "PreToolUse" {
		t.Fatalf("internal buffer mutated: event = %q; want %q", got, "PreToolUse")
	}
	if got := second.WindowMetadata[1].Durable.SessionTitle; got != "W:api" {
		t.Fatalf("metadata mutated: session_title = %q; want %q", got, "W:api")
	}
}

func TestHandler_EventsExport_CarriesSource(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "SessionStart",
		Source:       "new",
		SessionTitle: "W:api",
	})

	resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 1})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if len(resp.EventsByWindow[1]) != 1 {
		t.Fatalf("window 1 events = %d; want 1 (%+v)", len(resp.EventsByWindow[1]), resp.EventsByWindow[1])
	}
	if got := resp.EventsByWindow[1][0].Source; got != "new" {
		t.Fatalf("source = %q; want new", got)
	}
}

func TestHandler_EventsExport_IncludesDurableMetadata(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "PreToolUse",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "Copilot",
		PipeMode:     true,
	})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "idle",
		Event:        "TeammateIdle",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "Copilot",
		PipeMode:     true,
	})
	// Simulate a degraded terminal hook where the daemon no longer gets live
	// Kitty identity strings. The durable snapshot must keep the prior values.
	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "stop",
		Event:    "Stop",
		PipeMode: true,
	})

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	meta, ok := resp.WindowMetadata[1]
	if !ok {
		t.Fatalf("WindowMetadata[1] missing; got %+v", resp.WindowMetadata)
	}
	if meta.Stats.EventCount != 3 {
		t.Fatalf("event_count = %d; want 3", meta.Stats.EventCount)
	}
	if meta.Stats.FirstEventAt == 0 || meta.Stats.LastEventAt == 0 {
		t.Fatalf("stats timestamps = %+v; want non-zero first/last", meta.Stats)
	}
	if meta.Durable.SessionTitle != "W:api" {
		t.Fatalf("session_title = %q; want %q", meta.Durable.SessionTitle, "W:api")
	}
	if meta.Durable.Profile != "work" {
		t.Fatalf("profile = %q; want %q", meta.Durable.Profile, "work")
	}
	if meta.Durable.Harness != "copilot" {
		t.Fatalf("harness = %q; want %q", meta.Durable.Harness, "copilot")
	}
	if !meta.Durable.PipeMode {
		t.Fatal("pipe_mode = false; want true")
	}
	if meta.Durable.ActivityAt == 0 {
		t.Fatal("activity_at = 0; want non-zero")
	}
	if meta.Durable.LastAction != "stop" {
		t.Fatalf("last_action = %q; want %q", meta.Durable.LastAction, "stop")
	}
	if meta.Durable.LastActiveEvent != "PreToolUse" {
		t.Fatalf("last_active_event = %q; want %q", meta.Durable.LastActiveEvent, "PreToolUse")
	}
}

func TestHandler_EventsExport_DurableActiveMetadataSurvivesIdleCleanup(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "PreToolUse",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "claude",
	})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "idle",
		Event:        "TeammateIdle",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "claude",
	})

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	meta := resp.WindowMetadata[1]
	if meta.Durable.LastAction != "active" {
		t.Fatalf("last_action after idle = %q; want active", meta.Durable.LastAction)
	}
	if meta.Durable.LastActiveEvent != "PreToolUse" {
		t.Fatalf("last_active_event after idle = %q; want PreToolUse", meta.Durable.LastActiveEvent)
	}
}

func TestHandler_EventsExport_DurableSubagentCountPreservedOnTerminalCleanup(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart", SessionTitle: "W:api"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart", SessionTitle: "W:api"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "Stop", SessionTitle: "W:api"})

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if got := resp.WindowMetadata[1].Durable.SubagentCount; got != 2 {
		t.Fatalf("subagent_count after terminal cleanup = %d; want 2 (preserve last known in-flight count)", got)
	}
}

func TestHandler_EventsExport_PreservesMetadataAfterClosedWindowGC(t *testing.T) {
	h, fake, _ := newSweepTestHandler(0)
	fake.mu.Lock()
	fake.windows = fakeOSWindowsWithTab(1, "W:api")
	fake.mu.Unlock()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "PreToolUse",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "copilot",
	})

	fake.mu.Lock()
	fake.windows = nil
	fake.mu.Unlock()
	h.SweepStale(time.Now())

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if len(resp.EventsByWindow) != 0 {
		t.Fatalf("EventsByWindow = %+v; want empty after closed-window GC", resp.EventsByWindow)
	}
	meta, ok := resp.WindowMetadata[1]
	if !ok {
		t.Fatalf("WindowMetadata[1] missing after closed-window GC; got %+v", resp.WindowMetadata)
	}
	if meta.Stats.EventCount != 1 {
		t.Fatalf("event_count after closed-window GC = %d; want 1", meta.Stats.EventCount)
	}
	if meta.Durable.SessionTitle != "W:api" || meta.Durable.Profile != "work" || meta.Durable.Harness != "copilot" {
		t.Fatalf("durable metadata after closed-window GC = %+v", meta.Durable)
	}
}
