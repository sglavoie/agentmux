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
}

// TestHandler_EventLog_AppendOnCopilotMidSubagentSuppression confirms that the
// Copilot agentStop-during-subagent gate also records its suppression — without
// this, the user has no signal in the detail pane explaining why the bolt did
// not remain under the parent subagent icon.
func TestHandler_EventLog_AppendOnCopilotMidSubagentSuppression(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "subagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "agentStop", PreservesIcon: true})

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
