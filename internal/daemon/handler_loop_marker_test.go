package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestHandler_LoopMarkerPreservesIconWithoutPriorState verifies that an ASL
// lifecycle signal arriving on a fresh window returns IconStatePreserve and
// never introduces a bolt icon.
func TestHandler_LoopMarkerPreservesIconWithoutPriorState(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "loop_marker",
		Event:    "AgentSessionLoopStarted",
		Source:   "agent_session_loop",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
		t.Fatalf("loop_marker: want {ok:true, allowed:true, icon_state:%q}, got %+v", protocol.IconStatePreserve, resp)
	}
}

// TestHandler_LoopMarkerDoesNotDisturbGuardOrLastAction verifies that a
// loop_marker leaves the guard, lastAction, and lastActiveEvent untouched so a
// running stop/key/active state stays the source of truth.
func TestHandler_LoopMarkerDoesNotDisturbGuardOrLastAction(t *testing.T) {
	cases := []struct {
		prevAction string
		event      string
	}{
		{"active", "AgentSessionLoopStarted"},
		{"stop", "AgentSessionLoopTurnStarted"},
		{"permission", "AgentSessionLoopStarted"},
		{"subagent", "AgentSessionLoopTurnStarted"},
	}

	for _, tc := range cases {
		t.Run(tc.prevAction+"_"+tc.event, func(t *testing.T) {
			h := newTestHandler()

			h.Handle(protocol.Request{
				Type:     protocol.TypeStateUpdate,
				WindowID: 1,
				Action:   tc.prevAction,
				Event:    "seed",
			})

			h.stateMu.Lock()
			wantLastAction := h.store[1].LastAction
			wantLastActiveEvent := h.store[1].LastActiveEvent
			h.stateMu.Unlock()
			wantGuard := h.guard.Current(1)

			resp := h.Handle(protocol.Request{
				Type:     protocol.TypeStateUpdate,
				WindowID: 1,
				Action:   "loop_marker",
				Event:    tc.event,
				Source:   "agent_session_loop",
			})
			if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
				t.Fatalf("loop_marker after %q: want allowed preserve, got %+v", tc.prevAction, resp)
			}

			if gotGuard := h.guard.Current(1); gotGuard != wantGuard {
				t.Fatalf("guard.Current after loop_marker = %q, want %q (untouched)", gotGuard, wantGuard)
			}

			h.stateMu.Lock()
			gotLastAction := h.store[1].LastAction
			gotLastActiveEvent := h.store[1].LastActiveEvent
			h.stateMu.Unlock()
			if gotLastAction != wantLastAction {
				t.Fatalf("lastAction after loop_marker = %q, want %q (untouched)", gotLastAction, wantLastAction)
			}
			if gotLastActiveEvent != wantLastActiveEvent {
				t.Fatalf("lastActiveEvent after loop_marker = %q, want %q (untouched)", gotLastActiveEvent, wantLastActiveEvent)
			}
		})
	}
}

// TestHandler_LoopMarkerAppendsRingBufferEntry verifies that loop_marker still
// records a SessionEvent with ResultIconState=preserve for inspectability.
func TestHandler_LoopMarkerAppendsRingBufferEntry(t *testing.T) {
	h := newTestHandler()

	h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "loop_marker",
		Event:    "AgentSessionLoopTurnStarted",
		Source:   "agent_session_loop",
		Reason:   "turn_started",
	})

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 1})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d; want 1 (%+v)", len(resp.Events), resp.Events)
	}
	ev := resp.Events[0]
	if ev.Suppressed {
		t.Errorf("event suppressed=true; want false")
	}
	if ev.Action != "loop_marker" || ev.Event != "AgentSessionLoopTurnStarted" {
		t.Errorf("event = %+v; want action=loop_marker event=AgentSessionLoopTurnStarted", ev)
	}
	if ev.ResultIconState != protocol.IconStatePreserve {
		t.Errorf("event result_icon_state = %q; want %q", ev.ResultIconState, protocol.IconStatePreserve)
	}
	if ev.Source != "agent_session_loop" {
		t.Errorf("event source = %q; want agent_session_loop", ev.Source)
	}
}

// TestHandler_LoopMarkerSuppressesNotifications verifies that no notification
// fires for an ASL lifecycle signal, even when the notifier is wired for stop
// and permission events. The marker is silent by design.
func TestHandler_LoopMarkerSuppressesNotifications(t *testing.T) {
	cfg := &config.NotificationConfig{Stop: true, Permission: true}
	notifier := notification.NewNotifier(cfg)
	var mu sync.Mutex
	var calls []string
	notifier.SetBackend(func(_ context.Context, _ int, _, _, subtitle string) (bool, error) {
		mu.Lock()
		calls = append(calls, subtitle)
		mu.Unlock()
		return false, nil
	})
	h := NewHandler(activity.NewStateGuard(time.Hour), notifier)

	resp := h.Handle(protocol.Request{
		Type:         protocol.TypeStateUpdate,
		WindowID:     1,
		Action:       "loop_marker",
		Event:        "AgentSessionLoopStarted",
		Source:       "agent_session_loop",
		SessionTitle: "W:api",
		Account:      "work",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
		t.Fatalf("loop_marker: want allowed preserve, got %+v", resp)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("loop_marker fired notifications: %v", calls)
	}
}

// TestHandler_LoopMarkerLeavesStaleTrackingUntouched verifies that the
// notifiedStale and contextLimitNotified per-window latches survive a
// loop_marker so a fresh real event can still re-arm them.
func TestHandler_LoopMarkerLeavesStaleTrackingUntouched(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.ensureWindowStateLocked(1).NotifiedStale = true
	h.ensureWindowStateLocked(1).ContextLimitNotified = true
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "loop_marker",
		Event:    "AgentSessionLoopStarted",
		Source:   "agent_session_loop",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
		t.Fatalf("loop_marker: want allowed preserve, got %+v", resp)
	}

	h.stateMu.Lock()
	stillStale := h.store[1].NotifiedStale
	stillContextLimit := h.store[1].ContextLimitNotified
	h.stateMu.Unlock()
	if !stillStale {
		t.Fatal("loop_marker cleared notifiedStale latch; want untouched")
	}
	if !stillContextLimit {
		t.Fatal("loop_marker cleared contextLimitNotified latch; want untouched")
	}
}

// TestHandler_LoopMarkerArmsHookSilenceLatch verifies that a loop_marker for a
// profile resets the hook-silence latch so the profile is not flagged as silent
// while ASL is actively driving the loop.
func TestHandler_LoopMarkerArmsHookSilenceLatch(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.notifiedHookSilence["work"] = true
	h.stateMu.Unlock()

	before := time.Now()
	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "loop_marker",
		Event:    "AgentSessionLoopStarted",
		Account:  "Work",
		Source:   "agent_session_loop",
	})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("loop_marker: want allowed, got %+v", resp)
	}

	h.stateMu.Lock()
	stillLatched := h.notifiedHookSilence["work"]
	seenAt, hasSeen := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()
	if stillLatched {
		t.Fatal("loop_marker did not reset notifiedHookSilence for the profile")
	}
	if !hasSeen || seenAt.Before(before) {
		t.Fatalf("lastSeenEventAt[work] = %v; want >= %v", seenAt, before)
	}
}

// TestHandler_LoopMarkerAfterActiveLeavesBoltClearable verifies that a
// loop_marker arriving after a real "active" event still lets a follow-up
// "idle" clear the bolt, because the marker did not overwrite lastAction or
// the guard.
func TestHandler_LoopMarkerAfterActiveLeavesBoltClearable(t *testing.T) {
	h := newTestHandler()

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "UserPromptSubmit", IsPrompt: true})

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "loop_marker",
		Event:    "AgentSessionLoopTurnStarted",
		Source:   "agent_session_loop",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
		t.Fatalf("loop_marker after active: want allowed preserve, got %+v", resp)
	}

	resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "idle", Event: "AgentSessionLoopFinished"})
	if !resp.OK || !resp.Allowed || resp.IconState != "" {
		t.Fatalf("idle after loop_marker over active: want allowed empty icon, got %+v", resp)
	}
}
