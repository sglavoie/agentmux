package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file is the inspector's focus and its survival: which pane the keyboard
// is aimed at, the order Escape takes layers away in, what a resize is allowed
// to forget, and which surfaces may not disturb any of it.
//
// model_preview_test.go owns the geometry — the widths and heights a split is
// granted at. These are the behaviours layered on top of that geometry, and they
// fail differently: a floor that is off by one is a pane in the wrong place,
// while a focus rule that is off is a keystroke that went somewhere the operator
// was not looking.

// inspectorFocusModel is a dashboard with sessions, a terminal that affords a
// split, and the inspector actually on screen.
func inspectorFocusModel(t *testing.T) Model {
	t.Helper()
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 160, 50
	requireInspector(t, m)
	return m
}

// TestTabCyclesThePanesAndGivesTheKeyBackWhenThereIsOneOfThem pins both halves
// of Tab's contract. With two panes it is the only way to hand the keyboard
// between them, so it may not be spent on anything else; with one pane there is
// nothing to switch to and the filter chips keep the key they used to own.
func TestTabCyclesThePanesAndGivesTheKeyBackWhenThereIsOneOfThem(t *testing.T) {
	m := inspectorFocusModel(t)
	m.activeFilter = dashboard.Parse("@work")
	m.focusedChipIdx = -1

	m = pressKey(m, keyTab)
	if !m.inspectorFocused() {
		t.Fatal("tab did not move the keyboard into the inspector")
	}
	if m.focusedChipIdx != -1 {
		t.Fatalf("tab reached the chip ring as well: focused chip %d", m.focusedChipIdx)
	}
	m = pressKey(m, keyTab)
	if m.inspectorFocused() {
		t.Fatal("a second tab did not return the keyboard to the queue")
	}
	// Two panes are a cycle of length two, so the reverse spelling is the same
	// move rather than its inverse.
	m = pressKey(m, keyShiftTab)
	if !m.inspectorFocused() {
		t.Fatal("shift+tab did not switch panes")
	}

	// Hiding the pane hands the key back: a dashboard with one pane has nothing
	// to switch to, and the chips are what the key meant before the inspector
	// existed.
	m = pressKey(m, keyInspector)
	if m.inspectorActive() || m.inspectorFocused() {
		t.Fatal("i did not hide the inspector")
	}
	m = pressKey(m, keyTab)
	if m.focusedChipIdx != 0 {
		t.Fatalf("tab with one pane did not reach the chip ring: focused chip %d", m.focusedChipIdx)
	}
}

// TestTabOnATerminalTooNarrowToSplitLeavesTheStandingFocusAlone is the same key
// against the state a resize can leave behind. The focus bit outlives a terminal
// too narrow to honour it, and Tab may neither aim the keys at a pane that is
// not drawn nor quietly spend the operator's standing choice.
func TestTabOnATerminalTooNarrowToSplitLeavesTheStandingFocusAlone(t *testing.T) {
	m := inspectorFocusModel(t)
	m = pressKey(m, keyTab)
	if !m.inspectorFocused() {
		t.Fatal("setup: the inspector does not hold the focus")
	}
	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: splitFloor - 1, Height: 50})
	m = updated.(Model)

	m = pressKey(m, keyTab)
	if m.paneFocus != focusInspector {
		t.Fatal("tab on a queue-only dashboard rewrote the standing pane focus")
	}
	if m.inspectorFocused() {
		t.Fatal("the keys are aimed at a pane that is not on screen")
	}
}

// TestEscapePeelsOverlayThenFullScreenThenFocusThenFilters walks the whole
// ladder in one model, because the ordering is the contract and each rung is
// only meaningful relative to the ones above it. An Escape that cleared the
// filters while an overlay was open, or one that left full screen while the
// help box was in front of it, would each pass a test written about that rung
// alone.
func TestEscapePeelsOverlayThenFullScreenThenFocusThenFilters(t *testing.T) {
	m := inspectorFocusModel(t)
	m.activeFilter = dashboard.Parse("@work")
	m.inspectorDisplay = inspectorFullScreen
	m.paneFocus = focusInspector

	m = pressKey(m, keyQuestion)
	if m.mode != ModeHelp {
		t.Fatalf("setup: ? opened %v, not help", m.mode)
	}
	m = pressKey(m, keyEsc)
	if m.mode != ModeList {
		t.Fatalf("escape left mode %v, want the dashboard", m.mode)
	}
	if !m.inspectorFullScreenActive() {
		t.Fatal("closing the overlay also left full screen")
	}

	m = pressKey(m, keyEsc)
	if m.inspectorFullScreenActive() {
		t.Fatal("escape did not leave full screen")
	}
	if m.inspectorDisplay != inspectorAutomatic {
		t.Fatalf("leaving full screen landed on %v, want automatic", m.inspectorDisplay)
	}
	if m.activeFilter.Raw == "" {
		t.Fatal("leaving full screen cleared the filters as well")
	}

	// Full screen already gave the keys back, so re-aim them to reach the focus
	// rung on its own.
	m = pressKey(m, keyTab)
	if !m.inspectorFocused() {
		t.Fatal("setup: the inspector does not hold the focus")
	}
	m = pressKey(m, keyEsc)
	if m.inspectorFocused() {
		t.Fatal("escape did not return the keyboard to the queue")
	}
	if m.activeFilter.Raw == "" {
		t.Fatal("escape cleared the filters on the same press that moved the focus")
	}
	if !m.inspectorActive() {
		t.Fatal("escape took the pane away rather than the focus")
	}

	m = pressKey(m, keyEsc)
	if len(m.activeFilter.Components()) != 0 {
		t.Fatalf("the last rung did not clear the filters: %q", m.activeFilter.Raw)
	}
}

// TestEscapeOnAQueueOnlyDashboardClearsFiltersRatherThanAStandingFocus is the
// rung the narrow terminal makes reachable. The focus bit survives a terminal
// that cannot honour it, and an Escape spent silently clearing that invisible
// bit would read to the operator as a press that did nothing.
func TestEscapeOnAQueueOnlyDashboardClearsFiltersRatherThanAStandingFocus(t *testing.T) {
	m := inspectorFocusModel(t)
	m.activeFilter = dashboard.Parse("@work")
	m = pressKey(m, keyTab)
	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: splitFloor - 1, Height: 50})
	m = updated.(Model)

	m = pressKey(m, keyEsc)
	if len(m.activeFilter.Components()) != 0 {
		t.Fatalf("escape was spent on the invisible focus: %q", m.activeFilter.Raw)
	}
	if m.paneFocus != focusInspector {
		t.Fatal("escape rewrote the standing pane focus it could not show")
	}
}

// TestResizingAcrossTheFloorPreservesSelectionScopeAndFocus is the round trip
// the work item names. Nothing in the resize path may write intent: a terminal
// that briefly got narrow must not be able to answer "does the operator want an
// inspector, and where were they reading".
func TestResizingAcrossTheFloorPreservesSelectionScopeAndFocus(t *testing.T) {
	m := inspectorFocusModel(t)
	m.activeFilter = dashboard.Parse("@work")
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.rebuildItems()
	m.cursor = 1
	scrollListToCursor(&m)
	m = pressKey(m, keyTab)
	cursor, offset, filter := m.cursor, m.listOffset, m.activeFilter.Raw
	target := m.previewWindowID

	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: splitFloor - 1, Height: 50})
	narrow := updated.(Model)
	if narrow.inspectorActive() || narrow.inspectorFocused() {
		t.Fatal("a terminal below the floor still claims a split")
	}
	if narrow.inspectorDisplay != inspectorAutomatic {
		t.Fatalf("the resize wrote intent: %v", narrow.inspectorDisplay)
	}
	if narrow.cursor != cursor || narrow.activeFilter.Raw != filter {
		t.Fatalf("narrowing moved the selection or the scope: cursor %d filter %q",
			narrow.cursor, narrow.activeFilter.Raw)
	}

	updated, _ = narrow.handleWindowSize(tea.WindowSizeMsg{Width: 160, Height: 50})
	wide := updated.(Model)
	if !wide.inspectorActive() {
		t.Fatal("widening did not restore the split")
	}
	if !wide.inspectorFocused() {
		t.Fatal("widening restored the pane without the focus that was aimed at it")
	}
	if wide.cursor != cursor || wide.listOffset != offset || wide.activeFilter.Raw != filter {
		t.Fatalf("the round trip moved the selection or the scope: cursor %d→%d offset %d→%d filter %q",
			cursor, wide.cursor, offset, wide.listOffset, wide.activeFilter.Raw)
	}
	if wide.previewWindowID != target {
		t.Fatalf("the round trip retargeted the pane: %d→%d", target, wide.previewWindowID)
	}
}

// TestAnExplicitHideSurvivesEveryResize is the other intent. Automatic is a
// state that has made no statement, so the geometry may answer for it; hidden is
// a statement, and a widening is not a reason to overturn it.
func TestAnExplicitHideSurvivesEveryResize(t *testing.T) {
	m := inspectorFocusModel(t)
	m = pressKey(m, keyInspector)
	if m.inspectorDisplay != inspectorHidden {
		t.Fatalf("setup: i left display %v", m.inspectorDisplay)
	}
	for _, size := range []tea.WindowSizeMsg{
		{Width: splitFloor - 1, Height: 50},
		{Width: 240, Height: 60},
		{Width: 160, Height: 12},
		{Width: 160, Height: 50},
	} {
		updated, _ := m.handleWindowSize(size)
		m = updated.(Model)
		if m.inspectorDisplay != inspectorHidden || m.inspectorActive() {
			t.Fatalf("%dx%d overturned the hide: display=%v active=%v",
				size.Width, size.Height, m.inspectorDisplay, m.inspectorActive())
		}
	}
}

// TestInspectorStateSurvivesOverlaysAndOtherRootViews is the preservation item.
// The pane is not torn down and re-read because a taller header was drawn over
// it for a moment, and the way that is guaranteed is that the fit question is
// asked against the dashboard's own chrome whatever mode currently owns the
// terminal. A mode-sensitive answer here would show up as an inspector that
// forgot its scroll position every time the operator opened the palette.
func TestInspectorStateSurvivesOverlaysAndOtherRootViews(t *testing.T) {
	base := inspectorFocusModel(t)
	base = pressKey(base, keyTab)
	base.inspectorViewport = base.inspectorViewport.Top([]string{"one", "two", "three"}, 1)
	if !base.inspectorViewport.Paused() {
		t.Fatal("setup: the viewport is not holding a snapshot")
	}
	wantRows := dashboardInspectorBodyRows(base)

	for _, mode := range []Mode{
		ModeActions, ModeHelp, ModeCommandPalette, ModeConfirm, ModeDetail,
		ModeLiveSessionPicker, ModeWorktrees, ModePlanSearch, ModeSessionSearch, ModeASLRuns,
	} {
		m := base
		m.mode = mode
		if got := dashboardInspectorBodyRows(m); got != wantRows {
			t.Errorf("mode %v measured the split against %d body rows, the dashboard says %d", mode, got, wantRows)
		}
		if !m.inspectorActive() {
			t.Errorf("mode %v took the inspector off screen", mode)
		}
		if !m.inspectorFocused() || m.inspectorDisplay != inspectorAutomatic {
			t.Errorf("mode %v disturbed the focus or the intent: focus=%v display=%v",
				mode, m.paneFocus, m.inspectorDisplay)
		}
		if !m.inspectorViewport.Paused() {
			t.Errorf("mode %v resumed the held viewport", mode)
		}
		if m.previewWindowID != base.previewWindowID {
			t.Errorf("mode %v retargeted the pane: %d", mode, m.previewWindowID)
		}
	}
}

// TestOverlayRoundTripsLeaveTheInspectorExactlyWhereItWas is the same claim
// pressed through the real keys rather than set on the model. Opening and
// closing each overlay is what an operator actually does, and it runs the
// handlers that a mode assignment skips.
func TestOverlayRoundTripsLeaveTheInspectorExactlyWhereItWas(t *testing.T) {
	for _, tc := range []struct {
		name string
		open string
		mode Mode
	}{
		{"help", keyQuestion, ModeHelp},
		{"actions", keyA, ModeActions},
		{"palette", keyColon, ModeCommandPalette},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := inspectorFocusModel(t)
			m = pressKey(m, keyTab)
			before := m.previewWindowID

			m = pressKey(m, tc.open)
			if m.mode != tc.mode {
				t.Fatalf("%q opened %v, want %v", tc.open, m.mode, tc.mode)
			}
			m = pressKey(m, keyEsc)
			if m.mode != ModeList {
				t.Fatalf("escape left mode %v", m.mode)
			}
			if !m.inspectorActive() || m.inspectorDisplay != inspectorAutomatic {
				t.Fatalf("the round trip changed the pane: active=%v display=%v",
					m.inspectorActive(), m.inspectorDisplay)
			}
			if !m.inspectorFocused() {
				t.Fatal("the round trip took the keyboard out of the pane")
			}
			if m.previewWindowID != before {
				t.Fatalf("the round trip retargeted the pane: %d→%d", before, m.previewWindowID)
			}
		})
	}
}

// TestTheLauncherPreviewAndTheDashboardInspectorAreSeparateSurfaces is the
// isolation item. The two panes share a configuration block and nothing else:
// `p` is the launcher's toggle and answers nothing on the dashboard, `i` is the
// dashboard's and answers nothing in the launcher, and neither writes the
// other's state.
func TestTheLauncherPreviewAndTheDashboardInspectorAreSeparateSurfaces(t *testing.T) {
	m := inspectorFocusModel(t)

	// `p` retired its dashboard meaning in this session. Until the project
	// switcher claims the key it answers nothing, and in particular it may not
	// reach the inspector it used to toggle.
	pressed := pressKey(m, keyTogglePreview)
	if pressed.inspectorDisplay != m.inspectorDisplay || !pressed.inspectorActive() {
		t.Fatalf("dashboard p still reaches the inspector: display=%v active=%v",
			pressed.inspectorDisplay, pressed.inspectorActive())
	}
	if pressed.pickerPreviewEnabled != m.pickerPreviewEnabled {
		t.Fatal("dashboard p reached the launcher's preview")
	}

	launcher := pressKey(m, keyL)
	if launcher.mode != ModePicker {
		t.Fatalf("l opened %v, want the launcher", launcher.mode)
	}
	toggled := pressKey(launcher, keyTogglePreview)
	if toggled.pickerPreviewEnabled == launcher.pickerPreviewEnabled {
		t.Fatal("p did not toggle the launcher's own preview")
	}
	if toggled.inspectorDisplay != m.inspectorDisplay {
		t.Fatalf("the launcher's p rewrote the dashboard inspector to %v", toggled.inspectorDisplay)
	}

	// And the dashboard's own key is inert here: `i` in the launcher is the
	// picker's business, not a second way to reach the inspector behind it.
	inLauncher := pressKey(launcher, keyInspector)
	if inLauncher.inspectorDisplay != m.inspectorDisplay {
		t.Fatalf("i inside the launcher rewrote the inspector to %v", inLauncher.inspectorDisplay)
	}
}
