package daemon

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestHandler_StateUpdateAllowed(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active"})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("want {ok:true, allowed:true, icon_state:bolt}, got %+v", resp)
	}
}

func TestHandler_StateUpdateBlocked(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "failure"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "permission"})
	if !resp.OK || resp.Allowed {
		t.Fatalf("want {ok:true, allowed:false}, got %+v", resp)
	}
}

func TestHandler_AllActionIconStates(t *testing.T) {
	cases := []struct {
		action    string
		iconState string
	}{
		{"active", "bolt"},
		{"compact", "compact"},
		{"subagent", "subagent"},
		{"stop", "stop"},
		{"permission", "key"},
		{"failure", "warning"},
		{"stop_failure", "error"},
		{"elicitation", "question"},
		// idle and clear with no prior guard preserve whatever icon is already
		// in the title (nothing to clear).
		{"idle", protocol.IconStatePreserve},
		{"clear", protocol.IconStatePreserve},
		// user_clear is the TUI-initiated true wipe — icon goes empty.
		{"user_clear", ""},
		// loop_marker never paints an icon: ASL lifecycle signals are
		// marker-only and must preserve whatever the user is currently seeing.
		{"loop_marker", protocol.IconStatePreserve},
	}
	for _, tc := range cases {
		h := newTestHandler()
		resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: tc.action})
		if !resp.OK || !resp.Allowed || resp.IconState != tc.iconState {
			t.Errorf("action %q: want icon_state=%q, got %+v", tc.action, tc.iconState, resp)
		}
	}
}

// TestHandler_IdleClearsActiveIcon verifies that an idle event clears the bolt
// when the previous guard was active/subagent/compact, but preserves stop/
// permission/failure icons (the user still needs to see them).
// Both the within-guard-window case (prevGuard set) and the expired-guard case
// (lastAction fallback) are covered.
func TestHandler_IdleClearsActiveIcon(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cases := []struct {
			prev      string
			wantIcon  string
			wantLabel string
		}{
			{"active", "", "idle after active clears bolt"},
			{"subagent", "", "idle after subagent clears icon"},
			{"compact", "", "idle after compact clears icon"},
			{"stop", protocol.IconStatePreserve, "idle after stop preserves stop icon"},
			{"permission", protocol.IconStatePreserve, "idle after permission preserves key icon"},
			{"failure", protocol.IconStatePreserve, "idle after failure preserves warning icon"},
			{"stop_failure", protocol.IconStatePreserve, "idle after stop failure preserves error icon"},
			{"elicitation", protocol.IconStatePreserve, "idle after elicitation preserves question icon"},
		}
		// Test with a long guard (prevGuard still set when idle arrives).
		for _, tc := range cases {
			h := newTestHandler()
			h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: tc.prev})
			resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "idle"})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
				t.Errorf("%s (guard active): want icon_state=%q, got %+v", tc.wantLabel, tc.wantIcon, resp)
			}
		}

		// Test with an expired guard (idle arrives after guard_duration; must use
		// lastAction fallback — this was the stale-bolt bug on Claude sessions).
		for _, tc := range cases {
			guard := NewStateGuard(time.Nanosecond) // expires immediately
			notifier := notification.NewNotifier(&config.NotificationConfig{})
			notifier.SetBackend(func(_ context.Context, _ int, _, _, _ string) (bool, error) {
				return false, nil
			})
			h := NewHandler(guard, notifier)

			h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: tc.prev})
			time.Sleep(5 * time.Millisecond) // let the guard expire
			resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "idle"})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
				t.Errorf("%s (guard expired): want icon_state=%q, got %+v", tc.wantLabel, tc.wantIcon, resp)
			}
		}
	})
}

func TestHandler_LoopFinishedClearsActiveClassOnly(t *testing.T) {
	cases := []struct {
		prev     string
		wantIcon string
	}{
		{prev: "active", wantIcon: ""},
		{prev: "subagent", wantIcon: ""},
		{prev: "compact", wantIcon: ""},
		{prev: "stop", wantIcon: protocol.IconStatePreserve},
		{prev: "permission", wantIcon: protocol.IconStatePreserve},
		{prev: "failure", wantIcon: protocol.IconStatePreserve},
		{prev: "stop_failure", wantIcon: protocol.IconStatePreserve},
		{prev: "elicitation", wantIcon: protocol.IconStatePreserve},
	}
	for _, tc := range cases {
		t.Run(tc.prev, func(t *testing.T) {
			h := newTestHandler()
			h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: tc.prev})

			resp := h.Handle(protocol.Request{
				Type:     protocol.TypeStateUpdate,
				WindowID: 1,
				Action:   "idle",
				Event:    "AgentSessionLoopFinished",
				Source:   "agent_session_loop",
				Reason:   "run_finished",
			})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
				t.Fatalf("AgentSessionLoopFinished after %q: want icon_state=%q, got %+v", tc.prev, tc.wantIcon, resp)
			}
		})
	}
}

func TestHandler_CleanupUsesRestoredDaemonState(t *testing.T) {
	cases := []struct {
		name     string
		cleanup  string
		current  string
		harness  string
		wantIcon string
	}{
		{"idle clears stale bolt", "idle", "bolt", "", ""},
		{"idle clears stale compact", "idle", "compact", "", ""},
		{"idle promotes stale Copilot compact to stop", "idle", "compact", "copilot", "stop"},
		{"clear promotes stale Copilot compact to stop", "clear", "compact", "copilot", "stop"},
		{"idle clears stale subagent", "idle", "subagent", "", ""},
		{"clear clears stale bolt", "clear", "bolt", "", ""},
		{"clear preserves stop icon", "clear", "stop", "", protocol.IconStatePreserve},
		{"idle preserves permission icon", "idle", "key", "", protocol.IconStatePreserve},
		{"idle preserves warning icon", "idle", "warning", "", protocol.IconStatePreserve},
		{"idle preserves error icon", "idle", "error", "", protocol.IconStatePreserve},
		{"idle preserves elicitation icon", "idle", "question", "", protocol.IconStatePreserve},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			h.stateMu.Lock()
			h.store[1] = &state.WindowState{LastAction: actionForIconStateForTest(tc.current), DesiredIcon: tc.current}
			h.stateMu.Unlock()
			resp := h.Handle(protocol.Request{
				Type:     protocol.TypeStateUpdate,
				WindowID: 1,
				Action:   tc.cleanup,
				Harness:  tc.harness,
			})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIcon {
				t.Fatalf("want icon_state=%q, got %+v", tc.wantIcon, resp)
			}
		})
	}
}

func actionForIconStateForTest(iconState string) string {
	return map[string]string{
		"bolt":     "active",
		"compact":  "compact",
		"subagent": "subagent",
		"stop":     "stop",
		"key":      "permission",
		"warning":  "failure",
		"error":    "stop_failure",
		"question": "elicitation",
	}[iconState]
}

func TestHandler_StateUpdateRecordsActivity(t *testing.T) {
	h := newTestHandler()
	before := time.Now()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active"})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("want allowed update, got %+v", resp)
	}

	h.stateMu.Lock()
	st, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatalf("expected activity[42] to be recorded")
	}
	got := st.Activity
	if got.Before(before) {
		t.Fatalf("activity time %v is before request time %v", got, before)
	}
}

func TestHandler_StateUpdateBlockedDoesNotRecordActivity(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "failure"})

	h.stateMu.Lock()
	h.store[1].Activity = time.Time{}
	h.stateMu.Unlock()

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "permission"})
	if !resp.OK || resp.Allowed {
		t.Fatalf("expected blocked update, got %+v", resp)
	}

	h.stateMu.Lock()
	ok := !h.store[1].Activity.IsZero()
	h.stateMu.Unlock()
	if ok {
		t.Fatalf("blocked update should not record activity")
	}
}

// TestHandler_StateUpdateUsesSnapshotDerivedMute verifies that state updates do
// not affect the daemon's Kitty-derived mute cache.
func TestHandler_StateUpdateUsesSnapshotDerivedMute(t *testing.T) {
	h := newTestHandler()

	if h.IsMuted(42) {
		t.Fatal("window should default to unmuted")
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active"})
	if !resp.OK || !resp.Allowed {
		t.Fatalf("muted state update should still be allowed, got %+v", resp)
	}
	if h.IsMuted(42) {
		t.Fatal("state updates must not populate the mute cache")
	}

	h.setMutedFromSnapshot(42, true)
	if !h.IsMuted(42) {
		t.Fatal("snapshot-derived mute state should populate the cache")
	}

	h.setMutedFromSnapshot(42, false)
	if h.IsMuted(42) {
		t.Fatal("snapshot-derived unmute should clear the cache")
	}
}

// TestHandler_AutoCompactResumeKeepsBolt covers the PostCompact matcher split
// for automatic mid-turn compaction: PostCompactAuto resolves to "active", so
// the bolt must return after the compact icon (same-priority replacement), no
// "Session stopped" notification may fire while Claude is still working, and
// the active-after-terminal barrier must not arm against later tool events.
func TestHandler_AutoCompactResumeKeepsBolt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)

		resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "compact", Event: "PreCompact"})
		if !resp.OK || !resp.Allowed || resp.IconState != "compact" {
			t.Fatalf("PreCompact: want compact icon, got %+v", resp)
		}

		resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostCompactAuto"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("PostCompactAuto: want bolt restored, got %+v", resp)
		}

		resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})
		if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
			t.Fatalf("PostToolUse after auto compact: want bolt without active-after-terminal suppression, got %+v", resp)
		}

		synctest.Wait()
		if got := rec.len(); got != 0 {
			t.Fatalf("auto compact fired %d notifications, want 0: %+v", got, rec.snapshot())
		}
	})
}

// TestHandler_ManualCompactStopPersists covers the manual half of the
// PostCompact matcher split: PostCompactManual resolves to "stop", so the ⏹
// icon must land, notify, and persist — the active-after-terminal barrier
// suppresses follow-up non-prompt active events until the next user prompt.
func TestHandler_ManualCompactStopPersists(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)

		h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "compact", Event: "PreCompact"})
		resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "stop", Event: "PostCompactManual"})
		if !resp.OK || !resp.Allowed || resp.IconState != "stop" {
			t.Fatalf("PostCompactManual: want stop icon, got %+v", resp)
		}

		resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "PostToolUse"})
		if !resp.OK || resp.Allowed {
			t.Fatalf("PostToolUse after manual compact: want active-after-terminal suppression, got %+v", resp)
		}

		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("manual compact fired %d notifications, want 1 stop notification: %+v", got, rec.snapshot())
		}
	})
}
