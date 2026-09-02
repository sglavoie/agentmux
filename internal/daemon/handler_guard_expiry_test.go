package daemon

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// The reducer is authoritative for the visible state: a terminal stop/failure
// suppresses trailing non-prompt activity only within the guard-duration
// grace (for unidentified legacy sources), after which activity is
// authoritative recovery. These tests pin the three regimes — within-grace
// suppression, post-grace self-heal, and prompt bypass — replacing the old
// contract where a terminal state blocked non-prompt activity indefinitely.

// TestHandler_ActiveSuppressedWithinTerminalGrace verifies that a non-prompt
// "active" event (e.g. PostToolBatch arriving late due to async execution)
// cannot revive the bolt while the terminal grace is still running, for both
// stop and stop_failure. An explicit prompt event breaks through immediately.
func TestHandler_ActiveSuppressedWithinTerminalGrace(t *testing.T) {
	for _, terminal := range []string{"stop", "stop_failure"} {
		t.Run(terminal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := NewStateGuard(time.Hour) // grace mirrors the guard: 3600s
				notifier := notification.NewNotifier(&config.NotificationConfig{})
				notifier.SetBackend(func(_ context.Context, _ int, _, _, _ string) (bool, error) {
					return false, nil
				})
				h := NewHandler(guard, notifier)

				h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: terminal})
				wantIcon := state.ActionIcon(terminal)

				// Non-prompt active within the grace must not revive the bolt.
				resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active"})
				if !resp.OK {
					t.Fatalf("non-prompt active after %s: %+v", terminal, resp)
				}
				if eff, icon := windowEffective(t, h, 1); eff.Icon != wantIcon || icon != wantIcon {
					t.Fatalf("visible state after suppressed active = %+v/%q, want %q", eff, icon, wantIcon)
				}

				// A follow-up idle must also preserve the terminal icon.
				h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "idle"})
				if eff, icon := windowEffective(t, h, 1); eff.Icon != wantIcon || icon != wantIcon {
					t.Fatalf("visible state after idle = %+v/%q, want %q", eff, icon, wantIcon)
				}

				// A prompt event (UserPromptSubmit) breaks through immediately.
				// The legacy guard's prompt bypass is string-exact on "stop", so
				// only the authoritative visible state is asserted here — for
				// stop_failure the legacy response still reports blocked while
				// the reducer applies the recovery.
				resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})
				if !resp.OK {
					t.Fatalf("prompt active after %s: %+v", terminal, resp)
				}
				if eff, icon := windowEffective(t, h, 1); eff.Icon != "bolt" || icon != "bolt" {
					t.Fatalf("visible state after prompt = %+v/%q, want bolt", eff, icon)
				}
			})
		})
	}
}

// TestHandler_ActiveSelfHealsAfterTerminalGraceExpiry verifies that a
// non-prompt "active" event arriving after the terminal grace has elapsed is
// authoritative recovery: later tool events can no longer remain suppressed
// indefinitely once the trailing-completion window has passed.
func TestHandler_ActiveSelfHealsAfterTerminalGraceExpiry(t *testing.T) {
	for _, terminal := range []string{"stop", "stop_failure"} {
		t.Run(terminal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := NewStateGuard(time.Second) // grace mirrors the guard: 1s
				notifier := notification.NewNotifier(&config.NotificationConfig{})
				notifier.SetBackend(func(_ context.Context, _ int, _, _, _ string) (bool, error) {
					return false, nil
				})
				h := NewHandler(guard, notifier)

				h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: terminal})
				time.Sleep(2 * time.Second) // let both the guard and the grace expire

				resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active"})
				if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
					t.Fatalf("post-grace active after %s: want icon_state=bolt, got %+v", terminal, resp)
				}
				if eff, icon := windowEffective(t, h, 1); eff.Icon != "bolt" || icon != "bolt" {
					t.Fatalf("visible state after post-grace active = %+v/%q, want bolt", eff, icon)
				}
			})
		})
	}
}

// restoreV1State runs a schema-v1 amx_state payload (legacy scalar, no facts)
// through the real restore path so the migrated fact lands on the window's
// live legacy source key.
func restoreV1State(t *testing.T, h *Handler, windowID int, action string, at time.Time) {
	t.Helper()
	payload := fmt.Sprintf(`{"a":%q,"t":%d}`, action, at.Unix())
	h.restoreFromUserVars([]kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			Windows: []kitty.Window{{
				ID:       windowID,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: payload},
			}},
		}},
	}}, at)
}

// TestHandler_RestoredStopSuppressesTrailingActiveWithinGrace pins that a
// daemon restart preserving a ⏹ state reconstructs a terminal fact on the
// window's own legacy source, so a benign non-prompt "active" arriving within
// the grace cannot clear the unacknowledged stop — while a prompt still can.
func TestHandler_RestoredStopSuppressesTrailingActiveWithinGrace(t *testing.T) {
	h := newTestHandler() // 1h guard → 3600s grace
	restoreV1State(t, h, 1, "stop", time.Now())

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active"})
	if eff, icon := windowEffective(t, h, 1); eff.Icon != "stop" || icon != "stop" {
		t.Fatalf("visible state after restored-stop active = %+v/%q, want stop", eff, icon)
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("prompt active with restored stop: want icon_state=bolt, got %+v", resp)
	}
	if eff, icon := windowEffective(t, h, 1); eff.Icon != "bolt" || icon != "bolt" {
		t.Fatalf("visible state after prompt = %+v/%q, want bolt", eff, icon)
	}
}

func TestHandler_ActiveAllowedByCurrentWarningIconAfterDaemonRestart(t *testing.T) {
	h := newTestHandler()
	restoreV1State(t, h, 1, "failure", time.Now())

	resp := h.Handle(protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 1,
		Action:   "active",
	})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("non-prompt active with current warning icon: want icon_state=bolt allowed=true, got %+v", resp)
	}
	if eff, icon := windowEffective(t, h, 1); eff.Icon != "bolt" || icon != "bolt" {
		t.Fatalf("visible state after active over warning = %+v/%q, want bolt", eff, icon)
	}
}

// TestHandler_RestoredErrorStaysStickyUntilPromptRecovery pins that a fresh
// daemon restart preserving the ❗ glyph reconstructs a terminal stop_failure
// fact, so within the grace a benign non-prompt "active" cannot clear the
// unacknowledged failure. A prompt is authoritative recovery.
func TestHandler_RestoredErrorStaysStickyUntilPromptRecovery(t *testing.T) {
	h := newTestHandler()
	restoreV1State(t, h, 1, "stop_failure", time.Now())

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active"})
	if eff, icon := windowEffective(t, h, 1); eff.Icon != "error" || icon != "error" {
		t.Fatalf("visible state after restored-error active = %+v/%q, want error", eff, icon)
	}

	// An idle sweep must not clear the sticky failure either.
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "idle"})
	if eff, icon := windowEffective(t, h, 1); eff.Icon != "error" || icon != "error" {
		t.Fatalf("visible state after idle = %+v/%q, want error", eff, icon)
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", IsPrompt: true})
	if !resp.OK || !resp.Allowed || resp.IconState != "bolt" {
		t.Fatalf("prompt active with restored error: want icon_state=bolt, got %+v", resp)
	}
	if eff, icon := windowEffective(t, h, 1); eff.Icon != "bolt" || icon != "bolt" {
		t.Fatalf("visible state after prompt = %+v/%q, want bolt", eff, icon)
	}
}
