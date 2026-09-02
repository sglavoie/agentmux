package tui

import (
	"testing"

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

// TestListYCopiesTitleDirectly verifies `y` in ModeList copies the cursor
// row's title without opening the overlay.
func TestListYCopiesTitleDirectly(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	updated, cmd := m.Update(keyPress("y"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after y, got %d", m2.mode)
	}
	if cmd == nil {
		t.Error("expected copy cmd from list y")
	}
}

// TestListODirectOpensWhenConfigMatches verifies `o` in ModeList resolves
// the cursor's config session and dispatches an open-dir cmd.
func TestListODirectOpensWhenConfigMatches(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt"},
	})
	updated, cmd := m.Update(keyPress("o"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after o, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Error("expected open-dir cmd from list o with matching config session")
	}
}

// TestListONoOpWithoutMatchingConfig verifies `o` is silent on a row whose
// title isn't in the config.
func TestListONoOpWithoutMatchingConfig(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, Base: "orphan", Account: "work", State: "bolt"},
	})
	_, cmd := m.Update(keyPress("o"))
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

// TestListOpenEditorDirectOpensWhenConfigMatches verifies `O` in ModeList
// resolves the cursor's config session and dispatches an open-in-editor cmd.
func TestListOpenEditorDirectOpensWhenConfigMatches(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt"},
	})
	updated, cmd := m.Update(keyPress("O"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after O, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Error("expected open-in-editor cmd from list O with matching config session")
	}
}

// TestListOpenEditorNoOpWithoutMatchingConfig verifies `O` is silent on a
// row whose title isn't in the config.
func TestListOpenEditorNoOpWithoutMatchingConfig(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, Base: "orphan", Account: "work", State: "bolt"},
	})
	_, cmd := m.Update(keyPress("O"))
	if cmd != nil {
		t.Error("expected no cmd when no config session matches for O")
	}
}
