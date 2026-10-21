package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// The item projection is a stored field, not a value derived on read, so it can
// go stale. Every path that writes m.filtered therefore has to reproject, and
// the tests below drive those paths from the outside and assert the invariant
// afterwards: the projection describes exactly the rows the filter left behind,
// in the order the sort put them, and the cursor addresses a line that exists.

// assertItemsMatchFiltered fails unless m.items is a faithful projection of the
// current m.filtered and the cursor is in range for it.
func assertItemsMatchFiltered(t *testing.T, m Model, after string) {
	t.Helper()
	items := m.listItems()
	if got, want := len(items.Rows), len(m.filtered); got != want {
		t.Fatalf("after %s: projection holds %d rows, filtered holds %d", after, got, want)
	}
	for i := range m.filtered {
		if items.Rows[i].TabID != m.filtered[i].TabID {
			t.Fatalf("after %s: projection row %d is tab %d, filtered row %d is tab %d — the projection describes a stale order",
				after, i, items.Rows[i].TabID, i, m.filtered[i].TabID)
		}
	}
	// BuildItems is 1:1 today, which is what keeps the urgency view unchanged.
	if got, want := m.listItemCount(), len(m.filtered); got != want {
		t.Errorf("after %s: %d items for %d sessions", after, got, want)
	}
	if m.listItemCount() == 0 {
		if m.cursor != 0 {
			t.Errorf("after %s: cursor %d on an empty list", after, m.cursor)
		}
		return
	}
	if m.cursor < 0 || m.cursor >= m.listItemCount() {
		t.Errorf("after %s: cursor %d is outside the %d-item list", after, m.cursor, m.listItemCount())
	}
	if _, ok := m.selectedItem(); !ok {
		t.Errorf("after %s: cursor %d addresses no item", after, m.cursor)
	}
}

// rebuildTestModel returns a model with two profiles' worth of sessions, so the
// profile-filter cycle has something to cycle between.
func rebuildTestModel() Model {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
	}
	m := NewModel(cfg, kitty.NewClientWithSocket(kitty.NewMockRunner(nil), ""))
	m.width, m.height = 120, 40
	return loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work", State: "key"},
		{TabID: 2, WindowID: 12, Base: "backend", Account: "work", State: "bolt"},
		{TabID: 3, WindowID: 13, Base: "dotfiles", Account: "personal", State: ""},
	})
}

// TestItemsRebuildOnTopologyRefresh covers the refresh completion path: a new
// window tree replaces every row, so a projection left over from the previous
// tree would name windows that are already gone.
func TestItemsRebuildOnTopologyRefresh(t *testing.T) {
	t.Parallel()

	m := rebuildTestModel()
	assertItemsMatchFiltered(t, m, "the initial load")

	m = loadSessions(m, []session.RunningSession{
		{TabID: 4, WindowID: 14, Base: "etl", Account: "work", State: "bolt"},
	})
	assertItemsMatchFiltered(t, m, "a refresh that replaced the whole tree")

	m = loadSessions(m, nil)
	assertItemsMatchFiltered(t, m, "a refresh that emptied the tree")
}

// TestItemsRebuildOnFilterChange covers live filtering, the Esc cancel that
// restores the snapshot, and the Enter commit — each of which reapplies the
// filter to m.sessions and so rewrites m.filtered.
func TestItemsRebuildOnFilterChange(t *testing.T) {
	t.Parallel()

	m := rebuildTestModel()

	m = pressKey(m, "/")
	m = typeString(m, "api")
	assertItemsMatchFiltered(t, m, "typing a filter")
	if m.listItemCount() != 1 {
		t.Fatalf("filter \"api\" left %d items, want 1", m.listItemCount())
	}

	m = pressKey(m, "enter")
	assertItemsMatchFiltered(t, m, "committing the filter")

	m = pressKey(m, "/")
	m = typeString(m, "zzz")
	assertItemsMatchFiltered(t, m, "a filter that matches nothing")
	m = pressKey(m, "esc")
	assertItemsMatchFiltered(t, m, "cancelling back to the snapshot")
}

// TestItemsRebuildOnProfileFilterCycle covers the @profile chip cycle, which
// swaps one filter component and reapplies the rest.
func TestItemsRebuildOnProfileFilterCycle(t *testing.T) {
	t.Parallel()

	m := rebuildTestModel()
	seen := map[int]bool{}
	// One full lap plus one, so the wrap back to "all profiles" is covered too.
	for i := 0; i < len(m.cfg.Accounts)+2; i++ {
		m.cycleDashboardProfile()
		assertItemsMatchFiltered(t, m, "cycling the profile filter")
		seen[m.listItemCount()] = true
	}
	if len(seen) < 2 {
		t.Errorf("the profile cycle never changed the visible set (%v); the assertions above are vacuous", seen)
	}
}

// TestItemsRebuildOnResort covers the sort itself: sortFiltered reorders rows
// in place, and a projection built before the reorder names the wrong line for
// every row the sort moved.
func TestItemsRebuildOnResort(t *testing.T) {
	t.Parallel()

	m := rebuildTestModel()

	// Reverse the rows behind the projection's back, then resort. If the
	// reprojection were dropped, items.Rows would still hold the reversed order.
	for i, j := 0, len(m.filtered)-1; i < j; i, j = i+1, j-1 {
		m.filtered[i], m.filtered[j] = m.filtered[j], m.filtered[i]
	}
	m.sortFiltered()
	assertItemsMatchFiltered(t, m, "a resort")

	if got := m.listItems().Rows[0].State; got != "key" {
		t.Errorf("first item state = %q, want the attention row first", got)
	}
}

// TestSelectedSessionSurvivesRefresh pins the other half of the contract: the
// rebuild must not cost the operator their place. The cursor is restored by
// identity, so a row that is still visible stays selected even when the refresh
// reorders the list around it.
func TestSelectedSessionSurvivesRefresh(t *testing.T) {
	t.Parallel()

	m := rebuildTestModel()
	m = pressKey(m, keyEnd)
	before, ok := m.selectedSession()
	if !ok {
		t.Fatal("no session under the cursor after end")
	}

	// Same sessions, different order, plus a new one ahead of them all.
	m = loadSessions(m, []session.RunningSession{
		{TabID: 9, WindowID: 19, Base: "aaa", Account: "work", State: "bolt"},
		{TabID: 3, WindowID: 13, Base: "dotfiles", Account: "personal", State: ""},
		{TabID: 2, WindowID: 12, Base: "backend", Account: "work", State: "bolt"},
		{TabID: 1, WindowID: 11, Base: "api", Account: "work", State: "key"},
	})
	assertItemsMatchFiltered(t, m, "a reordering refresh")

	after, ok := m.selectedSession()
	if !ok {
		t.Fatal("no session under the cursor after the refresh")
	}
	if after.TabID != before.TabID {
		t.Errorf("selection moved from tab %d to tab %d across a refresh", before.TabID, after.TabID)
	}
}

// TestSelectedSessionClearedWhenFilteredOut is the complement: a selection that
// is no longer visible cannot be preserved, and what replaces it must still be
// a valid item rather than a dangling index.
func TestSelectedSessionClearedWhenFilteredOut(t *testing.T) {
	t.Parallel()

	m := rebuildTestModel()
	m = pressKey(m, keyEnd)
	gone, _ := m.selectedSession()

	m = pressKey(m, "/")
	m = typeString(m, "api")
	m = pressKey(m, "enter")
	assertItemsMatchFiltered(t, m, "filtering the selection away")

	s, ok := m.selectedSession()
	if !ok {
		t.Fatal("the surviving row did not resolve to a session")
	}
	if s.TabID == gone.TabID {
		t.Errorf("tab %d is still selected despite being filtered out", gone.TabID)
	}
}
