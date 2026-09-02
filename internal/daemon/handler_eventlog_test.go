package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

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

		h.Handle(protocol.Request{
			Type:         protocol.TypeStateUpdate,
			WindowID:     1,
			Action:       "active",
			Event:        "PreToolUse",
			ToolName:     "Bash",
			SessionTitle: "W:api",
		})

		// Backdate the activity timestamp so the sweep treats the window as stale.
		h.stateMu.Lock()
		h.ensureWindowStateLocked(1).Activity = time.Now().Add(-15 * time.Minute)
		h.stateMu.Unlock()

		h.SweepStale(time.Now())
		synctest.Wait()

		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 1})
		if !resp.OK {
			t.Fatalf("session detail: want OK=true, got %+v", resp)
		}
		// The sweep appends its own synthetic WatchdogStale entry; the original
		// history must still be there in front of it.
		if len(resp.Events) != 2 {
			t.Fatalf("events after SweepStale = %d; want 2 (%+v)", len(resp.Events), resp.Events)
		}
		if resp.Events[0].Event != "PreToolUse" {
			t.Errorf("event = %+v; want PreToolUse", resp.Events[0])
		}
		if resp.Events[1].Event != eventWatchdogStale {
			t.Errorf("event = %+v; want %s", resp.Events[1], eventWatchdogStale)
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
		h.ensureWindowStateLocked(1).EventLog = []protocol.SessionEvent{{At: now.Unix(), Event: "PreToolUse", Action: "active"}}
		h.ensureWindowStateLocked(2).EventLog = []protocol.SessionEvent{{At: now.Unix(), Event: "PreToolUse", Action: "active"}}
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		resp1 := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 1})
		if !resp1.OK {
			t.Fatalf("session detail w1: want OK=true, got %+v", resp1)
		}
		if len(resp1.Events) != 1 {
			t.Fatalf("window 1 events = %d; want 1 (%+v)", len(resp1.Events), resp1.Events)
		}

		resp2 := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 2})
		if !resp2.OK {
			t.Fatalf("session detail w2: want OK=true, got %+v", resp2)
		}
		if len(resp2.Events) != 0 {
			t.Fatalf("window 2 events = %d; want 0 — closed-window GC should drop it (%+v)", len(resp2.Events), resp2.Events)
		}
	})
}

// TestHandler_PruneClosedWindows_DropsAllLivePerWindowMaps verifies the
// broader closed-window GC: the per-window live store for a window not reported
// as live by Kitty is dropped during SweepStale, while the live window's state
// survives. Without this, a session whose tab was closed forcefully (Cmd+W,
// crash) without a terminal hook event would leak one entry per killed window
// for the daemon's lifetime, and a stale notifiedStale latch could silently
// suppress a notification for a hypothetical reused window ID. windowMetadata
// is intentionally retained for post-mortem exports.
func TestHandler_PruneClosedWindows_DropsAllLivePerWindowMaps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, _ := newSweepTestHandler(10 * time.Minute)
		fake.mu.Lock()
		fake.windows = fakeOSWindowsWithTab(1, "W:live")
		fake.mu.Unlock()

		now := time.Now()
		h.stateMu.Lock()
		for _, id := range []int{1, 2} {
			h.ensureWindowStateLocked(id).Activity = now
			h.ensureWindowStateLocked(id).LastAction = "active"
			h.ensureWindowStateLocked(id).LastAccount = "work"
			h.ensureWindowStateLocked(id).LastActiveEvent = "PreToolUse"
			h.ensureWindowStateLocked(id).CopilotToolPermissionState = state.CopilotToolPermissionAutoApproved
			h.ensureWindowStateLocked(id).SubagentCount = 1
			h.ensureWindowStateLocked(id).NotifiedStale = true
			h.ensureWindowStateLocked(id).LastSessionTitle = "W:foo"
			h.ensureWindowStateLocked(id).LastPipeMode = false
			h.ensureWindowStateLocked(id).PendingReset = state.ResetFlowRequested
			h.ensureWindowStateLocked(id).EventLog = []protocol.SessionEvent{{At: now.Unix(), Event: "PreToolUse", Action: "active"}}
			h.windowMetadata[id] = protocol.WindowDebugMetadata{Durable: protocol.WindowDebugDurable{SessionTitle: "W:foo"}}
		}
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		h.stateMu.Lock()
		defer h.stateMu.Unlock()

		// Live window 1: every entry should survive.
		st1, ok := h.store[1]
		if !ok {
			t.Fatalf("store[1] dropped; want preserved (live window)")
		}
		if st1.Activity.IsZero() || st1.LastAction == "" || st1.LastAccount == "" ||
			st1.LastActiveEvent == "" || st1.CopilotToolPermissionState == state.CopilotToolPermissionNone ||
			st1.SubagentCount == 0 || !st1.NotifiedStale || st1.LastSessionTitle == "" ||
			st1.PendingReset == state.ResetFlowNone || len(st1.EventLog) == 0 {
			t.Errorf("store[1] = %+v; want all seeded live fields preserved", st1)
		}
		if _, ok := h.windowMetadata[1]; !ok {
			t.Errorf("windowMetadata[1] dropped; want preserved (live window)")
		}

		// Closed window 2: every live map must be GC'd, but the durable
		// windowMetadata snapshot is intentionally retained so post-mortem
		// `amx daemon export-events` invocations still have identity context.
		if _, ok := h.store[2]; ok {
			t.Errorf("store[2] retained; want dropped (closed window)")
		}
		if _, ok := h.windowMetadata[2]; !ok {
			t.Errorf("windowMetadata[2] dropped; want preserved (durable snapshot)")
		}
	})
}

// TestHandler_PruneClosedWindows_PurgesNotifierState verifies that the
// notifier's per-(windowID, action) cooldown map and any pending permission
// timer are released for a window Kitty no longer reports as live. Without
// this, a long permission debounce scheduled before the tab was killed would
// fire (and dispatch under the prior session title) for a dead window, and
// the cooldown map would grow linearly with cumulative closed windows over
// the daemon's lifetime.
func TestHandler_PruneClosedWindows_PurgesNotifierState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		// A 24h debounce keeps the scheduled permission timer from firing
		// during the test, so the assertions observe whether PurgeWindow
		// cancelled and removed it rather than whether it elapsed.
		notifier, _ := newTestNotifier(&config.NotificationConfig{
			Permission: true, PermissionDebounceMs: int((24 * time.Hour).Milliseconds()),
		})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{}
		h.SetKittyClient(fake)
		h.SetStaleThresholds(map[string]time.Duration{
			"active":   10 * time.Minute,
			"subagent": 10 * time.Minute,
			"compact":  10 * time.Minute,
		}, nil)
		fake.mu.Lock()
		fake.windows = fakeOSWindowsWithTab(1, "W:live")
		fake.mu.Unlock()

		// Schedule a real pending permission for the soon-to-be-closed window,
		// then seed cooldown entries for both windows. The pending entry has to
		// come first: a recorded permission cooldown would suppress it.
		h.notifier.MaybeNotify(2, "W:dead", "", "permission", false)
		h.notifier.MarkNotified(1, "stop", time.Now())
		h.notifier.MarkNotified(2, "stop", time.Now())
		h.notifier.MarkNotified(2, "permission", time.Now())

		// Seed an eventLog entry for window 2 so pruneClosedWindows sees it as
		// a candidate to GC.
		h.stateMu.Lock()
		h.ensureWindowStateLocked(2).EventLog = []protocol.SessionEvent{{At: time.Now().Unix(), Event: "Stop", Action: "stop"}}
		h.stateMu.Unlock()

		h.SweepStale(time.Now())
		synctest.Wait()

		if !h.notifier.HasCooldown(1, "stop") {
			t.Errorf("notifier cooldown for live window 1 dropped; want preserved")
		}
		if h.notifier.HasCooldown(2, "stop") {
			t.Errorf("notifier cooldown w2:stop retained; want dropped (closed window)")
		}
		if h.notifier.HasCooldown(2, "permission") {
			t.Errorf("notifier cooldown w2:permission retained; want dropped (closed window)")
		}
		if h.notifier.HasPending(2) {
			t.Errorf("notifier pending permission for window 2 retained; want cancelled (closed window)")
		}
	})
}

// TestHandler_PruneClosedWindows_PurgesGuardEntries verifies that the
// state-guard map is also GC'd when Kitty no longer reports a window. The
// guard is keyed by window ID and is otherwise only cleared by idle/clear/
// user_clear events; a window closed forcefully (Cmd+W, crash) without a
// terminal hook would leak its guardEntry indefinitely. The live window's
// guard must remain intact.
func TestHandler_PruneClosedWindows_PurgesGuardEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, _ := newSweepTestHandler(10 * time.Minute)
		fake.mu.Lock()
		fake.windows = fakeOSWindowsWithTab(1, "W:live")
		fake.mu.Unlock()

		// Seed guard entries for both a live and a closed window.
		h.guard.TryUpdate(1, "permission", false)
		h.guard.TryUpdate(2, "permission", false)

		// Seed an eventLog entry for window 2 so pruneClosedWindows sees it as
		// a candidate to GC.
		h.stateMu.Lock()
		h.ensureWindowStateLocked(2).EventLog = []protocol.SessionEvent{{At: time.Now().Unix(), Event: "Stop", Action: "stop"}}
		h.stateMu.Unlock()

		h.SweepStale(time.Now())
		synctest.Wait()

		if cur := h.guard.Current(1); cur != "permission" {
			t.Errorf("guard[1] = %q; want preserved 'permission' for live window", cur)
		}
		if cur := h.guard.Current(2); cur != "" {
			t.Errorf("guard[2] = %q; want cleared for closed window", cur)
		}
	})
}
