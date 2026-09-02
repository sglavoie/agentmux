package daemon

// What the stale sweep does and does not touch. The sweep clears the states
// that mean "the agent is working" once they go quiet past their threshold, and
// leaves alone everything that is quiet by design: an unacknowledged stop or
// permission, a run waiting on a mailbox reply, and any window whose threshold
// is zero. Tab-title ownership across split tabs lives in
// handler_sweep_stale_owner_test.go; the busy-stretch anchor in
// handler_sweep_stale_busy_test.go.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestSweepStale_NoOpWhenThresholdZero verifies that a zero threshold disables
// the sweep entirely: no notifications, no title writes, no state mutations,
// even when a session is wildly stale.
func TestSweepStale_NoOpWhenThresholdZero(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(0)
		// Seed the live window set so pruneClosedWindows treats 42 as live and
		// does not GC the seeded per-window state under test. The threshold
		// itself is what gates the sweep; the GC is unrelated.
		fake.windows = fakeOSWindowsWithTab(42, "W:api")
		now := time.Now()

		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		action := h.store[42].LastAction
		h.stateMu.Unlock()
		if action != "active" {
			t.Fatalf("lastAction[42] = %q; want active (untouched)", action)
		}
	})
}

// TestSweepStale_ClearsActiveSessionAndNotifies verifies the happy path: a
// stale active session fires a single "Session idle" notification, has its
// daemon-side state cleared, and gets its tab-title icon stripped.
func TestSweepStale_ClearsActiveSessionAndNotifies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		boltTitle := palette.BuildTitle("W:api", "bolt", false)
		clearedTitle := palette.BuildTitle("W:api", "", false)
		fake.windows = fakeOSWindowsWithTab(42, boltTitle)

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.ensureWindowStateLocked(42).LastPipeMode = false
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		calls := rec.snapshot()
		if len(calls) != 1 {
			t.Fatalf("dispatches = %d; want 1 (%+v)", len(calls), calls)
		}
		if calls[0].title != "W:api" || calls[0].subtitle != "Session idle" {
			t.Fatalf("dispatch = %+v; want title=W:api subtitle=Session idle", calls[0])
		}

		h.stateMu.Lock()
		hasAction := h.store[42].LastAction != ""
		hasActiveEvt := h.store[42].LastActiveEvent != ""
		notified := h.store[42].NotifiedStale
		h.stateMu.Unlock()
		if hasAction {
			t.Fatalf("lastAction[42] still present after sweep")
		}
		if hasActiveEvt {
			t.Fatalf("lastActiveEvent[42] still present after sweep")
		}
		if !notified {
			t.Fatalf("notifiedStale[42] = false; want true")
		}

		titles := fake.titleCalls()
		if len(titles) != 1 {
			t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
		}
		if titles[0].title != clearedTitle {
			t.Fatalf("SetTabTitle title = %q; want %q", titles[0].title, clearedTitle)
		}
	})
}

func TestSweepStale_WithoutNotifierStillClearsStateAndTitle(t *testing.T) {
	guard := NewStateGuard(time.Hour)
	h := NewHandler(guard, nil)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)

	boltTitle := palette.BuildTitle("W:api", "bolt", false)
	clearedTitle := palette.BuildTitle("W:api", "", false)
	fake.windows = fakeOSWindowsWithTab(42, boltTitle)

	now := time.Now()
	h.stateMu.Lock()
	h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
	h.ensureWindowStateLocked(42).LastAction = "active"
	h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
	h.stateMu.Unlock()

	h.SweepStale(now)

	h.stateMu.Lock()
	hasAction := h.store[42].LastAction != ""
	notified := h.store[42].NotifiedStale
	h.stateMu.Unlock()
	if hasAction {
		t.Fatalf("lastAction[42] still present after sweep")
	}
	if !notified {
		t.Fatalf("notifiedStale[42] = false; want true")
	}
	if cur := guard.Current(42); cur != "" {
		t.Fatalf("guard.Current(42) = %q; want cleared", cur)
	}

	titles := fake.titleCalls()
	if len(titles) != 1 {
		t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
	}
	if titles[0].title != clearedTitle {
		t.Fatalf("SetTabTitle title = %q; want %q", titles[0].title, clearedTitle)
	}
}

// TestSweepStale_FreshActiveLeftAlone verifies that a recent active session
// is not touched by the sweep.
func TestSweepStale_FreshActiveLeftAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.windows = fakeOSWindowsWithTab(42, palette.BuildTitle("W:api", "bolt", false))

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-1 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		action := h.store[42].LastAction
		h.stateMu.Unlock()
		if action != "active" {
			t.Fatalf("lastAction[42] = %q; want active (untouched)", action)
		}
	})
}

// TestSweepStale_IgnoresStopAndPermission verifies that stop and permission
// icons (which intentionally persist until acknowledged) are never cleared by
// the sweep, even when their state is older than the threshold.
func TestSweepStale_IgnoresStopAndPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		fake.windows = []kitty.OSWindow{
			{
				ID: 1,
				Tabs: []kitty.Tab{
					{ID: 100, Title: palette.BuildTitle("W:api", "stop", false), Windows: []kitty.Window{{ID: 42}}},
					{ID: 101, Title: palette.BuildTitle("W:api", "key", false), Windows: []kitty.Window{{ID: 43}}},
				},
			},
		}

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "stop"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.ensureWindowStateLocked(43).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(43).LastAction = "permission"
		h.ensureWindowStateLocked(43).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (stop/permission ignored)", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		stopAction := h.store[42].LastAction
		permAction := h.store[43].LastAction
		h.stateMu.Unlock()
		if stopAction != "stop" {
			t.Fatalf("lastAction[42] = %q; want stop (untouched)", stopAction)
		}
		if permAction != "permission" {
			t.Fatalf("lastAction[43] = %q; want permission (untouched)", permAction)
		}
	})
}

// TestSweepStale_IgnoresWaitingASLRun verifies that a worker blocked on a
// mailbox reply keeps its bare 🔁 no matter how long the wait runs. A wait is
// quiet by construction, so sweeping it would erase the live run lease.
func TestSweepStale_IgnoresWaitingASLRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		loopTitle := palette.BuildTitleParts(palette.TitleParts{Base: "W:api", Starred: true, Looping: true})
		fake.windows = fakeOSWindowsWithTab(42, loopTitle)

		now := time.Now()
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.Activity = now.Add(-2 * time.Hour)
		st.LastAction = ""
		st.LastSessionTitle = "W:api"
		st.Reducer = NewReducer(h.reducerCfg)
		st.Reducer.Apply(protocol.ReducerEvent{
			Source: aslSource("run-A"), RunID: "run-A", Phase: ASLPhaseWaiting,
			Seq: 1, At: now.Add(-2 * time.Hour).Unix(),
		})
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 (a waiting run is not idle)", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		action := h.store[42].LastAction
		reduced := h.store[42].Reducer.Reduce(now.Unix())
		h.stateMu.Unlock()
		if action != "" {
			t.Fatalf("lastAction[42] = %q; want empty (untouched)", action)
		}
		if reduced.Icon != "" || !reduced.Looping {
			t.Fatalf("reduced = %+v; want bare 🔁 preserved across the sweep", reduced)
		}
	})
}

// TestSweepStale_SubagentAndCompactAlsoCleared verifies that the sweep treats
// subagent and compact identically to active: all three are "agent-in-flight"
// states that get cleared when stale.
func TestSweepStale_SubagentAndCompactAlsoCleared(t *testing.T) {
	cases := []struct {
		action   string
		iconName string
	}{
		{"active", "bolt"},
		{"subagent", "subagent"},
		{"compact", "compact"},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, fake, rec := newSweepTestHandler(10 * time.Minute)
				inTitle := palette.BuildTitle("W:api", tc.iconName, false)
				outTitle := palette.BuildTitle("W:api", "", false)
				fake.windows = fakeOSWindowsWithTab(42, inTitle)

				now := time.Now()
				h.stateMu.Lock()
				h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
				h.ensureWindowStateLocked(42).LastAction = tc.action
				h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
				h.stateMu.Unlock()

				h.SweepStale(now)
				synctest.Wait()

				if got := rec.len(); got != 1 {
					t.Fatalf("dispatches = %d; want 1", got)
				}
				titles := fake.titleCalls()
				if len(titles) != 1 || titles[0].title != outTitle {
					t.Fatalf("SetTabTitle = %+v; want one call with title %q", titles, outTitle)
				}
				h.stateMu.Lock()
				has := h.store[42].LastAction != ""
				h.stateMu.Unlock()
				if has {
					t.Fatalf("lastAction[42] still present after sweep")
				}
			})
		})
	}
}
