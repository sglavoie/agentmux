package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

// lockedTestModel returns a model preloaded with a single locked session
// at cursor 0 so each direct-key guard test exercises the lock branch
// rather than the empty-list early return.
func lockedTestModel() Model {
	m := testModelWithSessions()
	rs := session.RunningSession{
		TabID:     1,
		WindowID:  10,
		Base:      "api",
		Account:   "work",
		Profile:   "work",
		Directory: "/home/user/api",
		Locked:    true,
	}
	return loadSessions(m, []session.RunningSession{rs})
}

// assertLockedDenial verifies that pressing key on a locked row leaves the
// model in ModeList, surfaces a denial toast, and does not dispatch a
// destructive command. Shared across the r/x/c cases below so each test
// captures the same set of guarantees.
func assertLockedDenial(t *testing.T, key string) {
	t.Helper()
	m := lockedTestModel()
	updated, _ := m.Update(keyPress(key))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("%s on locked row: expected ModeList, got %d", key, m2.mode)
	}
	if len(m2.toasts) != 1 {
		t.Fatalf("%s on locked row: expected 1 denial toast, got %d", key, len(m2.toasts))
	}
	if m2.toasts[0].Severity != toastError {
		t.Errorf("%s on locked row: expected toastError severity, got %d", key, m2.toasts[0].Severity)
	}
	if !strings.Contains(m2.toasts[0].Text, "locked") {
		t.Errorf("%s on locked row: expected toast mentioning lock, got %q", key, m2.toasts[0].Text)
	}
	if len(m2.pendingUndo) != 0 {
		t.Errorf("%s on locked row: expected no pendingUndo, got %d", key, len(m2.pendingUndo))
	}
}

// TestRestartDeniedOnLockedRow verifies a direct r press on a locked row
// short-circuits before the restart cmd is built.
func TestRestartDeniedOnLockedRow(t *testing.T) {
	assertLockedDenial(t, "r")
}

// TestCloseDeniedOnLockedRow verifies a direct x press on a locked row
// short-circuits before the close cmd or undo snapshot is created. Also
// guards against the chip-remove branch swallowing the lock check.
func TestCloseDeniedOnLockedRow(t *testing.T) {
	assertLockedDenial(t, "x")
}

// TestOverrideProfileDeniedOnLockedRow verifies a direct c press on a
// locked row does not open the config picker.
func TestOverrideProfileDeniedOnLockedRow(t *testing.T) {
	m := lockedTestModel()
	updated, _ := m.Update(keyPress("c"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("c on locked row: expected ModeList, got %d", m2.mode)
	}
	if len(m2.toasts) != 1 || m2.toasts[0].Severity != toastError {
		t.Fatalf("c on locked row: expected one toastError, got %+v", m2.toasts)
	}
	if !strings.Contains(m2.toasts[0].Text, "locked") {
		t.Errorf("c on locked row: expected toast mentioning lock, got %q", m2.toasts[0].Text)
	}
}

// TestDirectLockKeyTogglesFromList verifies pressing L from ModeList
// dispatches a toggle command without opening the actions overlay. The cmd
// itself is opaque (it bridges to session.Lock/Unlock via the kitty
// client), so we only assert it was returned and the model stayed put.
func TestDirectLockKeyTogglesFromList(t *testing.T) {
	m := testModelWithSessions()
	rs := session.RunningSession{
		TabID:     1,
		WindowID:  10,
		Base:      "api",
		Account:   "work",
		Profile:   "work",
		Directory: "/home/user/api",
		Locked:    false,
	}
	m = loadSessions(m, []session.RunningSession{rs})

	updated, cmd := m.Update(keyPress("L"))
	m2 := updated.(Model)
	if cmd == nil {
		t.Fatal("L on list mode: expected toggle cmd, got nil")
	}
	if m2.mode != ModeList {
		t.Errorf("L on list mode: expected ModeList preserved, got %d", m2.mode)
	}
	if m2.actionErr != nil {
		t.Errorf("L on list mode: expected actionErr cleared, got %v", m2.actionErr)
	}
}

// TestDirectLockKeyNoOpWhenListEmpty guards against panics when L fires on
// an empty list — the early-return branch must not dereference the cursor.
func TestDirectLockKeyNoOpWhenListEmpty(t *testing.T) {
	m := testModelWithSessions()
	updated, cmd := m.Update(keyPress("L"))
	m2 := updated.(Model)
	if cmd != nil {
		t.Errorf("L on empty list: expected nil cmd, got %v", cmd)
	}
	if m2.mode != ModeList {
		t.Errorf("L on empty list: expected ModeList, got %d", m2.mode)
	}
}

// TestDirectMuteKeyTogglesFromList verifies pressing m from ModeList
// dispatches a toggle command without opening the actions overlay. The cmd
// itself is opaque (it bridges to session.Mute/Unmute via the kitty client),
// so we only assert it was returned and the model stayed put. Mirrors the
// lock-key direct-press contract above.
func TestDirectMuteKeyTogglesFromList(t *testing.T) {
	m := testModelWithSessions()
	rs := session.RunningSession{
		TabID:     1,
		WindowID:  10,
		Base:      "api",
		Account:   "work",
		Profile:   "work",
		Directory: "/home/user/api",
	}
	m = loadSessions(m, []session.RunningSession{rs})

	updated, cmd := m.Update(keyPress("m"))
	m2 := updated.(Model)
	if cmd == nil {
		t.Fatal("m on list mode: expected toggle cmd, got nil")
	}
	if m2.mode != ModeList {
		t.Errorf("m on list mode: expected ModeList preserved, got %d", m2.mode)
	}
	if m2.actionErr != nil {
		t.Errorf("m on list mode: expected actionErr cleared, got %v", m2.actionErr)
	}
}

// TestDirectMuteKeyNoOpWhenListEmpty guards against panics when m fires on
// an empty list — the early-return branch must not dereference the cursor.
func TestDirectMuteKeyNoOpWhenListEmpty(t *testing.T) {
	m := testModelWithSessions()
	updated, cmd := m.Update(keyPress("m"))
	m2 := updated.(Model)
	if cmd != nil {
		t.Errorf("m on empty list: expected nil cmd, got %v", cmd)
	}
	if m2.mode != ModeList {
		t.Errorf("m on empty list: expected ModeList, got %d", m2.mode)
	}
}
