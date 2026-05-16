package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// testModelWithSessions is defined in model_test.go and seeds the model with
// config sessions matching makeSessions() so per-row actions that resolve a
// config.Session (e.g. `o` open dir) are available.

func openActionsOverlay(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(keyPress("a"))
	out := updated.(Model)
	if out.mode != ModeActions {
		t.Fatalf("expected ModeActions, got %d", out.mode)
	}
	return out
}

// TestRenderActionsOverlayHeader checks the header carries the row's title.
func TestRenderActionsOverlayHeader(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	out := stripAnsi(renderActions(m))
	if !strings.Contains(out, "Actions for ") {
		t.Errorf("expected header prefix; got %q", out)
	}
	if !strings.Contains(out, m.targetSession.Base) {
		t.Errorf("expected target title %q in header; got %q", m.targetSession.Base, out)
	}
}

// TestActionAvailabilityActiveRow verifies the availability matrix on an
// active (bolt) row: peek is available, clear is not, every other action
// is available (open dir requires a matching config session).
func TestActionAvailabilityActiveRow(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", State: "bolt", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	avail := map[string]bool{}
	for _, a := range actionsFor(m) {
		avail[a.keyMatch] = a.available(m)
	}
	want := map[string]bool{
		"enter": true, "e": true, "r": true, "x": true,
		"c": true, "C": false, "o": true, "y": true,
	}
	for k, w := range want {
		if avail[k] != w {
			t.Errorf("active row: action %q availability got %v, want %v", k, avail[k], w)
		}
	}
}

// TestActionAvailabilityAttentionRow verifies the matrix on an attention
// (key) row: peek is unavailable, clear is available.
func TestActionAvailabilityAttentionRow(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", State: "key", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	avail := map[string]bool{}
	for _, a := range actionsFor(m) {
		avail[a.keyMatch] = a.available(m)
	}
	if avail["e"] != false {
		t.Errorf("attention row: peek should be unavailable")
	}
	if avail["C"] != true {
		t.Errorf("attention row: clear should be available")
	}
}

// TestActionAvailabilityIdleRow verifies the matrix on an idle row: both
// peek and clear are unavailable.
func TestActionAvailabilityIdleRow(t *testing.T) {
	m := testModelWithSessions()
	m.idleThreshold = time.Minute
	m.lastActivity = map[int]time.Time{10: time.Now().Add(-time.Hour)}
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", State: "", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	avail := map[string]bool{}
	for _, a := range actionsFor(m) {
		avail[a.keyMatch] = a.available(m)
	}
	if avail["e"] != false {
		t.Errorf("idle row: peek should be unavailable")
	}
	if avail["C"] != false {
		t.Errorf("idle row: clear should be unavailable")
	}
}

// TestOpenDirActionUnavailableWithoutConfig verifies the `o` action falls
// back to unavailable when no config.Session matches the row's Base title.
func TestOpenDirActionUnavailableWithoutConfig(t *testing.T) {
	m := testModel() // no Sessions in cfg
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, Base: "W:nosuch", Account: "work", State: "bolt"},
	})
	m = openActionsOverlay(t, m)
	for _, a := range actionsFor(m) {
		if a.keyMatch == keyO && a.available(m) {
			t.Errorf("expected open dir unavailable without matching config session")
		}
	}
}

// TestActionsOverlayEscReturnsToList verifies esc closes the overlay.
func TestActionsOverlayEscReturnsToList(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc, got %d", m.mode)
	}
}

// TestActionsOverlayQReturnsToList verifies q closes the overlay (not quit).
func TestActionsOverlayQReturnsToList(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	m = pressKey(m, "q")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after q in overlay, got %d", m.mode)
	}
}

// TestActionsOverlayDispatchClose verifies that pressing `x` from the
// overlay under the legacy confirm flow flips to ModeConfirm. The default
// toast/undo path is covered by undo_test.go.
func TestActionsOverlayDispatchClose(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.cfg.Defaults.ConfirmDestructive = true
	m = openActionsOverlay(t, m)
	m = pressKey(m, "x")
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after x, got %d", m.mode)
	}
	if m.confirmAction != "close" {
		t.Fatalf("expected confirmAction=close, got %q", m.confirmAction)
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
}

// TestActionsOverlayDispatchPeek verifies pressing `e` on an active row
// closes the overlay and returns a non-nil peek cmd.
func TestActionsOverlayDispatchPeek(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", State: "bolt", WindowID: 42},
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
		{TabID: 1, Base: "W:api", Account: "work", State: "", WindowID: 42},
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
		{TabID: 1, Base: "W:api", Account: "work", State: "bolt"},
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
		{TabID: 1, Base: "W:orphan", Account: "work", State: "bolt"},
	})
	_, cmd := m.Update(keyPress("o"))
	if cmd != nil {
		t.Error("expected no cmd when no config session matches")
	}
}
