package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
