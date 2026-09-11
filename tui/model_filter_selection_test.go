package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// pressKeyForPreview presses one key and reports the window the command it
// handed back would read.
//
// Retargeting the pane and dispatching the read are two different things, and
// only the first shows up in the model: a path that moved previewWindowID but
// dropped the command leaves the pane blank until the next tick, which is the
// state a stale preview is actually seen in. Running the command is the only
// way to tell the two apart, so the filter cases below do.
//
// The reply is fed back before returning, because the read controller allows one
// read at a time: a reply left unanswered holds the slot, and the next keystroke
// coalesces into it and hands back nil, which this helper would report as the
// key having dispatched nothing.
func pressKeyForPreview(t *testing.T, m Model, key string) (Model, int) {
	t.Helper()
	updated, cmd := m.Update(keyPress(key))
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("%q returned no command, so nothing re-reads the pane for the selection it left", key)
	}
	msg, ok := cmd().(previewContentMsg)
	if !ok {
		t.Fatalf("%q returned a %T, want the preview fetch for its new selection", key, cmd())
	}
	updated, follow := m.Update(msg)
	m = updated.(Model)
	settlePreviewRead(t, &m, follow)
	return m, msg.windowID
}

// A filter narrows the list; it does not usually remove what the operator was
// looking at. Every filter-driven rebuild therefore has the same two jobs a
// refresh and a view switch have: land the cursor back on the item it started
// on whenever that item is still on screen, and point the preview at whatever
// the cursor ended up on once it has.
//
// Both halves are asserted here against the item projection rather than against
// a session index, because the second half of each case selects a family
// summary — a line that stands for no session at all, and whose preview target
// is therefore window 0 rather than a window of its own.
//
// The numbers in the assertions are what make the test bite. A cursor that is
// merely clamped into the shorter list survives a narrowing by coincidence of
// index, so each case moves the selection to a row whose index in the filtered
// list differs from its index in the full one; a clamp lands on a different row
// and the assertion names both.
func TestDashboardFilterPreservesSelectionAndRetargetsPreview(t *testing.T) {
	t.Run("a still-visible session survives live edits and the commit", func(t *testing.T) {
		m, _ := listViewModel(dashboard.ViewUrgency)
		m = selectLine(t, m, "session:cli")
		settlePreviewRead(t, &m, m.refreshPreviewForCursor())
		if m.previewWindowID != 13 {
			t.Fatalf("setup: the preview tracks window %d, want cli's 13", m.previewWindowID)
		}
		if m.cursor != 3 {
			t.Fatalf("setup: cli sits at line %d, want 3 — the clamp this test kills needs it past the filtered list's end", m.cursor)
		}

		m = pressKey(m, "/")
		if m.mode != ModeSearch {
			t.Fatalf("/ left the model in mode %v, want ModeSearch", m.mode)
		}
		if got := selectedLine(m); got != "session:cli" {
			t.Errorf("entering search moved the selection to %q, want session:cli", got)
		}

		// "tool" matches the two /tools checkouts. cli is the first of them, so
		// a cursor clamped from line 3 into a two-line list lands on lib.
		m = typeString(m, "tool")
		wantLines(t, m, []string{"session:cli", "session:lib"})
		if got := selectedLine(m); got != "session:cli" {
			t.Errorf("the live filter moved the selection to %q, want session:cli", got)
		}
		if m.previewWindowID != 13 {
			t.Errorf("the preview tracks window %d after the live filter, but the cursor is on %q (window 13)",
				m.previewWindowID, selectedLine(m))
		}

		// Committing only leaves the input, so its retarget is a backstop: the
		// invariant it holds is that the pane and the cursor agree once the
		// input is gone, whichever keystroke last moved either.
		m = pressKey(m, "enter")
		if m.mode != ModeList {
			t.Fatalf("enter left the model in mode %v, want ModeList", m.mode)
		}
		if got := selectedLine(m); got != "session:cli" {
			t.Errorf("committing the filter moved the selection to %q, want session:cli", got)
		}
		if m.previewWindowID != 13 {
			t.Errorf("the preview tracks window %d after the commit, but the cursor is on %q (window 13)",
				m.previewWindowID, selectedLine(m))
		}
	})

	t.Run("cancelling restores the saved filter and the pre-search session", func(t *testing.T) {
		m, _ := listViewModel(dashboard.ViewUrgency)
		m = selectLine(t, m, "session:lib")
		settlePreviewRead(t, &m, m.refreshPreviewForCursor())
		if m.previewWindowID != 14 {
			t.Fatalf("setup: the preview tracks window %d, want lib's 14", m.previewWindowID)
		}

		m = pressKey(m, "/")
		// "a" alone already leaves api the only match, so this is the keystroke
		// on which the pane has to move — and be told to re-read.
		m, fetched := pressKeyForPreview(t, m, "a")
		m = typeString(m, "pi")
		wantLines(t, m, []string{"session:api"})
		if m.previewWindowID != 11 {
			t.Errorf("the preview still tracks window %d after the filter left only %q (window 11) on screen",
				m.previewWindowID, selectedLine(m))
		}
		if fetched != 11 {
			t.Errorf("the live filter dispatched a read of window %d, want api's 11", fetched)
		}

		m, fetched = pressKeyForPreview(t, m, "esc")
		if fetched != 14 {
			t.Errorf("cancelling dispatched a read of window %d, want the restored lib's 14", fetched)
		}
		if m.mode != ModeList {
			t.Fatalf("esc left the model in mode %v, want ModeList", m.mode)
		}
		if !m.activeFilter.IsEmpty() {
			t.Errorf("esc left the filter %q applied, want the empty snapshot taken on entry", m.activeFilter.Raw)
		}
		wantLines(t, m, urgencyLines)
		if got := selectedLine(m); got != "session:lib" {
			t.Errorf("cancelling landed on %q, want the pre-search session:lib", got)
		}
		if m.previewWindowID != 14 {
			t.Errorf("the preview tracks window %d after the cancel, but the cursor is back on %q (window 14)",
				m.previewWindowID, selectedLine(m))
		}
	})

	t.Run("a selected family summary survives a filter that keeps its family", func(t *testing.T) {
		m, _ := listViewModel(dashboard.ViewProjects)

		// Start on a session so the pane holds a real window; the moves below
		// then have something to be wrong about.
		m = selectLine(t, m, "session:api")
		settlePreviewRead(t, &m, m.refreshPreviewForCursor())
		m = selectLine(t, m, "summary:toolbox(open)")
		settlePreviewRead(t, &m, m.refreshPreviewForCursor())
		if m.previewWindowID != 0 {
			t.Fatalf("setup: the preview tracks window %d on a summary, want the empty window", m.previewWindowID)
		}
		if m.cursor != 4 {
			t.Fatalf("setup: the toolbox summary sits at line %d, want 4", m.cursor)
		}

		// "tool" keeps both toolbox checkouts, so the family still earns a
		// summary — at line 0 of a three-line list rather than line 4 of a
		// seven-line one. A clamped cursor lands on session:lib.
		m = pressKey(m, "/")
		m = typeString(m, "tool")
		wantLines(t, m, []string{"summary:toolbox(open)", "session:cli", "session:lib"})
		if got := selectedLine(m); got != "summary:toolbox(open)" {
			t.Errorf("the live filter moved the selection to %q, want the toolbox summary", got)
		}
		if m.previewWindowID != 0 {
			t.Errorf("the preview tracks window %d while the cursor is on a summary, which owns none", m.previewWindowID)
		}
		m = pressKey(m, "enter")
		if got := selectedLine(m); got != "summary:toolbox(open)" {
			t.Errorf("committing the filter moved the selection to %q, want the toolbox summary", got)
		}

		// Re-entering search widens the list back to everything and cancelling
		// puts both the committed filter and the summary back, with the pane
		// following the cursor in each direction.
		m = pressKey(m, "/")
		m, fetched := pressKeyForPreview(t, m, "a")
		m = typeString(m, "pi")
		wantLines(t, m, []string{"session:api"})
		if m.previewWindowID != 11 {
			t.Errorf("the preview tracks window %d after the filter left only %q (window 11) on screen",
				m.previewWindowID, selectedLine(m))
		}
		if fetched != 11 {
			t.Errorf("the live filter dispatched a read of window %d, want api's 11", fetched)
		}

		// The cancel lands back on a summary, whose read is the empty-window
		// placeholder rather than a Kitty call.
		m, fetched = pressKeyForPreview(t, m, "esc")
		if fetched != 0 {
			t.Errorf("cancelling onto a summary dispatched a read of window %d, want the empty window", fetched)
		}
		if got := m.activeFilter.Raw; got != "tool" {
			t.Errorf("esc restored the filter %q, want the committed \"tool\"", got)
		}
		wantLines(t, m, []string{"summary:toolbox(open)", "session:cli", "session:lib"})
		if got := selectedLine(m); got != "summary:toolbox(open)" {
			t.Errorf("cancelling landed on %q, want the pre-search toolbox summary", got)
		}
		if m.previewWindowID != 0 {
			t.Errorf("the preview still tracks window %d after the cancel put the cursor back on a summary",
				m.previewWindowID)
		}
	})
}
