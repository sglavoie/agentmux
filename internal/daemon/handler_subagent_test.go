package daemon

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestHandler_SubagentReferenceCount verifies that the robot emoji persists
// while any sub-agents are still running and clears when the last one stops.
func TestHandler_SubagentReferenceCount(t *testing.T) {
	t.Run("single agent start then stop shows then clears robot", func(t *testing.T) {
		h := newTestHandler()

		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
		if !resp.OK || !resp.Allowed || resp.IconState != "subagent" {
			t.Fatalf("SubagentStart: want icon_state=subagent, got %+v", resp)
		}

		// SubagentStop with count=1 → count hits 0 → guard cleared → "active" allowed.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("SubagentStop (last): want icon_state=bolt, got %+v", resp)
		}
	})

	t.Run("two concurrent agents: first stop suppressed, second allows transition", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

		// First SubagentStop: count 2→1, still running — must be suppressed.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("first SubagentStop (count=1): want allowed=false, got %+v", resp)
		}

		// Second SubagentStop: count 1→0, last agent done — must be allowed.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("second SubagentStop (count=0): want icon_state=bolt, got %+v", resp)
		}
	})

	t.Run("parent active events blocked while agents running", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

		// Parent PreToolUse "active" while subagent is running must be blocked.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("parent active during subagent: want allowed=false, got %+v", resp)
		}

		// After last SubagentStop, parent active events can proceed.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("parent active after last SubagentStop: want icon_state=bolt, got %+v", resp)
		}
	})

	t.Run("SubagentStop without prior start does not clear unrelated guard", func(t *testing.T) {
		h := newTestHandler()

		// Establish a stop_failure guard (priority 4; "active" cannot bypass it).
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop_failure"})

		// SubagentStop with count=0: wasTracked=false, our code must not call
		// guard.Clear(), so stop_failure guard must block the "active".
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		// "active" (priority 1) < "stop_failure" (priority 4) → must be blocked.
		if !resp.OK || resp.Allowed {
			t.Fatalf("spurious SubagentStop must not clear stop_failure guard, got %+v", resp)
		}
	})

	t.Run("stop resets subagent counter", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

		// Session stops — counter must reset.
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop"})

		// After stop, SubagentStop should be treated as untracked (wasTracked=false)
		// and the resulting "active" falls through to the normal priority check.
		// stop (priority 2) still holds the guard, so "active" (priority 1) is blocked.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("after stop resets counter, SubagentStop should not clear stop guard, got %+v", resp)
		}
	})

	t.Run("independent windows have separate counters", func(t *testing.T) {
		h := newTestHandler()

		h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "subagent", Event: "SubagentStart"})

		// Window 1 has count=1; its SubagentStop should be the last.
		resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("window 1 single SubagentStop: want icon_state=bolt, got %+v", resp)
		}

		// Window 2 has count=2; first stop is still suppressed.
		resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "active", Event: "SubagentStop"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("window 2 first SubagentStop: want allowed=false, got %+v", resp)
		}
	})
}

// TestHandler_SubagentCounterRecoversAfterTerminalAction locks in the
// self-healing property of subagentCount: if any SubagentStop events are lost
// (daemon restart, crashed subprocess, dropped IPC frame), a subsequent
// terminal action must reset the counter so future SubagentStop → active
// transitions are not permanently suppressed.
func TestHandler_SubagentCounterRecoversAfterTerminalAction(t *testing.T) {
	h := newTestHandler()

	// Two SubagentStarts → counter=2. The matching SubagentStops never arrive
	// (simulating lost IPC frames).
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

	// Terminal action arrives — counter must reset to 0.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop"})

	// New prompt breaks through the stop guard.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})

	// A fresh subagent starts → counter should now be 1 (not 3 from the stuck state).
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart"})

	// The matching SubagentStop should be treated as the last agent finishing
	// (counter 1→0) and allow the resulting "active" with bolt icon. If the
	// counter had not been reset, this would still be > 0 and suppressed.
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "SubagentStop"})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("active after subagent-counter recovery: want icon_state=bolt, got %+v", resp)
	}
}

// TestHandler_IdleAfterSuppressedActivePreservesStopIcon verifies that when a
// non-prompt "active" event lands within the terminal grace after
// "stop"/"stop_failure" (the reducer suppresses it as a trailing completion),
// a subsequent idle does not clear the terminal icon: idle only withdraws
// active-class facts, so the unacknowledged terminal state keeps rendering.
func TestHandler_IdleAfterSuppressedActivePreservesStopIcon(t *testing.T) {
	cases := []struct {
		name       string
		prevAction string
		wantIcon   string
	}{
		{"stop", "stop", "stop"},
		{"stop_failure", "stop_failure", "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := NewStateGuard(time.Second) // grace mirrors the guard: 1s
				notifier := NewNotifier(&config.NotificationConfig{})
				notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (bool, error) { return false, nil }
				h := NewHandler(guard, notifier)

				h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.prevAction})

				// Non-prompt active within the grace is a trailing completion of
				// the terminated turn — the terminal icon must keep rendering.
				resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active"})
				if !resp.OK {
					t.Fatalf("non-prompt active after %s: %+v", tc.prevAction, resp)
				}
				if eff, icon := windowEffective(t, h, 1); eff.Icon != tc.wantIcon || icon != tc.wantIcon {
					t.Fatalf("visible state after suppressed active = %+v/%q, want %q", eff, icon, tc.wantIcon)
				}

				// A follow-up idle must also preserve the terminal icon, even
				// after the guard and grace have both expired: idle never
				// withdraws attention/terminal facts.
				time.Sleep(2 * time.Second)
				resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
				if !resp.OK || !resp.Allowed {
					t.Fatalf("idle after suppressed active: want allowed=true, got %+v", resp)
				}
				if eff, icon := windowEffective(t, h, 1); eff.Icon != tc.wantIcon || icon != tc.wantIcon {
					t.Fatalf("visible state after idle = %+v/%q, want %q", eff, icon, tc.wantIcon)
				}
			})
		})
	}
}

// TestHandler_SubagentCountCleanedUpAfterLastStop verifies that the per-window
// subagentCount map entry is deleted when the count reaches zero via a normal
// SubagentStop, rather than being left as a zero-valued entry that survives
// until a terminal event. Without the cleanup, a long-running daemon
// processing many subagent cycles across many windows accumulates stale
// entries indefinitely (slow but real memory leak).
func TestHandler_SubagentCountCleanedUpAfterLastStop(t *testing.T) {
	h := newTestHandler()

	for windowID := 1; windowID <= 10; windowID++ {
		h.Handle(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "subagent", Event: "SubagentStart"})
		h.Handle(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "active", Event: "SubagentStop"})
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	var residual int
	for _, st := range h.store {
		if st.SubagentCount != 0 {
			residual++
		}
	}
	if residual != 0 {
		t.Fatalf("subagentCount has %d residual nonzero entries after normal stop cycles; want 0", residual)
	}
}
