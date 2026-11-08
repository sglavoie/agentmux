package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// pastSessionsReadyLauncherModel is launcherHeldInspectorModel with the
// transcript side of Past Sessions already answered, so pressing the launcher's
// `s` exercises the mode change and nothing else.
//
// Both refresh seams are stubbed rather than one: entry through
// enterSessionSearchForProject takes the warm-refresh path on the way in and the
// plain refresh path on the way back out, and either left live would walk the
// operator's real transcript directory from a unit test.
func pastSessionsReadyLauncherModel(t *testing.T) Model {
	t.Helper()
	m := launcherHeldInspectorModel(t)
	m.transcriptIndex = &transcript.Index{}
	m.sessionWarmRefreshCommand = func(int, transcript.Query) tea.Cmd { return nil }
	m.sessionRefreshCommand = func(int) tea.Cmd { return nil }
	m.sessionSearchCommand = func(int, transcript.Query) tea.Cmd { return nil }
	return m
}

// TestLeavingTheLauncherIntoPastSessionsGivesThePaneBack is the third way out of
// the launcher, and the one that does not land on the dashboard. `s` opens Past
// Sessions filtered to the row under the cursor — a sibling surface, not a
// deeper room in the launcher — so the visit is over and the borrow ends with
// it, exactly as it does on esc and on enter.
//
// The dashboard is not on screen when this happens, which is the point rather
// than an objection: Past Sessions has its own esc back to it, and an inspector
// whose snapshot is still sitting in the launcher's stash when that esc lands is
// one the operator can read, scroll, and resume while it is still on loan.
func TestLeavingTheLauncherIntoPastSessionsGivesThePaneBack(t *testing.T) {
	t.Parallel()

	m := pastSessionsReadyLauncherModel(t)
	target, text, window := m.previewWindowID, m.previewText, inspectorWindow(m)
	poll := m.previewPollSeq

	launcher := openLauncherPreview(t, m)
	if launcher.previewWindowID == target {
		t.Fatalf("setup: the launcher never moved the shared pane off window %d", target)
	}

	updated, back := launcher.Update(keyPress(keyProjectSessionSearch))
	m = updated.(Model)
	if m.mode != ModeSessionSearch {
		t.Fatalf("s left mode %v rather than opening Past Sessions", m.mode)
	}
	if m.launcherBorrowedPane != nil {
		t.Fatal("leaving the launcher through Past Sessions left the borrow outstanding")
	}
	if m.previewWindowID != target || m.previewText != text || !m.previewLoaded {
		t.Fatalf("Past Sessions kept the pane the launcher borrowed: window %d→%d, text kept=%v, loaded=%v",
			target, m.previewWindowID, m.previewText == text, m.previewLoaded)
	}
	if !m.inspectorViewport.Paused() {
		t.Fatal("the Past Sessions exit resumed the pane the operator had paused")
	}
	if got := inspectorWindow(m); got != window {
		t.Fatalf("the Past Sessions exit moved the output under the reader:\ngot  %q\nwant %q",
			firstPreviewLine(got), firstPreviewLine(window))
	}

	// The same liveness the other two exits owe. Past Sessions is a surface the
	// operator leaves with esc, and the dashboard behind it must not be waiting on
	// a chain that died when the launcher took the pane.
	if back == nil {
		t.Fatal("leaving the launcher through Past Sessions restarted nothing")
	}
	if m.previewPollSeq == poll {
		t.Fatal("the dashboard came back on the chain the launcher was polling")
	}
	if !m.previewReadInFlight && !m.previewReadPending {
		t.Fatal("the dashboard came back owing no read, so the pane it resumes to is whatever the launcher left")
	}
}

// TestAPastSessionsExitLeavesNoStashForALaterLauncherVisitToRestore is the
// damage the outstanding borrow did, rather than the bookkeeping. A stash that
// outlives the visit is not merely late: the operator reaches the dashboard
// again through Past Sessions' own esc, resumes the pane with End, and the NEXT
// launcher visit hands back the snapshot from before that resume — a pause they
// already cancelled, reinstated by a surface they only passed through.
func TestAPastSessionsExitLeavesNoStashForALaterLauncherVisitToRestore(t *testing.T) {
	t.Parallel()

	m := pastSessionsReadyLauncherModel(t)

	launcher := openLauncherPreview(t, m)
	updated, _ := launcher.Update(keyPress(keyProjectSessionSearch))
	m = updated.(Model)

	m = pressKey(m, keyEsc)
	if m.mode != ModeList {
		t.Fatalf("esc left Past Sessions in mode %v", m.mode)
	}
	if !m.inspectorFocused() {
		t.Fatal("setup: the inspector does not hold the keyboard, so End would move the queue instead")
	}
	m = pressKey(m, keyEnd)
	if m.inspectorViewport.Paused() {
		t.Fatal("setup: End did not resume the pane, so there is no resume to undo")
	}
	tail := inspectorWindow(m)

	// A second visit with no keypress of its own: the launcher's preview is
	// remembered from the first, so opening it borrows and retargets on entry.
	second := pressKey(m, keyL)
	if second.mode != ModePicker {
		t.Fatalf("setup: l opened %v rather than the launcher", second.mode)
	}
	updated, _ = second.Update(keyPress(keyEsc))
	m = updated.(Model)
	if m.inspectorViewport.Paused() {
		t.Fatal("the second launcher visit put back a hold the operator had already resumed")
	}
	if got := inspectorWindow(m); got != tail {
		t.Fatalf("the second launcher visit restored a snapshot from before the resume:\ngot  %q\nwant %q",
			firstPreviewLine(got), firstPreviewLine(tail))
	}
}

// TestADashboardResizeLeavesTheLaunchersRememberedPreviewAlone is the third way
// the launcher used to reach the inspector, and the one with no keypress behind
// it at all. pickerPreviewEnabled is the launcher's remembered visibility and
// outlives the visit that set it, so a resize that consulted it alone let a pane
// three views away and invisible retarget the one on screen.
//
// The configuration is deliberately vertical. Under the horizontal default both
// surfaces clear their floors at the same column count, so there is no terminal
// that the launcher's pane does not fit in and the dashboard's does — and the
// test would be asserting about a resize that changed nothing for either.
func TestADashboardResizeLeavesTheLaunchersRememberedPreviewAlone(t *testing.T) {
	t.Parallel()

	m := launcherHeldInspectorModel(t)
	m.cfg.Defaults.Preview.Direction = "vertical"
	m.cfg.Defaults.Preview.MinRows = 40
	// Remembered from an earlier visit; the launcher itself is not on screen.
	m.pickerPreviewEnabled = true
	target, top := m.previewWindowID, firstPreviewLine(inspectorWindow(m))

	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 160, Height: 40})
	resized := updated.(Model)
	if !resized.inspectorActive() {
		t.Fatal("setup: the resize took the dashboard's own split away, so it proves nothing about the launcher's")
	}
	if previewFits(resized.width, resized.height, footerBudgetLines(resized),
		resized.cfg.Defaults.Preview.MinCols, resized.cfg.Defaults.Preview.MinRows,
		resized.cfg.Defaults.Preview.Direction) {
		t.Fatal("setup: the launcher's pane still fits, so the disable path was never reached")
	}
	if !resized.inspectorViewport.Paused() {
		t.Fatal("a dashboard resize resumed the pane through the launcher's flag")
	}
	if resized.previewWindowID != target {
		t.Fatalf("a dashboard resize retargeted the pane through the launcher's flag: %d→%d",
			target, resized.previewWindowID)
	}
	// The pane's first visible line rather than its whole window: this resize
	// changes the row count, so the window legitimately grows or shrinks. Where
	// the operator is reading FROM is the thing that may not move.
	if got := firstPreviewLine(inspectorWindow(resized)); got != top {
		t.Fatalf("a dashboard resize moved the output under the reader: %q, want %q", got, top)
	}
	if resized.launcherBorrowedPane != nil {
		t.Fatal("a dashboard resize borrowed the pane on behalf of a launcher that is not on screen")
	}
	// The flag itself is untouched: it records what the launcher looked like when
	// the operator last left it, and a terminal they resized while looking at the
	// dashboard is not them changing their mind about the launcher.
	if !resized.pickerPreviewEnabled {
		t.Fatal("a dashboard resize decided something about the launcher's preview")
	}
}
