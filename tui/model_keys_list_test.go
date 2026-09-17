package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/launch"
)

// lockedTestModel returns a model preloaded with a single locked session
// at cursor 0 so each guard test exercises the lock branch rather than the
// empty-list early return.
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

// assertLockedDenial verifies that the destructive entry bound to key is refused
// on a locked row. The refusal moved with the keymap: it used to be a direct
// press answered by an error toast, and it is now an overlay entry that draws
// dim and swallows its own key. That is the better shape for the same guarantee
// — the operator is told before pressing rather than after — so what this
// asserts is that the entry reports itself unavailable AND that pressing it
// anyway does nothing.
func assertLockedDenial(t *testing.T, key string) {
	t.Helper()
	m := pressKey(lockedTestModel(), keyA)
	if m.mode != ModeActions {
		t.Fatalf("%s: expected the actions overlay to open, got mode=%d", key, m.mode)
	}
	if !m.targetSession.Locked {
		t.Fatalf("%s: setup: the overlay targeted an unlocked row", key)
	}

	var found bool
	for _, a := range actionsFor(m) {
		if a.KeyMatch != key {
			continue
		}
		found = true
		if a.Available(m) {
			t.Fatalf("%s (%s) is available on a locked row", key, a.Label)
		}
	}
	if !found {
		t.Fatalf("no actions-overlay entry is bound to %q", key)
	}

	updated, cmd := m.Update(keyPress(key))
	m2 := updated.(Model)
	if m2.mode != ModeActions {
		t.Fatalf("%s on locked row: the overlay dispatched and left for mode %d", key, m2.mode)
	}
	if cmd != nil {
		t.Errorf("%s on locked row: expected no command, got one", key)
	}
	if got := m2.activeUndoEntries(); len(got) != 0 {
		t.Errorf("%s on locked row: expected no recovery entry, got %v", key, got)
	}
}

// TestRestartDeniedOnLockedRow verifies restart is refused on a locked row.
func TestRestartDeniedOnLockedRow(t *testing.T) {
	assertLockedDenial(t, keyR)
}

// TestCloseDeniedOnLockedRow verifies close is refused on a locked row before
// the close cmd or undo snapshot is created.
func TestCloseDeniedOnLockedRow(t *testing.T) {
	assertLockedDenial(t, keyClose)
}

// TestOverrideProfileDeniedOnLockedRow verifies profile override is refused on
// a locked row and does not open the config picker.
func TestOverrideProfileDeniedOnLockedRow(t *testing.T) {
	assertLockedDenial(t, keyC)
}

func TestDashboardLaunchAllowedOnLockedRow(t *testing.T) {
	m := pressRowAction(t, lockedTestModel(), keyDashboardLaunch)
	if m.mode != ModeConfigPicker {
		t.Fatalf("launch new on locked row: expected ModeConfigPicker, got %d", m.mode)
	}
	if m.configPickerPurpose != launch.ConfigPickerDashboardLaunch {
		t.Fatalf("launch new on locked row: expected dashboard-launch purpose, got %d", m.configPickerPurpose)
	}
	if len(m.toasts) != 0 {
		t.Fatalf("launch new on locked row: expected no denial toast, got %+v", m.toasts)
	}
}

func TestClearStatusAllowedOnLockedAttentionRow(t *testing.T) {
	m := lockedTestModel()
	// The badge is written into both lists because the shared controller
	// revalidates the captured row against the unfiltered one before it
	// dispatches. A model whose filtered view claimed a badge the source list
	// did not would be refused for exactly that reason, and the refusal would
	// be right — it is the state the dashboard actually has.
	m.filtered[0].State = "key"
	m.sessions[0].State = "key"
	m2, cmd := runRowAction(t, m, keyClearStatus)
	if m2.mode != ModeList {
		t.Fatalf("clear icon on locked row: expected ModeList, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("clear icon on locked row: expected clear-status cmd")
	}
	// The dispatch reports that it is in flight, which is not a denial: a
	// refusal is a warning naming the reason, and this is an info-severity
	// sentence about the request that was made.
	if len(m2.toasts) != 1 || m2.toasts[0].Severity != toastInfo {
		t.Fatalf("clear icon on locked row: expected the in-flight toast alone, got %+v", m2.toasts)
	}
}

// Clear icon has nothing to clear on a row carrying no badge, so the overlay
// dims it. runRowAction refuses a blocked entry, which is what proves the gate.
func TestClearStatusUnavailableOnNonAttentionRow(t *testing.T) {
	m := pressKey(lockedTestModel(), keyA)
	for _, a := range actionsFor(m) {
		if a.KeyMatch != keyClearStatus {
			continue
		}
		if a.Available(m) {
			t.Fatal("clear icon is available on a row with no badge to clear")
		}
		return
	}
	t.Fatalf("no actions-overlay entry is bound to %q", keyClearStatus)
}

// TestLockActionTogglesFromTheOverlay verifies the lock entry dispatches a
// toggle command and returns to the list. The cmd itself is opaque (it bridges
// to session.Lock/Unlock via the kitty client), so we only assert it was
// returned and the overlay closed behind it.
func TestLockActionTogglesFromTheOverlay(t *testing.T) {
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

	m2, cmd := runRowAction(t, m, keyLock)
	if cmd == nil {
		t.Fatal("lock/unlock: expected toggle cmd, got nil")
	}
	if m2.mode != ModeList {
		t.Errorf("lock/unlock: expected the overlay to close back to ModeList, got %d", m2.mode)
	}
	if m2.actionErr != nil {
		t.Errorf("lock/unlock: expected actionErr cleared, got %v", m2.actionErr)
	}
}

// TestMuteActionTogglesFromTheOverlay mirrors the lock contract above.
func TestMuteActionTogglesFromTheOverlay(t *testing.T) {
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

	m2, cmd := runRowAction(t, m, keyM)
	if cmd == nil {
		t.Fatal("mute/unmute: expected toggle cmd, got nil")
	}
	if m2.mode != ModeList {
		t.Errorf("mute/unmute: expected the overlay to close back to ModeList, got %d", m2.mode)
	}
	if m2.actionErr != nil {
		t.Errorf("mute/unmute: expected actionErr cleared, got %v", m2.actionErr)
	}
}

// TestRowActionsAreUnreachableWhenTheListIsEmpty replaces the pair of
// empty-list no-op tests that pressed L and m directly. There is one guard now
// rather than one per key: with no row under the cursor the overlay does not
// open, so no entry in it can be reached at all.
func TestRowActionsAreUnreachableWhenTheListIsEmpty(t *testing.T) {
	m := testModelWithSessions()
	updated, cmd := m.Update(keyPress(keyA))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Errorf("%q on an empty list: expected ModeList, got %d", keyA, m2.mode)
	}
	if cmd != nil {
		t.Errorf("%q on an empty list: expected nil cmd, got %v", keyA, cmd)
	}
}
