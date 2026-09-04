package daemon

// The unseen bit. Two writers, deliberately asymmetric: publishEffectiveLocked
// raises it when a window comes to rest unwatched, and the snapshot focus
// observation clears it when the user looks. The tests below pin that asymmetry
// from both ends, because either half doing the other's job is a user-visible
// bug — a refresh that could set would re-raise every read row every two
// seconds, and a publish that could clear would lose the news.

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// unseen reads the window's unseen bit.
func unseen(t *testing.T, h *Handler, windowID int) bool {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.Unseen
}

// seedUnseen writes the bit directly, which is how a case proves "unchanged"
// means unchanged in both directions rather than only in the one the publish path
// happens to produce.
func seedUnseen(h *Handler, windowID int, unseen, muted bool) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.ensureWindowStateLocked(windowID)
	st.Unseen = unseen
	st.Muted = muted
}

// unseenTree builds the canonical one-OS-window, one-tab, one-pane snapshot with
// the pane either focused or not. Focus is all three flags at once, so an
// unfocused tree here still has an active tab and an active pane: the only thing
// that varies is whether the OS window has focus.
func unseenTree(windowID int, focused bool) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID:        1,
		IsActive:  true,
		IsFocused: focused,
		Tabs: []kitty.Tab{{
			ID:       100,
			IsActive: true,
			Title:    "W:api",
			Windows: []kitty.Window{{
				ID:       windowID,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
			}},
		}},
	}}
}

// TestUnseenPublishTransitionTable walks every publish transition that can reach
// the bit. `from` is established first, then the bit and the mute flag are seeded,
// then the focus snapshot is published, then `to` is published — so each row
// isolates exactly one transition against a known starting bit.
func TestUnseenPublishTransitionTable(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		seed    bool
		muted   bool
		focused bool
		to      string
		want    bool
	}{
		{
			name: "focused into rest is seen",
			from: "bolt", focused: true, to: "stop", want: false,
		},
		{
			name: "unfocused into rest is unseen",
			from: "bolt", focused: false, to: "stop", want: true,
		},
		{
			// A watchdog sweep, restore, or probe republishing the same resting
			// icon is not new news. Re-deriving the bit here would undo the focus
			// clear the user just earned.
			name: "rest republished does not re-raise a cleared bit",
			from: "stop", seed: false, focused: false, to: "stop", want: false,
		},
		{
			name: "rest republished keeps a raised bit",
			from: "stop", seed: true, focused: false, to: "stop", want: true,
		},
		{
			name: "leaving rest for work clears the bit",
			from: "stop", seed: true, focused: false, to: "bolt", want: false,
		},
		{
			// Mute suppresses a notification; it is not a claim that the user
			// saw anything. A muted window that finished unwatched is exactly as
			// unread as a loud one.
			name: "muted unfocused into rest is still unseen",
			from: "bolt", muted: true, focused: false, to: "stop", want: true,
		},
		{
			name: "muted focused into rest is still seen",
			from: "bolt", muted: true, focused: true, to: "stop", want: false,
		},
		{
			// Every non-resting icon clears, not just the busy ones: an attention
			// or error icon is a live row, and the bit only describes rest.
			name: "attention clears the bit",
			from: "stop", seed: true, focused: false, to: "key", want: false,
		},
		{
			name: "first publish into rest while unfocused is unseen",
			from: "", focused: false, to: "stop", want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			publishIcon(h, 7, tc.from)
			h.publishSnapshot(unseenTree(7, tc.focused), time.Now())
			seedUnseen(h, 7, tc.seed, tc.muted)

			publishIcon(h, 7, tc.to)

			if got := unseen(t, h, 7); got != tc.want {
				t.Fatalf("Unseen after %q → %q (focused=%v muted=%v seed=%v) = %v; want %v",
					tc.from, tc.to, tc.focused, tc.muted, tc.seed, got, tc.want)
			}
		})
	}
}

// TestUnseenSurvivesUnrelatedRepublishes pins the practical consequence of the
// stop→stop rule: an unfocused row that finished an hour ago stays raised through
// however many maintenance ticks republish it.
func TestUnseenSurvivesUnrelatedRepublishes(t *testing.T) {
	h := newTestHandler()
	publishIcon(h, 7, "bolt")
	h.publishSnapshot(unseenTree(7, false), time.Now())
	publishIcon(h, 7, "stop")
	if !unseen(t, h, 7) {
		t.Fatal("unfocused stop did not raise the bit")
	}

	for i := 0; i < 5; i++ {
		publishIcon(h, 7, "stop")
		if !unseen(t, h, 7) {
			t.Fatalf("republish %d cleared the bit", i+1)
		}
	}
}

// TestFocusSnapshotClearsUnseen is the clearing half seen end to end: the same
// publishSnapshot path production uses, no explicit user action, just the pane
// being on screen when the read landed.
func TestFocusSnapshotClearsUnseen(t *testing.T) {
	h := newTestHandler()
	publishIcon(h, 7, "bolt")
	h.publishSnapshot(unseenTree(7, false), time.Now())
	publishIcon(h, 7, "stop")
	if !unseen(t, h, 7) {
		t.Fatal("unfocused stop did not raise the bit")
	}

	h.publishSnapshot(unseenTree(7, true), time.Now())

	if unseen(t, h, 7) {
		t.Fatal("a snapshot observing focus did not clear the bit")
	}
}

// TestFocusRefreshRequiresAllThreeFlags pins why the observation is a conjunction:
// Kitty marks the active pane of every tab and the active tab of every OS window,
// so any single flag is true for panes nobody can see.
func TestFocusRefreshRequiresAllThreeFlags(t *testing.T) {
	cases := []struct {
		name                               string
		osFocused, tabActive, windowActive bool
		wantCleared                        bool
	}{
		{name: "all three", osFocused: true, tabActive: true, windowActive: true, wantCleared: true},
		{name: "os window not focused", osFocused: false, tabActive: true, windowActive: true},
		{name: "tab not active", osFocused: true, tabActive: false, windowActive: true},
		{name: "pane not active", osFocused: true, tabActive: true, windowActive: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			seedUnseen(h, 7, true, false)
			windows := []kitty.OSWindow{{
				ID:        1,
				IsActive:  true,
				IsFocused: tc.osFocused,
				Tabs: []kitty.Tab{{
					ID:       100,
					IsActive: tc.tabActive,
					Title:    "W:api",
					Windows:  []kitty.Window{{ID: 7, IsActive: tc.windowActive}},
				}},
			}}

			h.stateMu.Lock()
			h.refreshFocusFromSnapshotLocked(windows, time.Now())
			h.stateMu.Unlock()

			if cleared := !unseen(t, h, 7); cleared != tc.wantCleared {
				t.Fatalf("cleared = %v; want %v", cleared, tc.wantCleared)
			}
		})
	}
}

// TestFocusRefreshNeverSetsUnseen is the one-directional guarantee. A snapshot
// that fails to observe focus is not evidence the user looked away — it is the
// normal state of every window but one — so the refresh must leave a seen window
// seen no matter how many times it runs.
func TestFocusRefreshNeverSetsUnseen(t *testing.T) {
	h := newTestHandler()
	// Window 7 is the focused pane; window 8 shares the tab but is not active;
	// window 9 lives in an unfocused OS window. All three start seen.
	windows := []kitty.OSWindow{
		{
			ID:       1,
			IsActive: true,
			Tabs: []kitty.Tab{{
				ID: 100, IsActive: true, Title: "W:api",
				Windows: []kitty.Window{{ID: 9, IsActive: true}},
			}},
		},
		{
			ID:        2,
			IsActive:  true,
			IsFocused: true,
			Tabs: []kitty.Tab{{
				ID: 200, IsActive: true, Title: "W:web",
				Windows: []kitty.Window{{ID: 7, IsActive: true}, {ID: 8}},
			}},
		},
	}
	for _, id := range []int{7, 8, 9} {
		seedUnseen(h, id, false, false)
	}

	for i := 0; i < 3; i++ {
		h.stateMu.Lock()
		h.refreshFocusFromSnapshotLocked(windows, time.Now())
		h.stateMu.Unlock()
	}

	for _, id := range []int{7, 8, 9} {
		if unseen(t, h, id) {
			t.Fatalf("window %d was raised to unseen by the focus refresh", id)
		}
	}
}

// TestFocusRefreshClearsOnlyTheFocusedPane keeps the blast radius honest: looking
// at one row does not mark its neighbours read.
func TestFocusRefreshClearsOnlyTheFocusedPane(t *testing.T) {
	h := newTestHandler()
	for _, id := range []int{7, 8, 9} {
		seedUnseen(h, id, true, false)
	}
	windows := []kitty.OSWindow{
		{
			ID:       1,
			IsActive: true,
			Tabs: []kitty.Tab{{
				ID: 100, IsActive: true, Title: "W:api",
				Windows: []kitty.Window{{ID: 9, IsActive: true}},
			}},
		},
		{
			ID:        2,
			IsActive:  true,
			IsFocused: true,
			Tabs: []kitty.Tab{{
				ID: 200, IsActive: true, Title: "W:web",
				Windows: []kitty.Window{{ID: 7, IsActive: true}, {ID: 8}},
			}},
		},
	}

	h.stateMu.Lock()
	h.refreshFocusFromSnapshotLocked(windows, time.Now())
	h.stateMu.Unlock()

	if unseen(t, h, 7) {
		t.Fatal("the focused pane was not cleared")
	}
	if !unseen(t, h, 8) || !unseen(t, h, 9) {
		t.Fatalf("neighbours were cleared: 8=%v 9=%v", unseen(t, h, 8), unseen(t, h, 9))
	}
}

// TestFocusRefreshIgnoresUnknownWindows guards the refresh against inventing
// state for panes the daemon does not manage — the same non-creating lookup
// refreshMutedFromSnapshotLocked uses.
func TestFocusRefreshIgnoresUnknownWindows(t *testing.T) {
	h := newTestHandler()

	h.stateMu.Lock()
	h.refreshFocusFromSnapshotLocked(unseenTree(4242, true), time.Now())
	storeSize := len(h.store)
	h.stateMu.Unlock()

	if storeSize != 0 {
		t.Fatalf("store holds %d windows after refreshing an unmanaged pane; want 0", storeSize)
	}
}

// TestUnseenNotRaisedByRestartRepublish is the restart guarantee from the daemon
// side. Restore republishes every restored window's reduced state, and the rows
// were persisted as seen, so the sweep must stay quiet: a restart that dumped
// every already-read stop into Needs Attention would be worse than not having the
// bit at all.
func TestUnseenNotRaisedByRestartRepublish(t *testing.T) {
	h := newTestHandler()
	publishIcon(h, 7, "stop")
	h.publishSnapshot(unseenTree(7, true), time.Now())
	if unseen(t, h, 7) {
		t.Fatal("focused stop raised the bit")
	}

	// The shape of a restore: the window already rests, and the restored reduced
	// state rests too, published against a cold snapshot cache that cannot prove
	// focus either way.
	h2 := newTestHandler()
	publishIcon(h2, 7, "stop")
	seedUnseen(h2, 7, false, false)
	publishIcon(h2, 7, "stop")

	if unseen(t, h2, 7) {
		t.Fatal("a restore-shaped republish of an unchanged resting icon raised the bit")
	}
}
