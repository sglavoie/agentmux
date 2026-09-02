package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeClaudeContextLimit_NotifiesOncePerEpisode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newClaudeContextLimitProbeTestHandler()
		fake.windows = codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		})
		fake.getText = map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		}

		now := time.Now()
		h.ProbeClaudeContextLimit(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("first probe dispatches = %d; want 1", got)
		}
		got := rec.snapshot()[0]
		if got.windowID != 42 {
			t.Errorf("dispatch windowID = %d; want 42", got.windowID)
		}
		if got.account != "work" {
			t.Errorf("dispatch account = %q; want work", got.account)
		}
		if got.title != "api" {
			t.Errorf("dispatch title = %q; want api", got.title)
		}
		if got.subtitle != "Session stopped (failure)" {
			t.Errorf("dispatch subtitle = %q; want %q", got.subtitle, "Session stopped (failure)")
		}

		// Second tick within the same stall episode must not fire again.
		fake.mu.Lock()
		fake.windows[0].Tabs[0].Title = palette.BuildTitle("api", "error", false)
		fake.mu.Unlock()
		h.ProbeClaudeContextLimit(now.Add(2 * time.Second))
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("second probe total dispatches = %d; want 1 (dedup should suppress)", got)
		}
	})
}

// TestProbeClaudeContextLimit_RecoveryReArmsNotifier verifies that once an
// active-class hook event lands on a window currently flagged as
// context-limit-notified, the dedup flag is cleared and a subsequent
// stall fires a fresh notification.
func TestProbeClaudeContextLimit_RecoveryReArmsNotifier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newClaudeContextLimitProbeTestHandler()
		fake.windows = codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		})
		fake.getText = map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		}

		now := time.Now()
		h.ProbeClaudeContextLimit(now)
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("first stall dispatches = %d; want 1", got)
		}

		// In production the stop_failure guard expires after
		// defaults.guard_duration (~1s) before the next hook event lands. The
		// test handler uses a 1h guard duration to avoid timing flakiness, so
		// clear the guard explicitly to simulate that natural expiry. Without
		// this the priority-4 stop_failure guard would block the priority-1
		// active and the recovery branch would never run.
		h.guard.Clear(42)

		// User runs /compact or /clear; Claude resumes and a new hook event
		// for the same window lands with action=active. That should clear
		// contextLimitNotified so a future stall can re-notify.
		resp := h.Handle(protocol.Request{
			Type:         protocol.TypeStateUpdate,
			WindowID:     42,
			Action:       "active",
			Event:        "UserPromptSubmit",
			IsPrompt:     true,
			Account:      "work",
			Harness:      "claude",
			SessionTitle: "api",
		})
		if !resp.OK {
			t.Fatalf("active state update failed: %s", resp.Error)
		}

		h.stateMu.Lock()
		notified := h.store[42].ContextLimitNotified
		h.stateMu.Unlock()
		if notified {
			t.Fatalf("contextLimitNotified[42] = true after active recovery; want false")
		}

		// Simulate the tab title flipping back to bolt as Claude resumes.
		fake.mu.Lock()
		fake.windows[0].Tabs[0].Title = palette.BuildTitle("api", "bolt", false)
		fake.mu.Unlock()

		// Advance past the notifier's 10s per-(window, action) cooldown so the
		// second dispatch is not silently dropped by the cooldown map.
		time.Sleep(11 * time.Second)
		h.ProbeClaudeContextLimit(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 2 {
			t.Fatalf("second stall dispatches = %d; want 2 (re-arm should fire fresh notification)", got)
		}
	})
}

// TestProbeClaudeContextLimit_UserClearReArmsNotifier verifies that an
// explicit user_clear action through the TUI also resets the dedup flag,
// mirroring the terminal/cleanup branch in handler_state_update.go.
func TestProbeClaudeContextLimit_UserClearReArmsNotifier(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	h.stateMu.Lock()
	notified := h.store[42].ContextLimitNotified
	h.stateMu.Unlock()
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false after probe; want true")
	}

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 42,
		Action:   "user_clear",
		Account:  "work",
		Harness:  "claude",
	})
	if !resp.OK {
		t.Fatalf("user_clear state update failed: %s", resp.Error)
	}

	h.stateMu.Lock()
	notified = h.store[42].ContextLimitNotified
	h.stateMu.Unlock()
	if notified {
		t.Fatalf("contextLimitNotified[42] = true after user_clear; want false")
	}
}

// TestProbeClaudeContextLimit_ClosedWindowGCDropsFlag verifies that when
// pruneClosedWindows runs against a live-set that no longer contains the
// probed window, the contextLimitNotified entry is GC'd alongside the rest
// of the per-window state.
func TestProbeClaudeContextLimit_ClosedWindowGCDropsFlag(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	h.stateMu.Lock()
	notified := h.store[42].ContextLimitNotified
	h.stateMu.Unlock()
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false after probe; want true")
	}

	// Kitty no longer reports window 42 → pruneClosedWindows should drop it.
	h.pruneClosedWindows(time.Now(), map[int]bool{})

	h.stateMu.Lock()
	_, stillThere := h.store[42]
	h.stateMu.Unlock()
	if stillThere {
		t.Fatalf("contextLimitNotified[42] survived pruneClosedWindows; want GC'd")
	}
}
