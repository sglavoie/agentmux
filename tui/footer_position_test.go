package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file holds the footer position indicator's agreement with the renderer.
// The indicator says which items are on screen out of how many, and the only
// way it can say that truthfully is to ask the same windowed walk the renderer
// runs: a list-pane height is a budget of LINES, and in a multi-group urgency
// view the group labels and their separators are spent out of it before a
// single session is drawn.

// positionFixtureRows returns seven rows spanning all three urgency buckets, so
// the renderer draws three group labels and two blank separators — five lines of
// chrome the item count knows nothing about.
//
// Each base is a token that appears nowhere else in a rendered row, which is
// what lets a test map a painted line back to the item it came from.
func positionFixtureRows() []session.RunningSession {
	states := []string{"question", "question", "bolt", "bolt", "bolt", "stop", "stop"}
	out := make([]session.RunningSession, len(states))
	for i, state := range states {
		out[i] = session.RunningSession{
			TabID:     i + 1,
			WindowID:  i + 1,
			Base:      fmt.Sprintf("zrow%02d", i+1),
			Account:   "work",
			Directory: "/tmp",
			State:     state,
		}
	}
	return out
}

func positionFixtureModel(rows []session.RunningSession, width, height int) Model {
	m := testModel()
	m.width, m.height = width, height
	return withListRows(m, rows...)
}

// itemToken is the string a rendered line carries when it is drawing this item:
// a session's base, or a family summary's label.
func itemToken(m Model, i int) string {
	if s, ok := m.items.SessionAt(i); ok {
		return s.Base
	}
	it, _ := m.items.At(i)
	return it.FamilyLabel
}

// renderedItemWindow reports the half-open item range the list renderer actually
// painted, and whether it drew the overflow markers. It reads the painted text
// rather than re-deriving the window, so it cannot agree with a broken fit by
// sharing its mistake.
func renderedItemWindow(t *testing.T, m Model) (start, end int, top, bottom bool) {
	t.Helper()
	out := stripAnsi(renderList(m))
	start, end = -1, -1
	for i := 0; i < m.listItemCount(); i++ {
		token := itemToken(m, i)
		if token == "" {
			t.Fatalf("item %d has no token to match a rendered line against", i)
		}
		if !strings.Contains(out, token) {
			continue
		}
		if start == -1 {
			start = i
		}
		end = i + 1
	}
	if start == -1 {
		t.Fatalf("no item of %d was rendered:\n%s", m.listItemCount(), out)
	}
	// The renderer paints one contiguous window; a gap would mean the token
	// matching, not the renderer, produced this range.
	for i := start; i < end; i++ {
		if !strings.Contains(out, itemToken(m, i)) {
			t.Fatalf("rendered items are not contiguous: item %d missing from [%d,%d):\n%s", i, start, end, out)
		}
	}
	return start, end, strings.Contains(out, "▲"), strings.Contains(out, "▼")
}

// TestListFooterPositionFragmentAccountsForGroupChrome is the regression for a
// footer that compared a line budget to an item count and concluded that seven
// items fit a fifteen-line terminal, while the renderer was drawing three of
// them under a ▼.
//
// Every case asserts the same rule from the rendered pixels outward: the
// fragment names the window the renderer painted, and it is absent exactly when
// that window is the whole list.
func TestListFooterPositionFragmentAccountsForGroupChrome(t *testing.T) {
	t.Run("multi-group window is smaller than the line budget", func(t *testing.T) {
		m := positionFixtureModel(positionFixtureRows(), 120, 15)
		total := m.listItemCount()

		// The two setup guards are what keep this test from passing vacuously.
		// The first is the shape the bug needed: a budget that looks big enough
		// for every item. The second is the fact it ignored: the renderer, paying
		// for three group labels and two separators, shows fewer.
		if budget := listVisibleHeight(m); budget < total {
			t.Fatalf("setup: line budget %d is already below the %d items; the old suppression rule would not have fired", budget, total)
		}
		start, end, top, bottom := renderedItemWindow(t, m)
		if start != 0 || end >= total {
			t.Fatalf("setup: want a window starting at the top and overflowing; got [%d,%d) of %d", start, end, total)
		}
		if top {
			t.Fatalf("setup: unscrolled window should draw no ▲")
		}
		if !bottom {
			t.Fatalf("setup: a partial window must draw the ▼ this fragment reports")
		}

		want := fmt.Sprintf("%d–%d of %d", start+1, end, total)
		if got := stripAnsi(renderFooter(m)); !strings.Contains(got, want) {
			t.Errorf("footer position fragment = missing %q; got:\n%s", want, got)
		}
	})

	t.Run("scrolled window reports its own range", func(t *testing.T) {
		m := positionFixtureModel(positionFixtureRows(), 120, 15)
		m = pressKey(m, "G")
		if m.listOffset == 0 {
			t.Fatalf("setup: jumping to the last item did not scroll the window")
		}
		total := m.listItemCount()

		// The only setup this case needs is that the window has scrolled off the
		// top; where it ends is the thing under test, so it is read from the
		// render rather than demanded of it.
		start, end, top, _ := renderedItemWindow(t, m)
		if !top {
			t.Fatalf("setup: a scrolled window must draw the ▲ the start of the range accounts for")
		}

		want := fmt.Sprintf("%d–%d of %d", start+1, end, total)
		if got := stripAnsi(renderFooter(m)); !strings.Contains(got, want) {
			t.Errorf("footer position fragment = missing %q; got:\n%s", want, got)
		}
	})

	t.Run("single group keeps its whole-list suppression", func(t *testing.T) {
		rows := positionFixtureRows()
		for i := range rows {
			rows[i].State = "bolt"
		}
		// Taller than the multi-group cases above: one bucket still draws its own
		// heading — the count is a fact about the queue, not a separator between
		// buckets — so the fixture has to be able to pay for it and still show
		// every item, which is the condition the suppression rule is about.
		m := positionFixtureModel(rows, 120, 20)

		start, end, _, _ := renderedItemWindow(t, m)
		if start != 0 || end != m.listItemCount() {
			t.Fatalf("setup: one group draws one heading, so all %d items should still fit; got [%d,%d)", m.listItemCount(), start, end)
		}
		if got := stripAnsi(renderFooter(m)); strings.Contains(got, " of ") {
			t.Errorf("expected no position fragment when the renderer shows every item; got:\n%s", got)
		}
	})

	t.Run("projects view keeps its whole-list suppression", func(t *testing.T) {
		m, _ := listViewModel(dashboard.ViewProjects)
		m.width, m.height = 120, 15

		start, end, _, _ := renderedItemWindow(t, m)
		if start != 0 || end != m.listItemCount() {
			t.Fatalf("setup: the Projects view draws no headings, so all %d items should fit; got [%d,%d)", m.listItemCount(), start, end)
		}
		if got := stripAnsi(renderFooter(m)); strings.Contains(got, " of ") {
			t.Errorf("expected no position fragment when the renderer shows every item; got:\n%s", got)
		}
	})

	t.Run("projects view reports the window it overflows into", func(t *testing.T) {
		m, _ := listViewModel(dashboard.ViewProjects)
		m.width, m.height = 120, 12
		total := m.listItemCount()

		start, end, _, bottom := renderedItemWindow(t, m)
		if !bottom || end >= total {
			t.Fatalf("setup: want an overflowing Projects window at this height; got [%d,%d) of %d", start, end, total)
		}
		want := fmt.Sprintf("%d–%d of %d", start+1, end, total)
		if got := stripAnsi(renderFooter(m)); !strings.Contains(got, want) {
			t.Errorf("footer position fragment = missing %q; got:\n%s", want, got)
		}
	})
}
