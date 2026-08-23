package daemon

// Which window owns the tab title when a sweep fires inside a split tab. A tab
// shows one title but can hold several windows, so the sweep may only rewrite
// it on behalf of the window that currently owns it. Sweeping a stale sibling
// clears that sibling's state and notifies, but must leave the title the owner
// put there alone.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestSweepStale_DoesNotClearTitleForStaleNonOwnerSibling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		boltTitle := palette.BuildTitle("W:api", "bolt", false)
		fake.windows = fakeOSWindowsWithSplitTab(boltTitle, []kitty.Window{
			{
				ID:       42,
				IsActive: true,
				UserVars: kitty.UserVars{
					Profile: "work",
					Harness: "codex",
				},
			},
			{
				ID: 43,
				UserVars: kitty.UserVars{
					Profile: "work",
					Harness: "codex",
				},
			},
		})

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(43).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(43).LastAction = "active"
		h.ensureWindowStateLocked(43).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d; want 0", got)
		}
		h.stateMu.Lock()
		hasAction := h.store[43].LastAction != ""
		h.stateMu.Unlock()
		if hasAction {
			t.Fatalf("lastAction[43] still present after sweep")
		}
		if got := fake.windows[0].Tabs[0].Title; got != boltTitle {
			t.Fatalf("tab title = %q; want %q", got, boltTitle)
		}
	})
}

func TestSweepStale_ClearsTitleForStaleOwnerInSplitTab(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newSweepTestHandler(10 * time.Minute)
		boltTitle := palette.BuildTitle("W:api", "bolt", false)
		clearedTitle := palette.BuildTitle("W:api", "", false)
		fake.windows = fakeOSWindowsWithSplitTab(boltTitle, []kitty.Window{
			{
				ID:       42,
				IsActive: true,
				UserVars: kitty.UserVars{
					Profile: "work",
					Harness: "codex",
				},
			},
			{
				ID: 43,
				UserVars: kitty.UserVars{
					Profile: "work",
					Harness: "codex",
				},
			},
		})

		now := time.Now()
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Activity = now.Add(-15 * time.Minute)
		h.ensureWindowStateLocked(42).LastAction = "active"
		h.ensureWindowStateLocked(42).LastSessionTitle = "W:api"
		h.stateMu.Unlock()

		h.SweepStale(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("dispatches = %d; want 1", got)
		}
		titles := fake.titleCalls()
		if len(titles) != 1 {
			t.Fatalf("SetTabTitle calls = %d; want 1", len(titles))
		}
		if titles[0].match != kitty.MatchTabByWindowID(42) {
			t.Fatalf("SetTabTitle match = %q; want %q", titles[0].match, kitty.MatchTabByWindowID(42))
		}
		if titles[0].title != clearedTitle {
			t.Fatalf("SetTabTitle title = %q; want %q", titles[0].title, clearedTitle)
		}
		h.stateMu.Lock()
		hasAction := h.store[42].LastAction != ""
		h.stateMu.Unlock()
		if hasAction {
			t.Fatalf("lastAction[42] still present after sweep")
		}
	})
}

func fakeOSWindowsWithSplitTab(tabTitle string, windows []kitty.Window) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:      100,
					Title:   tabTitle,
					Windows: windows,
				},
			},
		},
	}
}
