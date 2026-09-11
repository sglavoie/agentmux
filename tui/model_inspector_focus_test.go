package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/transcript"
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

// heldInspectorModel is a dashboard whose inspector is focused, carries a real
// snapshot, and has been scrolled to the top of it — a paused pane with a place
// in the output worth losing.
//
// It is built on previewReadModel rather than inspectorFocusModel because the
// selection has to be able to MOVE to a different window while the pane is gone:
// the sessions there carry distinct window IDs, so "the target changed" and "the
// target did not" are two states a test can actually be in.
func heldInspectorModel(t *testing.T) Model {
	t.Helper()
	m := previewReadModel(t)
	m = pressKey(m, keyTab)
	if !m.inspectorFocused() {
		t.Fatal("setup: the inspector does not hold the keyboard")
	}

	var sb strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&sb, "output line %03d\n", i)
	}
	updated, _ := m.Update(previewContentMsg{
		targetSeq: m.previewTargetSeq, requestSeq: m.previewRequestSeq,
		windowID: m.previewWindowID, text: sb.String(),
	})
	m = updated.(Model)
	if !m.previewLoaded || m.previewText == "" {
		t.Fatalf("setup: the snapshot did not land (loaded=%v)", m.previewLoaded)
	}

	m = pressKey(m, keyHome)
	if !m.inspectorViewport.Paused() {
		t.Fatal("setup: home did not hold the snapshot")
	}
	return m
}

// inspectorWindow is the output the pane is actually showing, which is what an
// operator would see move under them.
func inspectorWindow(m Model) string {
	return strings.Join(m.inspectorViewport.Window(dashboard.SplitOutput(m.previewText), m.inspectorOutputRows()), "\n")
}

// TestAWidthRoundTripLeavesAHeldInspectorHeld is the resize half of the hold
// contract. The pane pauses on an upward scroll and resumes on End or on a
// changed selection — a geometry event is neither, so a split that fell below
// its floors and came back may not quietly hand the operator the tail of a
// snapshot they had scrolled away from.
//
// The assertion is on the visible WINDOW rather than on the Paused bit alone.
// The bit is what the pane reports; the window is the damage — the operator's
// place in the output — and a repair that kept the flag while dropping the held
// copy would pass a test written about the flag.
func TestAWidthRoundTripLeavesAHeldInspectorHeld(t *testing.T) {
	m := heldInspectorModel(t)
	target, text, window := m.previewWindowID, m.previewText, inspectorWindow(m)
	poll := m.previewPollSeq

	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 24})
	narrow := updated.(Model)
	if narrow.inspectorActive() {
		t.Fatal("setup: a terminal below the floor still holds a split")
	}

	updated, cmd := narrow.handleWindowSize(tea.WindowSizeMsg{Width: 160, Height: 50})
	wide := updated.(Model)
	if !wide.inspectorActive() {
		t.Fatal("widening did not restore the split")
	}
	if !wide.inspectorViewport.Paused() {
		t.Fatal("the round trip resumed the pane the operator had paused")
	}
	if got := inspectorWindow(wide); got != window {
		t.Fatalf("the round trip moved the output under the reader:\ngot  %q\nwant %q",
			firstPreviewLine(got), firstPreviewLine(window))
	}
	if wide.previewWindowID != target || wide.previewText != text || !wide.previewLoaded {
		t.Fatalf("the round trip treated an unchanged target as a new one: window %d→%d, text kept=%v, loaded=%v",
			target, wide.previewWindowID, wide.previewText == text, wide.previewLoaded)
	}

	// Keeping the hold may not cost the pane its liveness: the polling chain died
	// the first time a tick found the split gone, so the widening owes a fresh
	// chain and a read, and an operator who presses End must land on live output
	// rather than on a pane nothing is feeding.
	if cmd == nil {
		t.Fatal("widening restored the pane without restarting anything")
	}
	if wide.previewPollSeq == poll {
		t.Fatal("widening left the pane with the polling chain the shrink retired")
	}
	if !wide.previewReadInFlight && !wide.previewReadPending {
		t.Fatal("widening owes no read, so the resumed pane would show the snapshot it went away with")
	}
}

// TestAWidthRoundTripStillResetsTheHoldWhenTheSelectionMoved is the other half,
// and the one that keeps the repair from being "never resume". The queue is
// still on screen below the floor, so the operator can walk it while the pane is
// gone; coming back to a different session with the previous session's snapshot
// held would show one session's output under another's metadata.
func TestAWidthRoundTripStillResetsTheHoldWhenTheSelectionMoved(t *testing.T) {
	m := heldInspectorModel(t)
	target := m.previewWindowID

	updated, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 24})
	narrow := updated.(Model)
	if narrow.inspectorActive() {
		t.Fatal("setup: a terminal below the floor still holds a split")
	}
	// The focus bit still says inspector, but the pane is not drawn, so the
	// movement keys are the queue's again — which is how an operator arrives at
	// another session without ever seeing the inspector.
	narrow = selectLine(t, narrow, "session:web")

	updated, _ = narrow.handleWindowSize(tea.WindowSizeMsg{Width: 160, Height: 50})
	wide := updated.(Model)
	if wide.previewWindowID == target {
		t.Fatalf("widening did not follow the selection that moved while the pane was gone: still %d", target)
	}
	if wide.inspectorViewport.Paused() {
		t.Fatal("the new selection inherited the previous session's hold")
	}
	if wide.previewText != "" || wide.previewLoaded {
		t.Fatalf("the new target kept the previous session's snapshot: loaded=%v text=%q",
			wide.previewLoaded, firstPreviewLine(wide.previewText))
	}
}

// launcherHeldInspectorModel is heldInspectorModel with a launcher that has
// somewhere else to point: one configured project bound to a running session
// that is NOT the one the inspector is reading.
//
// The two window IDs being different is the whole setup. A launcher whose row
// resolved to the same window would move the shared pane to where it already
// was, and every assertion below about "the launcher took the pane" would pass
// on a model where nothing was ever taken.
func launcherHeldInspectorModel(t *testing.T) Model {
	t.Helper()
	m := heldInspectorModel(t)
	m.cfg.Sessions = []config.Session{{Title: "web", Account: "work"}}
	return m
}

// openLauncherPreview presses `l` then `p` and answers the read that opening
// the launcher's pane dispatches, leaving the launcher on screen holding the
// shared pane.
func openLauncherPreview(t *testing.T, m Model) Model {
	t.Helper()
	m = pressKey(m, keyL)
	if m.mode != ModePicker {
		t.Fatalf("setup: l opened %v rather than the launcher", m.mode)
	}
	updated, cmd := m.Update(keyPress(keyTogglePreview))
	m = updated.(Model)
	if !m.pickerPreviewEnabled {
		t.Fatal("setup: p did not open the launcher's preview")
	}
	settlePreviewRead(t, &m, cmd)
	return m
}

// TestALauncherVisitLeavesAHeldInspectorHeld is the launcher half of the hold
// contract, and the same claim TestAWidthRoundTripLeavesAHeldInspectorHeld makes
// about a resize. Opening the launcher, using its preview, and coming back to the
// same dashboard selection is not a statement about what the operator wants to
// read; it may not resume a pane they paused.
//
// The launcher's own pane is why this is not the resize case over again. A resize
// takes the pane away; the launcher POINTS IT SOMEWHERE ELSE and puts its own
// content on it, so the dashboard's snapshot is gone rather than merely
// unattended, and coming back means putting it back rather than leaving it alone.
func TestALauncherVisitLeavesAHeldInspectorHeld(t *testing.T) {
	m := launcherHeldInspectorModel(t)
	target, text, window := m.previewWindowID, m.previewText, inspectorWindow(m)
	poll := m.previewPollSeq

	launcher := openLauncherPreview(t, m)
	if launcher.previewWindowID == target {
		t.Fatalf("setup: the launcher never moved the shared pane off window %d", target)
	}

	updated, back := launcher.Update(keyPress(keyEsc))
	m = updated.(Model)
	if m.mode != ModeList {
		t.Fatalf("esc left mode %v", m.mode)
	}
	if !m.inspectorViewport.Paused() {
		t.Fatal("the launcher visit resumed the pane the operator had paused")
	}
	if got := inspectorWindow(m); got != window {
		t.Fatalf("the launcher visit moved the output under the reader:\ngot  %q\nwant %q",
			firstPreviewLine(got), firstPreviewLine(window))
	}
	if m.previewWindowID != target || m.previewText != text || !m.previewLoaded {
		t.Fatalf("the launcher kept the pane it borrowed: window %d→%d, text kept=%v, loaded=%v",
			target, m.previewWindowID, m.previewText == text, m.previewLoaded)
	}
	if m.launcherBorrowedPane != nil {
		t.Fatal("leaving the launcher left the borrow outstanding")
	}

	// Giving the hold back may not cost the pane its liveness, for the same
	// reason the resize may not: the chain the launcher opened was the
	// launcher's, and an operator who presses End must land on live output.
	if back == nil {
		t.Fatal("leaving the launcher restarted nothing")
	}
	if m.previewPollSeq == poll {
		t.Fatal("the dashboard came back on the chain the launcher was polling")
	}
	if !m.previewReadInFlight && !m.previewReadPending {
		t.Fatal("the dashboard came back owing no read, so the pane it resumes to is whatever the launcher left")
	}
}

// TestTurningTheLauncherPreviewOffAndOnAgainLeavesAHeldInspectorHeld is the same
// claim through the other spelling of the visit. Turning the launcher's pane OFF
// looks like giving it back and is not: it points the shared target at nothing,
// and the inspector behind the launcher is pointed at that same target.
func TestTurningTheLauncherPreviewOffAndOnAgainLeavesAHeldInspectorHeld(t *testing.T) {
	m := launcherHeldInspectorModel(t)
	target, window := m.previewWindowID, inspectorWindow(m)

	launcher := openLauncherPreview(t, m)
	updated, cmd := launcher.Update(keyPress(keyTogglePreview))
	launcher = updated.(Model)
	if launcher.pickerPreviewEnabled {
		t.Fatal("setup: the second p did not close the launcher's preview")
	}
	settlePreviewRead(t, &launcher, cmd)

	updated, _ = launcher.Update(keyPress(keyEsc))
	m = updated.(Model)
	if !m.inspectorViewport.Paused() {
		t.Fatal("closing the launcher's preview resumed the dashboard's")
	}
	if got := inspectorWindow(m); got != window || m.previewWindowID != target {
		t.Fatalf("closing the launcher's preview took the dashboard's target with it: window %d→%d\ngot  %q",
			target, m.previewWindowID, firstPreviewLine(got))
	}
}

// TestTheLauncherPreviewShowsItsOwnTargetWhileItHoldsThePane is the other done
// criterion, and the guard against the cheap repair. Leaving the dashboard's
// snapshot on the shared fields while the launcher navigates would preserve the
// hold perfectly and paint one session's output as another's: the launcher's
// pane is an answer about the row under ITS cursor.
func TestTheLauncherPreviewShowsItsOwnTargetWhileItHoldsThePane(t *testing.T) {
	m := launcherHeldInspectorModel(t)
	dashboardTarget := m.previewWindowID

	launcher := openLauncherPreview(t, m)
	if launcher.previewWindowID == dashboardTarget || launcher.previewWindowID == 0 {
		t.Fatalf("the launcher's preview did not follow its own row: window %d", launcher.previewWindowID)
	}
	if launcher.inspectorViewport.Paused() {
		t.Fatal("the launcher's pane inherited the dashboard's hold, so its own output would not scroll")
	}
	if got := stripAnsi(renderPicker(launcher)); strings.Contains(got, "output line 001") {
		t.Fatalf("the launcher drew the dashboard's held snapshot as its own content:\n%s", got)
	}
	if launcher.launcherBorrowedPane == nil {
		t.Fatal("the launcher moved the shared pane without recording the borrow")
	}
}

// TestALauncherVisitStillResetsTheHoldWhenTheSelectionMoved keeps the repair
// from being "always put it back". The dashboard's selection can move while the
// launcher is on screen — a topology refresh reorders the queue under it — and a
// restore that ignored that would hand the new session the old one's snapshot.
func TestALauncherVisitStillResetsTheHoldWhenTheSelectionMoved(t *testing.T) {
	m := launcherHeldInspectorModel(t)
	target := m.previewWindowID

	launcher := openLauncherPreview(t, m)
	launcher = selectLine(t, launcher, "session:web")

	updated, _ := launcher.Update(keyPress(keyEsc))
	m = updated.(Model)
	if m.previewWindowID == target {
		t.Fatalf("leaving the launcher did not follow the selection that moved while it was away: still %d", target)
	}
	if m.inspectorViewport.Paused() {
		t.Fatal("the new selection inherited the previous session's hold")
	}
	if m.previewText != "" || m.previewLoaded {
		t.Fatalf("the new target kept a snapshot that was never read for it: loaded=%v text=%q",
			m.previewLoaded, firstPreviewLine(m.previewText))
	}
}

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
