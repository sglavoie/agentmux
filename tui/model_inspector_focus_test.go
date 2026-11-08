package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
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

func TestTabMakesTheFocusedPaneVisible(t *testing.T) {
	t.Parallel()

	m := inspectorFocusModel(t)
	before := renderDashboardFrame(m)
	g := dashboardGeometry(m)
	queueFocused := dashboard.RenderList(dashboardListInput(m, g.listWidth, g.listHeight))
	assertHeading := func(want string) {
		t.Helper()
		frame := renderDashboardFrame(m)
		rows := strings.Split(frame.text, "\n")
		heading := rows[frame.hitMap.listPane.Y-1]
		plain := stripAnsi(heading)
		if strings.Count(plain, "· focused") != 1 || !strings.Contains(plain, "> "+want+" · focused") {
			t.Fatalf("ambiguous pane focus: %q", plain)
		}
		if layout.StyledWidth(heading) != m.width || frame.hitMap.listPane != before.hitMap.listPane || frame.hitMap.inspectorPane != before.hitMap.inspectorPane {
			t.Fatal("focus indicator changed pane geometry")
		}
		if strings.Index(plain, "Inspector") < g.listWidth {
			t.Fatalf("inspector heading is not over its pane: %q", plain)
		}
		if !strings.Contains(heading, "\x1b[") {
			t.Fatal("pane heading has no visual styling")
		}
	}
	assertHeading("Sessions")
	m = pressKey(m, keyTab)
	assertHeading("Inspector")
	queueBlurred := dashboard.RenderList(dashboardListInput(m, g.listWidth, g.listHeight))
	if queueFocused == queueBlurred || stripAnsi(queueFocused) != stripAnsi(queueBlurred) {
		t.Fatal("moving focus must change the queue's selection styling, preserving its text")
	}
	m = pressKey(m, keyShiftTab)
	assertHeading("Sessions")
	if got := dashboard.RenderList(dashboardListInput(m, g.listWidth, g.listHeight)); got != queueFocused {
		t.Fatal("returning focus did not restore the queue's selection styling")
	}
}

func TestPaneHeadingFollowsVisibleFocusOnResize(t *testing.T) {
	t.Parallel()

	m := pressKey(inspectorFocusModel(t), keyTab)
	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: splitFloor - 1, Height: 50})
	m = updated.(Model)
	if heading := stripAnsi(renderHeaderSpacer(m)); !strings.Contains(heading, "> Sessions · focused") || strings.Contains(heading, "Inspector") {
		t.Fatalf("hidden inspector still claims focus: %q", heading)
	}
	if dashboardListInput(m, m.width, 20).Blurred {
		t.Fatal("visible queue selection is still blurred after inspector disappears")
	}
	m = pressKey(m, keyInspector)
	if heading := stripAnsi(renderHeaderSpacer(m)); !strings.Contains(heading, "> Inspector · focused") || strings.Contains(heading, "Sessions") {
		t.Fatalf("full-screen inspector lacks focus feedback: %q", heading)
	}
}

// TestTabCyclesThePanesAndIsInertWhenThereIsOneOfThem pins both halves of Tab's
// contract. With two panes it is the only way to hand the keyboard between them,
// so it may not be spent on anything else; with one pane there is nothing to
// switch to and the key answers nothing at all.
//
// The second half used to be the interesting one for a different reason: Tab
// with a single pane handed the key back to the filter-chip ring, which owned it
// before the inspector existed. The ring is retired, so there is no second
// claimant left and the assertion is now that the press changes nothing — which
// is also why the footer only prints `tab switch pane` while a second pane is
// on screen.
func TestTabCyclesThePanesAndIsInertWhenThereIsOneOfThem(t *testing.T) {
	t.Parallel()

	m := inspectorFocusModel(t)
	m.activeFilter = dashboard.Parse("@work")

	m = pressKey(m, keyTab)
	if !m.inspectorFocused() {
		t.Fatal("tab did not move the keyboard into the inspector")
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

	// Hiding the pane leaves nothing to switch to, and with the chip ring gone
	// the key has nowhere else to go: the filter, the cursor, and the mode must
	// all come through the press untouched.
	m = pressKey(m, keyInspector)
	if m.inspectorActive() || m.inspectorFocused() {
		t.Fatal("i did not hide the inspector")
	}
	before := m
	m = pressKey(m, keyTab)
	if m.mode != ModeList || m.cursor != before.cursor || m.activeFilter.Raw != before.activeFilter.Raw {
		t.Fatalf("tab with one pane was spent on something: mode=%v cursor=%d filter=%q",
			m.mode, m.cursor, m.activeFilter.Raw)
	}
	if m.inspectorActive() || m.inspectorFocused() || m.inspectorDisplay != before.inspectorDisplay {
		t.Fatalf("tab with one pane disturbed the hidden pane: active=%v focused=%v display=%v",
			m.inspectorActive(), m.inspectorFocused(), m.inspectorDisplay)
	}
}

// TestTabOnATerminalTooNarrowToSplitLeavesTheStandingFocusAlone is the same key
// against the state a resize can leave behind. The focus bit outlives a terminal
// too narrow to honour it, and Tab may neither aim the keys at a pane that is
// not drawn nor quietly spend the operator's standing choice.
func TestTabOnATerminalTooNarrowToSplitLeavesTheStandingFocusAlone(t *testing.T) {
	t.Parallel()

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

// fullScreenInspectorModel is a dashboard on a terminal too narrow to split,
// with the inspector opened over the whole body by the key that is the only way
// in. Pressing `i` rather than assigning inspectorFullScreen is deliberate: the
// focus the full-screen pane takes on the way in is half of what these tests are
// about.
func fullScreenInspectorModel(t *testing.T) Model {
	t.Helper()
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = splitFloor-1, 24
	if m.inspectorActive() {
		t.Fatal("setup: a terminal below the floor already holds a split")
	}
	m = pressKey(m, keyInspector)
	if !m.inspectorFullScreenActive() {
		t.Fatalf("setup: i left display %v, want full screen", m.inspectorDisplay)
	}
	if !m.inspectorFocused() {
		t.Fatal("setup: the full-screen pane did not take the keyboard")
	}
	return m
}

// TestTabInAFullScreenInspectorKeepsTheKeysOnTheVisiblePane is the other end of
// the rule TestTabOnATerminalTooNarrowToSplitLeavesTheStandingFocusAlone pins.
// That one is a queue with no inspector to reach; this is an inspector with no
// queue to reach, and the cycle is a cycle of length one in both.
//
// The failure it forbids is silent by construction. Handing the keys to a queue
// that is not drawn leaves the operator looking at an inspector while `j` walks
// the selection behind it — so the assertion is on the SELECTION rather than on
// the focus bit alone, because a moved cursor is the damage a repaired focus
// rule exists to prevent.
func TestTabInAFullScreenInspectorKeepsTheKeysOnTheVisiblePane(t *testing.T) {
	t.Parallel()

	for _, key := range []string{keyTab, keyShiftTab} {
		t.Run(key, func(t *testing.T) {
			m := fullScreenInspectorModel(t)
			before := m.cursor

			m = pressKey(m, key)
			if !m.inspectorFocused() {
				t.Fatalf("%s handed the keyboard to the hidden queue", key)
			}
			if !m.inspectorFullScreenActive() {
				t.Fatalf("%s left full screen: display=%v", key, m.inspectorDisplay)
			}

			// The pane is still the one taking movement keys, which is the thing an
			// operator would notice: j scrolls output and does not walk a list they
			// cannot see.
			m = pressKey(m, keyJ)
			if m.cursor != before {
				t.Fatalf("%s then j moved the hidden selection %d→%d", key, before, m.cursor)
			}
			if m.mode != ModeList {
				t.Fatalf("%s then j left mode %v", key, m.mode)
			}
		})
	}
}

// TestFullScreenStillLeavesTheSplitAndItsPaneSwitchingIntact is the guard on the
// repair's blast radius. focusPane now refuses one target as well as the other,
// and the way that could go wrong is by refusing too much: Escape has to give
// the keys back on the way out of full screen, and Tab has to keep working once
// two panes are on screen again.
func TestFullScreenStillLeavesTheSplitAndItsPaneSwitchingIntact(t *testing.T) {
	t.Parallel()

	m := fullScreenInspectorModel(t)

	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 160, Height: 50})
	m = updated.(Model)
	m = pressKey(m, keyEsc)
	if m.inspectorFullScreenActive() || m.inspectorDisplay != inspectorAutomatic {
		t.Fatalf("escape left full screen for %v, want automatic", m.inspectorDisplay)
	}
	if m.inspectorFocused() {
		t.Fatal("escape left the keyboard in the pane it just shrank")
	}
	if !m.inspectorActive() {
		t.Fatal("a terminal above the floor did not land on the split")
	}

	m = pressKey(m, keyTab)
	if !m.inspectorFocused() {
		t.Fatal("tab no longer reaches the inspector on a real split")
	}
	m = pressKey(m, keyTab)
	if m.inspectorFocused() {
		t.Fatal("tab no longer returns the keyboard to a visible queue")
	}
}

// TestEscapePeelsOverlayThenFullScreenThenFocusThenFilters walks the whole
// ladder in one model, because the ordering is the contract and each rung is
// only meaningful relative to the ones above it. An Escape that cleared the
// filters while an overlay was open, or one that left full screen while the
// help box was in front of it, would each pass a test written about that rung
// alone.
func TestEscapePeelsOverlayThenFullScreenThenFocusThenFilters(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
