package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestHandler_HookEventAgentSessionLoopStartedTargetsWorkspace(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop"}),
		[]kitty.Window{
			{
				ID: 42,
				UserVars: kitty.UserVars{
					Profile:      "api",
					Harness:      "claude",
					AMXDirectory: projectDir,
				},
			},
			{ID: 43},
		},
	)}
	h.SetKittyClient(fake)

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeHookEvent,
		WindowID: 43,
		Event:    "AgentSessionLoopStarted",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source:        "agent_session_loop",
			CWD:           projectDir,
			Harness:       "claude",
			KittyWindowID: 43,
		}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Action != "loop_marker" {
		t.Fatalf("events = %+v; want loop_marker on project pane", detail.Events)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || !st.Looping || !st.LoopingSet || st.DesiredIcon != "" {
		t.Fatalf("state = %+v; want loop marker true without changing stop icon", st)
	}
}

// TestHandler_HookEventLoopLifecycleTargetsActiveTitleOwner verifies that loop
// lifecycle fan-out lands on the window that actually owns the tab title (the
// active managed pane) rather than the first managed window in Kitty order. Two
// managed panes share a workspace, but only the active title owner should carry
// the loop marker.
func TestHandler_HookEventLoopLifecycleTargetsActiveTitleOwner(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	h := newTestHandler()
	// Window 42 comes first in Kitty order but is inactive; window 44 is the
	// active managed pane, so it owns the tab title. With no window id in the
	// payload, matching falls to CWD and both managed panes qualify — the fan-out
	// must still resolve to the active owner (44), not the first managed (42).
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop"}),
		[]kitty.Window{
			{
				ID:       42,
				IsActive: false,
				UserVars: kitty.UserVars{Profile: "api", Harness: "claude", AMXDirectory: projectDir},
			},
			{
				ID:       44,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "api", Harness: "claude", AMXDirectory: projectDir},
			},
		},
	)}
	h.SetKittyClient(fake)

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeHookEvent,
		WindowID: 44,
		Event:    "AgentSessionLoopStarted",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source:  "agent_session_loop",
			CWD:     projectDir,
			Harness: "claude",
		}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}

	h.stateMu.Lock()
	owner := h.store[44]
	first := h.store[42]
	h.stateMu.Unlock()
	if owner == nil || !owner.Looping || !owner.LoopingSet {
		t.Fatalf("active title owner state = %+v; want loop marker on window 44", owner)
	}
	if first != nil && (first.Looping || first.LoopingSet) {
		t.Fatalf("first managed window state = %+v; loop marker must not land on window 42", first)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 44})
	if len(detail.Events) != 1 || detail.Events[0].Action != "loop_marker" {
		t.Fatalf("owner events = %+v; want loop_marker on the active title owner", detail.Events)
	}
}

func TestHandler_HookEventAgentSessionLoopFinishedClearsLoopMarker(t *testing.T) {
	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, nil, nil)

	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop", Looping: true}),
		[]kitty.Window{
			{
				ID: 42,
				UserVars: kitty.UserVars{
					Profile:      "api",
					Harness:      "claude",
					AMXDirectory: projectDir,
				},
			},
			{ID: 43},
		},
	)}
	h.SetKittyClient(fake)
	h.stateMu.Lock()
	h.ensureWindowStateLocked(42).Looping = true
	h.ensureWindowStateLocked(42).LoopingSet = true
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeHookEvent,
		WindowID: 43,
		Event:    "AgentSessionLoopFinished",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source:        "agent_session_loop",
			Reason:        "run_finished",
			CWD:           projectDir,
			Harness:       "claude",
			KittyWindowID: 43,
		}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Action != "idle" || detail.Events[0].Event != "AgentSessionLoopFinished" {
		t.Fatalf("events = %+v; want idle AgentSessionLoopFinished on project pane", detail.Events)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.Looping || !st.LoopingSet {
		t.Fatalf("state = %+v; want loop marker cleared", st)
	}
}

func TestHandler_HookEventDefersResetSensitiveStop(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 40 * time.Millisecond
	t.Cleanup(func() {
		resetFlowStopDelay = oldDelay
	})

	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		h.SetStopConfirm(0, 0, false) // exercise the reset hold class, not the terminal one
		fake := &fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
		}})}
		h.SetKittyClient(fake)

		resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "Stop"})
		if !resp.OK {
			t.Fatalf("Stop response = %+v", resp)
		}
		if detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42}); len(detail.Events) != 0 {
			t.Fatalf("events before deferred timer = %+v; want none", detail.Events)
		}

		time.Sleep(resetFlowStopDelay / 4)
		resp = h.Handle(protocol.Request{
			Type:        protocol.TypeHookEvent,
			WindowID:    42,
			Event:       "SessionEnd",
			PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{Reason: "clear"}),
		})
		if !resp.OK {
			t.Fatalf("SessionEnd response = %+v", resp)
		}

		time.Sleep(resetFlowStopDelay)
		synctest.Wait()

		detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if len(detail.Events) != 2 {
			t.Fatalf("events = %d, want 2 (%+v)", len(detail.Events), detail.Events)
		}
		for _, ev := range detail.Events {
			if ev.Action != "user_clear" || !ev.ResetFlow || ev.ResultIconState != "" {
				t.Fatalf("event = %+v; reset-sensitive stop should be rewritten to user_clear", ev)
			}
		}
	})
}

// TestHandler_HookEventDeferredResetStopSupersededByPromptAndClear covers the
// ordering hazard where a Stop is still held in the reset-defer window when the
// next turn's prompt and an F8 clear both arrive. Both are newer authoritative
// events, so the held stop must be cancelled and must never fire afterward to
// repaint a stale stop icon over the wiped tab.
func TestHandler_HookEventDeferredResetStopSupersededByPromptAndClear(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 60 * time.Millisecond // a real, nonzero debounce interval
	t.Cleanup(func() {
		resetFlowStopDelay = oldDelay
	})

	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		h.SetStopConfirm(0, 0, false) // exercise the reset hold class, not the terminal one
		fake := &fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}})}
		h.SetKittyClient(fake)

		// The Stop enters the reset-defer window rather than painting immediately.
		if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
			t.Fatalf("Stop response = %+v", resp)
		}
		// Advance less than the debounce so the stop is still pending.
		time.Sleep(resetFlowStopDelay / 3)

		// A fresh prompt begins the next turn and must cancel the pending stop.
		if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit"}); !resp.OK {
			t.Fatalf("UserPromptSubmit response = %+v", resp)
		}
		// F8 then wipes the tab, still before the original stop timer fires.
		if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"}); !resp.OK {
			t.Fatalf("user_clear response = %+v", resp)
		}

		// Let the original debounce fully elapse; the superseded stop must not fire.
		time.Sleep(resetFlowStopDelay)
		synctest.Wait()

		h.stateMu.Lock()
		st := h.store[42]
		h.stateMu.Unlock()
		if st == nil || st.DesiredIcon != "" {
			t.Fatalf("state = %+v; want cleared icon after prompt+F8 supersede the deferred stop", st)
		}
		detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		for _, ev := range detail.Events {
			if ev.ResultIconState == "stop" {
				t.Fatalf("events = %+v; the deferred stop must not fire after being superseded", detail.Events)
			}
		}
	})
}
