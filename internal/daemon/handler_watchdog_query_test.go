package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestHandler_TracksLastSessionTitle verifies that the session title from
// each state_update is recorded so the future stale-active sweep can fall
// back to it when the live kitten lookup fails.
func TestHandler_TracksLastSessionTitle(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", SessionTitle: "W:api"})

	h.stateMu.Lock()
	got := h.store[42].LastSessionTitle
	h.stateMu.Unlock()
	if got != "W:api" {
		t.Fatalf("lastSessionTitle[42] = %q; want %q", got, "W:api")
	}
}

// TestHandler_TracksPipeMode verifies that the pipe-mode flag from each
// state_update is recorded so stale notifications can honor the same
// silence rule used by other notifyable actions.
func TestHandler_TracksPipeMode(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", PipeMode: true})

	h.stateMu.Lock()
	got := h.store[42].LastPipeMode
	h.stateMu.Unlock()
	if !got {
		t.Fatalf("lastPipeMode[42] = false; want true")
	}
}

// TestHandler_NotifiedStaleClearedOnFreshEvent verifies that a fresh real
// event for a window resets the single-shot stale-notification guard so a
// later stall produces a new notification.
func TestHandler_NotifiedStaleClearedOnFreshEvent(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.ensureWindowStateLocked(42).NotifiedStale = true
	h.stateMu.Unlock()

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active"})

	h.stateMu.Lock()
	present := h.store[42].NotifiedStale
	h.stateMu.Unlock()
	if present {
		t.Fatalf("notifiedStale[42] still present after fresh active event")
	}
}

// TestHandler_CleanupOnTerminalAction verifies that terminal actions clear
// selected per-window state fields that should not outlive the session.
func TestHandler_CleanupOnTerminalAction(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.ensureWindowStateLocked(42).NotifiedStale = true
	h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
	h.ensureWindowStateLocked(42).LastPipeMode = true
	h.stateMu.Unlock()

	// "stop" carries no SessionTitle / PipeMode in this test, so the
	// post-cleanup re-write at the activity site leaves notifiedStale empty
	// and lastSessionTitle / lastPipeMode at zero values.
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop"})

	h.stateMu.Lock()
	staleOK := h.store[42].NotifiedStale
	title := h.store[42].LastSessionTitle
	pipe := h.store[42].LastPipeMode
	h.stateMu.Unlock()

	if staleOK {
		t.Fatalf("notifiedStale[42] still present after stop")
	}
	if title != "" {
		t.Fatalf("lastSessionTitle[42] = %q after stop; want empty", title)
	}
	if pipe {
		t.Fatalf("lastPipeMode[42] = true after stop; want false")
	}
}

// TestHandler_TracksLastAccount verifies that handleStateUpdate records the
// account name so the sweep can resolve per-account threshold overrides.
func TestHandler_TracksLastAccount(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	h.stateMu.Lock()
	got := h.store[42].LastAccount
	h.stateMu.Unlock()
	if got != "work" {
		t.Fatalf("lastAccount[42] = %q; want work", got)
	}
}

// TestHandler_LastAccountClearedOnTerminal verifies that terminal actions
// clear the per-window account field.
func TestHandler_LastAccountClearedOnTerminal(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.ensureWindowStateLocked(42).LastAccount = "work"
	h.stateMu.Unlock()

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop"})

	h.stateMu.Lock()
	present := h.store[42].LastAccount != ""
	h.stateMu.Unlock()
	if present {
		t.Fatalf("lastAccount[42] still present after stop")
	}
}

// TestHandler_StateQueryPopulatesWatchdogGlobalDefault verifies that
// handleStateQuery returns the global threshold when no per-account override
// is configured. Both the action name and the resolved threshold are
// included so the TUI can render an age column without re-reading config.
func TestHandler_StateQueryPopulatesWatchdogGlobalDefault(t *testing.T) {
	h := newTestHandler()
	h.SetStaleThresholds(map[string]time.Duration{
		"active":   10 * time.Minute,
		"subagent": 30 * time.Minute,
		"compact":  30 * time.Second,
	}, nil)
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Account: "personal"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	if !resp.OK {
		t.Fatalf("want ok=true, got %+v", resp)
	}
	entry, ok := resp.Watchdog[42]
	if !ok {
		t.Fatalf("Watchdog[42] missing; got %+v", resp.Watchdog)
	}
	if entry.LastAction != "active" {
		t.Errorf("LastAction = %q; want active", entry.LastAction)
	}
	if entry.ThresholdSec != int((10 * time.Minute).Seconds()) {
		t.Errorf("ThresholdSec = %d; want %d (global active)", entry.ThresholdSec, int((10 * time.Minute).Seconds()))
	}
	if entry.LastEventAt == 0 {
		t.Errorf("LastEventAt = 0; want non-zero unix timestamp")
	}
}

// TestHandler_StateQueryWatchdogHonorsPerAccountOverride verifies that the
// per-account override wins over the global default, matching SweepStale's
// resolution logic.
func TestHandler_StateQueryWatchdogHonorsPerAccountOverride(t *testing.T) {
	h := newTestHandler()
	h.SetStaleThresholds(
		map[string]time.Duration{"active": 10 * time.Minute},
		map[string]map[string]time.Duration{
			"work": {"active": 30 * time.Second},
		},
	)
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	entry, ok := resp.Watchdog[42]
	if !ok {
		t.Fatalf("Watchdog[42] missing; got %+v", resp.Watchdog)
	}
	if entry.ThresholdSec != 30 {
		t.Errorf("ThresholdSec = %d; want 30 (per-account override)", entry.ThresholdSec)
	}
}

// TestHandler_StateQueryWatchdogSkipsNonActiveActions verifies that only the
// three sweep-eligible actions (active/subagent/compact) appear in the
// watchdog map. Stop/permission/failure-class windows are tracked in
// lastAction but should not surface here — they don't have an age budget.
func TestHandler_StateQueryWatchdogSkipsNonActiveActions(t *testing.T) {
	h := newTestHandler()
	h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "permission"})
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 43, Action: "failure"})

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	if _, ok := resp.Watchdog[42]; ok {
		t.Errorf("Watchdog[42] should be absent for permission action; got %+v", resp.Watchdog[42])
	}
	if _, ok := resp.Watchdog[43]; ok {
		t.Errorf("Watchdog[43] should be absent for failure action; got %+v", resp.Watchdog[43])
	}
}

// TestHandler_TracksLastSeenEventAt verifies that handleStateUpdate records
// the most recent event timestamp per account. The hook-silence sweep (S6)
// will compare this against the configured threshold.
func TestHandler_TracksLastSeenEventAt(t *testing.T) {
	h := newTestHandler()

	before := time.Now()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})
	after := time.Now()

	h.stateMu.Lock()
	got, present := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()
	if !present {
		t.Fatalf("lastSeenEventAt[work] missing after state update")
	}
	if got.Before(before) || got.After(after) {
		t.Fatalf("lastSeenEventAt[work] = %v; want between %v and %v", got, before, after)
	}
}

// TestHandler_LastSeenEventAtIsolatedPerAccount verifies that an event for
// account A does not touch account B's timestamp. The hook-silence detector
// needs strict per-account isolation to detect single-account hook drift.
func TestHandler_LastSeenEventAtIsolatedPerAccount(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	h.stateMu.Lock()
	workBefore := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()

	// Advance an event for a different account.
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 43, Action: "active", Account: "personal"})

	h.stateMu.Lock()
	workAfter := h.lastSeenEventAt["work"]
	_, personalPresent := h.lastSeenEventAt["personal"]
	h.stateMu.Unlock()

	if !workAfter.Equal(workBefore) {
		t.Errorf("personal event mutated lastSeenEventAt[work]: before=%v after=%v", workBefore, workAfter)
	}
	if !personalPresent {
		t.Errorf("lastSeenEventAt[personal] missing after personal event")
	}
}

// TestHandler_LastSeenEventAtSkippedWhenAccountEmpty verifies that events
// without an account name (e.g. synthetic probes) do not seed the per-account
// map. The map is keyed by real account names only.
func TestHandler_LastSeenEventAtSkippedWhenAccountEmpty(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active"})

	h.stateMu.Lock()
	_, emptyKeyPresent := h.lastSeenEventAt[""]
	mapLen := len(h.lastSeenEventAt)
	h.stateMu.Unlock()

	if emptyKeyPresent {
		t.Errorf("lastSeenEventAt[\"\"] should not be populated for account-less events")
	}
	if mapLen != 0 {
		t.Errorf("lastSeenEventAt has %d entries; want 0 for account-less event", mapLen)
	}
}

// TestHandler_LastSeenEventAtSurvivesTerminal verifies that the per-account
// map outlives terminal actions on a window. Per-window state is cleared on
// stop/failure/idle/clear, but the per-account map must persist so the
// hook-silence detector can still observe activity from the account.
func TestHandler_LastSeenEventAtSurvivesTerminal(t *testing.T) {
	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	h.stateMu.Lock()
	before, present := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()
	if !present {
		t.Fatalf("lastSeenEventAt[work] missing before terminal action")
	}

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop"})

	h.stateMu.Lock()
	after, stillPresent := h.lastSeenEventAt["work"]
	h.stateMu.Unlock()
	if !stillPresent {
		t.Fatalf("lastSeenEventAt[work] cleared by terminal action; should persist across sessions")
	}
	if !after.Equal(before) {
		t.Errorf("lastSeenEventAt[work] mutated by terminal action: before=%v after=%v", before, after)
	}
}

// TestHandler_NotifiedHookSilenceLatchReArmed verifies that a fresh event for
// an account clears the one-shot hook-silence latch so a future silence
// stretch can produce another notification.
func TestHandler_NotifiedHookSilenceLatchReArmed(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.notifiedHookSilence["work"] = true
	h.stateMu.Unlock()

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Account: "work"})

	h.stateMu.Lock()
	_, latched := h.notifiedHookSilence["work"]
	h.stateMu.Unlock()
	if latched {
		t.Fatalf("notifiedHookSilence[work] should be cleared by a fresh event for the account")
	}
}

// TestHandler_NotifiedHookSilenceUntouchedByOtherAccount verifies that a
// fresh event for one account does not re-arm the latch for another. The
// latch is strictly per-account.
func TestHandler_NotifiedHookSilenceUntouchedByOtherAccount(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.notifiedHookSilence["work"] = true
	h.stateMu.Unlock()

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 43, Action: "active", Account: "personal"})

	h.stateMu.Lock()
	stillLatched := h.notifiedHookSilence["work"]
	h.stateMu.Unlock()
	if !stillLatched {
		t.Fatalf("notifiedHookSilence[work] should not be touched by personal event")
	}
}

// TestHandler_HookSilenceNotificationDispatches verifies that the notifier
// dispatches the "hook_silence" action through MaybeNotify when enabled,
// using the new label.
func TestHandler_HookSilenceNotificationDispatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := &config.NotificationConfig{}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "work", "work", "hook_silence", false)
		synctest.Wait()
		if calls.len() < 1 {
			t.Fatalf("expected hook_silence notification to fire")
		}
		got := calls.snapshot()[0]
		if got.title != "work" {
			t.Errorf("title = %q; want %q", got.title, "work")
		}
		if got.subtitle != "Hook silence detected" {
			t.Errorf("subtitle = %q; want %q", got.subtitle, "Hook silence detected")
		}
	})
}

// TestHandler_HookSilenceNotificationSuppressedByConfig verifies that the
// notifier honors NotificationConfig.HookSilence = &false.
func TestHandler_HookSilenceNotificationSuppressedByConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		disabled := false
		cfg := &config.NotificationConfig{HookSilence: &disabled}
		n, calls := newTestNotifier(cfg)
		n.MaybeNotify(1, "work", "work", "hook_silence", false)
		synctest.Wait()
		if calls.len() > 0 {
			t.Fatalf("hook_silence should be suppressed when HookSilence=&false")
		}
	})
}
