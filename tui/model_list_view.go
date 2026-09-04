// This file owns the two choices that change how the dashboard list is
// arranged without changing what is in it: which view the rows are projected
// under, and which project families are folded inside that view.
//
// Both are process-local by design. Every run starts in Urgency with every
// family expanded, and the choices last until the program exits. The collapse
// map is keyed by family key, and a family key is built from a filesystem path
// — persisting it would write checkout paths to disk on behalf of a keystroke
// that only asked to hide three rows, which is why context.md lists persistent
// view and collapse state as a non-goal rather than as unfinished work.
//
// Every mutation here ends in reprojectList, which is what makes a fold, an
// unfold, and a view switch one step rather than four. The order inside it is
// the whole point: rebuild the projection, resolve the cursor against the list
// that came out, size the window around the cursor that survived, and only then
// ask the preview what it should be showing. Refreshing the preview before the
// selection settles would point the pane at whichever row the cursor happened
// to be sitting on mid-rebuild.

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// reprojectList rebuilds the item projection and lands the cursor back on what
// the operator was looking at, atomically. prior is captured against the OLD
// projection, so callers must take it before the change they are about to make.
func (m *Model) reprojectList(prior dashboard.CursorIdentity) tea.Cmd {
	m.rebuildItems()
	m.cursor = dashboard.RestoreItemCursor(m.items, prior, m.lastActivity, m.idleThreshold)
	if n := m.listItemCount(); n == 0 {
		m.cursor = 0
	} else {
		m.cursor = clamp(m.cursor, 0, n-1)
	}
	clampListOffset(m)
	scrollListToCursor(m)
	return m.refreshPreviewForCursor()
}

// toggleListView switches the list between the urgency arrangement and the
// project-family one. It is what both the `v` key and the palette entry run, so
// the two cannot drift.
func (m *Model) toggleListView() tea.Cmd {
	if m.listView == dashboard.ViewProjects {
		return m.setListView(dashboard.ViewUrgency)
	}
	return m.setListView(dashboard.ViewProjects)
}

// setListView draws the list under view, carrying the selection across.
func (m *Model) setListView(view dashboard.ViewMode) tea.Cmd {
	if m.listView == view {
		return nil
	}
	prior := m.viewSwitchIdentity(view)
	m.listView = view
	return m.reprojectList(prior)
}

// viewSwitchIdentity is what the cursor should be looking for on the other side
// of a view switch.
//
// A selected session carries itself over unchanged: it is a line in both views,
// so if it is visible after the switch that is where the cursor lands, and if a
// collapsed family hides it RestoreItemCursor's family fallback puts the cursor
// on the summary standing in for it. Neither case needs help from here.
//
// A selected summary does. Urgency has no summaries at all, so a captured one
// would resolve through RestoreItemCursor's findFamilyMember fallback to the
// family's first member in the rebuilt list. Today that IS the lead, because the
// row list is urgency-sorted and a family's first row is therefore its most
// urgent one — the two agree by coincidence of the sort, not by construction.
//
// Substituting the lead says which of the two was meant. It is what makes the
// landing survive an ordering the sort does not currently produce (a Projects
// view ordered by family label, say), and it captures a full session identity
// rather than a family key, so a member whose family stamping was lost between
// projections is still found by tab ID instead of falling through to the first
// attention row.
func (m Model) viewSwitchIdentity(view dashboard.ViewMode) dashboard.CursorIdentity {
	it, ok := m.selectedItem()
	if !ok {
		return dashboard.CursorIdentity{}
	}
	if it.Kind == dashboard.ItemFamilySummary && view != dashboard.ViewProjects {
		if lead, found := m.items.SummaryLead(it, m.lastActivity, m.idleThreshold); found {
			return dashboard.CaptureCursorIdentity(lead)
		}
	}
	return dashboard.CaptureItemIdentity(m.items, m.cursor)
}

// setFamilyCollapsed folds or unfolds one family and reprojects around the
// change. A no-op request returns nil rather than spending a rebuild, which is
// what keeps Left on an already-folded family from re-fetching the preview.
//
// The map is replaced rather than mutated in place. Model is copied by value on
// every Update, so writing into the existing map would reach into every copy
// that shares it, including the one a caller is holding while it decides what to
// do with the result.
func (m *Model) setFamilyCollapsed(key string, collapsed bool) tea.Cmd {
	if key == "" || m.collapsedFamilies[key] == collapsed {
		return nil
	}
	next := make(map[string]bool, len(m.collapsedFamilies)+1)
	for k, v := range m.collapsedFamilies {
		next[k] = v
	}
	if collapsed {
		next[key] = true
	} else {
		// Deleted rather than set to false: absent IS expanded, so a map that
		// only ever grows would keep a family the operator unfolded as a
		// permanent entry saying nothing.
		delete(next, key)
	}
	prior := dashboard.CaptureItemIdentity(m.items, m.cursor)
	m.collapsedFamilies = next
	return m.reprojectList(prior)
}

// collapseSelection is what Left does in the list.
//
// On a summary it folds the family. On a child it does NOT fold anything: it
// moves the selection up to the family's summary first, so the keystroke that
// hides a row is always pressed while looking at the line that will remain. A
// second Left then folds. Anything else — a top-level leaf, a row whose family
// has no summary, the urgency view where no summary exists — has no family line
// to move to and does nothing.
func (m *Model) collapseSelection() tea.Cmd {
	it, ok := m.selectedItem()
	if !ok {
		return nil
	}
	if it.Kind == dashboard.ItemFamilySummary {
		return m.setFamilyCollapsed(it.FamilyKey, true)
	}
	return m.selectFamilySummary(it.FamilyKey)
}

// expandSelection is what Right does in the list: unfold the selected family.
// A child is already inside an unfolded family, so Right on one does nothing
// rather than guessing at a deeper level the list does not have.
func (m *Model) expandSelection() tea.Cmd {
	it, ok := m.selectedItem()
	if !ok || it.Kind != dashboard.ItemFamilySummary {
		return nil
	}
	return m.setFamilyCollapsed(it.FamilyKey, false)
}

// toggleSelectedFamily is what Enter and Space do when the selection is a
// summary. Those two keys focus a session's Kitty tab on every other line; a
// summary has no tab to focus, so they fold and unfold instead of refusing.
func (m *Model) toggleSelectedFamily() tea.Cmd {
	it, ok := m.selectedItem()
	if !ok || it.Kind != dashboard.ItemFamilySummary {
		return nil
	}
	return m.setFamilyCollapsed(it.FamilyKey, !it.Collapsed)
}

// selectFamilySummary moves the cursor onto the summary standing for key. It
// changes the selection only; the projection is untouched, so this is a cursor
// move with the same scroll and preview bookkeeping j and k do.
func (m *Model) selectFamilySummary(key string) tea.Cmd {
	i := m.items.FamilySummaryIndex(key)
	if i < 0 || i == m.cursor {
		return nil
	}
	m.cursor = i
	scrollListToCursor(m)
	return m.refreshPreviewForCursor()
}

// summarySelected reports whether the cursor is parked on a family summary. It
// is the footer's and the palette's question, asked the other way round from
// hasSelectedSession: some keys are unavailable on a summary, and a few — the
// collapse keys — are available on nothing else.
func (m Model) summarySelected() bool {
	it, ok := m.selectedItem()
	return ok && it.Kind == dashboard.ItemFamilySummary
}
