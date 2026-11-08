package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file is the interaction half of the Projects view: the palette entry
// that switches the arrangement, Enter's folding, and the three things that
// have to survive a rebuild — which line the cursor is on, which families are
// folded, and what the preview pane is pointed at.
//
// Both of the vocabularies it used to drive are retired. `v` switched the
// arrangement and is a palette command now; `←`/`→` folded and unfolded and are
// Enter alone, which toggles. Enter is unambiguous here because a summary is the
// one line where it has no session to focus, so the key that means "act on this
// row" already meant nothing else on the row folding applies to.
//
// Every assertion here is written against the item list rather than against a
// session index. That is the whole point of the projection: under Projects a
// line is not a session, so a test that counted sessions would agree with the
// model right up to the first fold and then quietly stop describing the screen.

// TestProjectsNavigationAtListBoundaries drives the cursor over the ends of the
// list under every fold arrangement that changes its length. The bound is the
// item count, which is what a folded family makes smaller than the session
// count — a clamp still charging sessions would let the cursor sit past the end.
func TestProjectsNavigationAtListBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		folds  []string
		length int
	}{
		{"all expanded", nil, 7},
		{"one folded", []string{"summary:monorepo(open)"}, 5},
		{"both folded", []string{"summary:monorepo(open)", "summary:toolbox(open)"}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := listViewModel(dashboard.ViewProjects)
			for _, line := range tc.folds {
				m = selectLine(t, m, line)
				m = foldToggle(m)
			}
			if got := m.listItemCount(); got != tc.length {
				t.Fatalf("list holds %d lines (%v), want %d", got, itemLines(m), tc.length)
			}

			// Named keys and familiar aliases navigate the same projected list.
			m = pressKey(m, keyHome)
			if m.cursor != 0 {
				t.Errorf("home left the cursor at %d, want the first line", m.cursor)
			}
			m = pressKey(m, keyK)
			if m.cursor != 0 {
				t.Errorf("k at the top wrapped or ran off to %d", m.cursor)
			}

			m = pressKey(m, keyEnd)
			if got, want := m.cursor, tc.length-1; got != want {
				t.Errorf("end left the cursor at %d, want %d", got, want)
			}
			m = pressKey(m, keyJ)
			if got, want := m.cursor, tc.length-1; got != want {
				t.Errorf("j at the bottom ran off to %d, want %d", got, want)
			}

			m = pressKey(m, keyG)
			if m.cursor != 0 {
				t.Fatalf("g did not reach the first line: %d", m.cursor)
			}
			m = pressKey(m, keyGG)
			if m.cursor != tc.length-1 {
				t.Fatalf("G did not reach the last line: %d", m.cursor)
			}

			// Every line is reachable, and every line the cursor stops on is a
			// line the renderer can scroll to.
			m.height = 12
			for i := 0; i < tc.length; i++ {
				m.cursor = i
				scrollListToCursor(&m)
				if !listCursorFitsWindow(&m, m.listOffset, listVisibleHeight(m), listShowsGroupHeadings(&m)) {
					t.Errorf("line %d (%q) does not fit the window at offset %d", i, selectedLine(m), m.listOffset)
				}
			}
		})
	}
}

// TestProjectsNavigationUnderAFilter asserts the projection is built from the
// rows that survived the filter, not from the family as it exists. A family
// filtered down to one visible checkout is a plain leaf: a summary over one row
// would be a claim about a group the operator cannot see the rest of.
func TestProjectsNavigationUnderAFilter(t *testing.T) {
	t.Parallel()

	m := listViewModel(dashboard.ViewProjects)

	m.applyFilterChange(dashboard.Parse("api"))
	wantLines(t, m, []string{"session:api"})
	if m.cursor != 0 {
		t.Errorf("the filter left the cursor at %d over a one-line list", m.cursor)
	}
	if _, ok := m.selectedSession(); !ok {
		t.Error("the lone surviving checkout did not resolve to a session")
	}

	// Widening the filter back to both members restores the summary, and the
	// count it reports is of visible rows only.
	m.applyFilterChange(dashboard.Parse("repo"))
	wantLines(t, m, []string{"summary:monorepo(open)", "session:api", "session:web"})
	summary, _ := m.items.At(0)
	if got := m.items.SummaryCounts(summary, m.lastActivity, m.idleThreshold); got != [3]int{1, 1, 0} {
		t.Errorf("summary counts under the filter = %v, want one attention and one active", got)
	}

	m.applyFilterChange(dashboard.Filter{})
	wantLines(t, m, projectLines)
}
