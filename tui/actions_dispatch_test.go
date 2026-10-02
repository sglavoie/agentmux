package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/launch"
)

// TestActionsOverlayDispatchClose verifies that pressing `X` from the
// overlay under the legacy confirm flow flips to ModeConfirm. The default
// toast/undo path is covered by undo_test.go.
func TestActionsOverlayDispatchClose(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.cfg.Defaults.ConfirmDestructive = true
	m = openActionsOverlay(t, m)
	m = pressKey(m, "X")
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after X, got %d", m.mode)
	}
	if m.confirmAction != "close" {
		t.Fatalf("expected confirmAction=close, got %q", m.confirmAction)
	}
}

// TestConfirmQCancelsWithoutQuitting pins q as a cancel key in the
// destructive confirmation: it lands exactly where esc does rather than
// closing amx.
func TestConfirmQCancelsWithoutQuitting(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.cfg.Defaults.ConfirmDestructive = true
	m = openActionsOverlay(t, m)
	m = pressKey(m, "X")
	if m.mode != ModeConfirm {
		t.Fatalf("setup: expected ModeConfirm after X, got %d", m.mode)
	}
	viaEsc := pressKey(m, "esc")
	updated, cmd := m.Update(keyPress("q"))
	if cmd != nil {
		if msg := cmd(); msg == (tea.QuitMsg{}) {
			t.Fatal("q in confirm quit the program; want cancel")
		}
	}
	if got := updated.(Model); got.mode != viaEsc.mode || got.mode == ModeConfirm {
		t.Fatalf("q in confirm left mode %d; esc leaves %d", got.mode, viaEsc.mode)
	}
}

func TestActionsOverlayDispatchClearStatus(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "key", WindowID: 42},
	})
	m = openActionsOverlay(t, m)
	updated, cmd := m.Update(keyPress("x"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after x, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected clear-status cmd from overlay x")
	}
}

func TestActionsOverlayDispatchDashboardLaunch(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after overlay C, got %d", m.mode)
	}
	if m.configPickerPurpose != launch.ConfigPickerDashboardLaunch {
		t.Fatalf("expected dashboard-launch purpose after overlay C, got %d", m.configPickerPurpose)
	}
}

// TestActionsOverlayDispatchRestart verifies that `r` from the overlay
// under the legacy confirm flow flips to ModeConfirm.
func TestActionsOverlayDispatchRestart(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.cfg.Defaults.ConfirmDestructive = true
	m = openActionsOverlay(t, m)
	m = pressKey(m, "r")
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after r, got %d", m.mode)
	}
	if m.confirmAction != "restart" {
		t.Fatalf("expected confirmAction=restart, got %q", m.confirmAction)
	}
}

// TestActionsOverlayDispatchConfigPicker verifies `c` opens ModeConfigPicker.
func TestActionsOverlayDispatchConfigPicker(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	m = pressKey(m, "c")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after c, got %d", m.mode)
	}
	if m.configPickerPurpose != launch.ConfigPickerRestartProfile {
		t.Fatalf("expected restart-profile purpose after overlay c, got %d", m.configPickerPurpose)
	}
}

// TestActionsOverlayDispatchPeek verifies pressing `e` on an active row
// closes the overlay and returns a non-nil peek cmd.
func TestActionsOverlayDispatchPeek(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 42},
	})
	m = openActionsOverlay(t, m)
	updated, cmd := m.Update(keyPress("e"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after e, got %d", m2.mode)
	}
	if cmd == nil {
		t.Error("expected peek cmd from overlay e")
	}
}

// TestActionsOverlayPeekUnavailableNoDispatch verifies pressing `e` on a
// non-bolt row is a no-op (overlay stays open, no cmd).
func TestActionsOverlayPeekUnavailableNoDispatch(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "", WindowID: 42},
	})
	m = openActionsOverlay(t, m)
	updated, cmd := m.Update(keyPress("e"))
	m2 := updated.(Model)
	if m2.mode != ModeActions {
		t.Fatalf("expected overlay to stay open, got mode %d", m2.mode)
	}
	if cmd != nil {
		t.Error("expected no cmd when peek is unavailable")
	}
}

// TestActionsOverlayDispatchCopyTitle verifies `y` dispatches a copy cmd.
func TestActionsOverlayDispatchCopyTitle(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	updated, cmd := m.Update(keyPress("y"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after y, got %d", m2.mode)
	}
	if cmd == nil {
		t.Error("expected copy cmd from overlay y")
	}
}

// `y`, `o`, and `O` used to dispatch straight from the list, bypassing the
// overlay that also offered them. The overlay is the only route now, so each of
// the three below asserts the capability where it lives rather than at a key
// that no longer answers; the keys' inertness is asserted once, in
// TestRetiredDirectActionKeysDoNotDispatch.

// TestOpenDirActionOpensWhenConfigMatches verifies the open-dir action resolves
// the target row's config session and dispatches.
func TestOpenDirActionOpensWhenConfigMatches(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt"},
	})
	m2, cmd := runRowAction(t, m, keyO)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after the open-dir action, got %d", m2.mode)
	}
	if cmd == nil {
		t.Error("expected open-dir cmd with a matching config session")
	}
}

// TestOpenDirActionUnavailableWithoutMatchingConfig verifies the entry dims on
// a row whose title isn't in the config, rather than dispatching into nothing.
func TestOpenDirActionUnavailableWithoutMatchingConfig(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, Base: "orphan", Account: "work", State: "bolt"},
	})
	m = openActionsOverlay(t, m)
	for _, a := range actionsFor(m) {
		if a.KeyMatch == keyO && a.Available(m) {
			t.Error("expected open-dir unavailable without a matching config session")
		}
	}
	_, cmd := m.Update(keyPress(keyO))
	if cmd != nil {
		t.Error("expected no cmd when no config session matches")
	}
}

// TestOpenInEditorActionUnavailableWithoutConfig verifies the `O` action
// falls back to unavailable when no config.Session matches the row's project.
func TestOpenInEditorActionUnavailableWithoutConfig(t *testing.T) {
	m := testModel() // no Sessions in cfg
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, Base: "nosuch", Account: "work", State: "bolt"},
	})
	m = openActionsOverlay(t, m)
	for _, a := range actionsFor(m) {
		if a.KeyMatch == keyOpenEditor && a.Available(m) {
			t.Errorf("expected open-in-editor unavailable without matching config session")
		}
	}
}

// TestOpenEditorActionOpensWhenConfigMatches verifies the open-in-editor action
// resolves the target row's config session and dispatches.
func TestOpenEditorActionOpensWhenConfigMatches(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt"},
	})
	m2, cmd := runRowAction(t, m, keyOpenEditor)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after the open-in-editor action, got %d", m2.mode)
	}
	if cmd == nil {
		t.Error("expected open-in-editor cmd with a matching config session")
	}
}
