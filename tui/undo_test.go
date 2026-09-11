package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// TestSessionFromRunningSnapshotMatchesRunning verifies the snapshot we
// stash for undo captures the launchable identity (title, directory,
// profile, env, groups) of the running session.
func TestSessionFromRunningSnapshotMatchesRunning(t *testing.T) {
	rs := session.RunningSession{
		Base:      "api",
		Project:   "api",
		Profile:   "work",
		Account:   "work",
		Directory: "/home/user/api",
		Groups:    []string{"backend"},
	}
	snap := session.SessionFromRunning(rs)
	if snap.Title != "api" {
		t.Errorf("Title: got %q, want %q", snap.Title, "api")
	}
	if snap.Directory != "/home/user/api" {
		t.Errorf("Directory: got %q, want %q", snap.Directory, "/home/user/api")
	}
	if snap.EffectiveProfile() != "work" {
		t.Errorf("EffectiveProfile: got %q, want %q", snap.EffectiveProfile(), "work")
	}
	if len(snap.Groups) != 1 || snap.Groups[0] != "backend" {
		t.Errorf("Groups: got %v, want [backend]", snap.Groups)
	}
}

// Close is an actions-overlay entry rather than a dashboard key now, so these
// tests reach `X` through the overlay. The mnemonic is unchanged and so is
// everything the entry does; only the two keystrokes in front of it are new.
// That `X` no longer dispatches from the list is asserted where it belongs, in
// TestRetiredDirectActionKeysDoNotDispatch.

// TestXCloseDispatchesToastByDefault verifies that choosing close with the
// default config (confirm_destructive=false) skips ModeConfirm, records an
// undoable snapshot, and pushes the aggregating undo toast.
func TestXCloseDispatchesToastByDefault(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m2, cmd := runRowAction(t, m, keyClose)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after close, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected a close+toast cmd, got nil")
	}
	if len(m2.pendingUndo) != 1 {
		t.Fatalf("expected 1 pending undo entry, got %d", len(m2.pendingUndo))
	}
	if len(m2.toasts) != 1 {
		t.Fatalf("expected 1 toast after close, got %d", len(m2.toasts))
	}
	// The toast advertises the palette, not `u`: undo is a command now, and a
	// toast naming a retired key would be the only thing on screen claiming it
	// still works. See undoToastText.
	if !strings.Contains(m2.toasts[0].Text, keyColon+" undo") {
		t.Errorf("expected toast to advertise undo, got %q", m2.toasts[0].Text)
	}
	assertStatusBarContains(t, m2, keyColon+" undo")
	if m2.toasts[0].id != undoCloseToastID {
		t.Errorf("expected toast id=%d, got %d", undoCloseToastID, m2.toasts[0].id)
	}
}

// TestXCloseUsesModalWhenConfirmDestructive verifies opt-out: with
// confirm_destructive=true, close reverts to the existing ModeConfirm flow and
// records no undo state.
func TestXCloseUsesModalWhenConfirmDestructive(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m2 := pressRowAction(t, m, keyClose)
	if m2.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm under opt-out, got %d", m2.mode)
	}
	if len(m2.pendingUndo) != 0 {
		t.Errorf("expected no pendingUndo under opt-out, got %d", len(m2.pendingUndo))
	}
}

// TestMultipleClosesAggregateToSingleToast verifies that successive
// destructive actions stack via the addressable undo toast id rather than
// pushing fresh toasts.
func TestMultipleClosesAggregateToSingleToast(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	for range 3 {
		m = pressRowAction(t, m, keyClose)
		// Each pass acts on the row under the cursor; the snapshots are
		// distinct, but the toast collapses into a single slot.
	}
	if len(m.pendingUndo) != 3 {
		t.Fatalf("expected 3 pending undo entries, got %d", len(m.pendingUndo))
	}
	undoToasts := 0
	for _, t := range m.toasts {
		if t.id == undoCloseToastID {
			undoToasts++
		}
	}
	if undoToasts != 1 {
		t.Fatalf("expected exactly 1 aggregating undo toast, got %d (toasts=%v)", undoToasts, m.toasts)
	}
	var aggText string
	for _, t := range m.toasts {
		if t.id == undoCloseToastID {
			aggText = t.Text
		}
	}
	if !strings.Contains(aggText, "3 sessions") {
		t.Errorf("expected aggregated toast to mention count, got %q", aggText)
	}
}

// TestUndoLastConsumesNewestEntry verifies Undo Last Close pops the most recent
// undo entry and produces a launch cmd. The aggregating toast updates so the
// remaining undo count stays accurate.
func TestUndoLastConsumesNewestEntry(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressRowAction(t, m, keyClose)
	first := m.pendingUndo[0].label

	m, cmd := runDashboardCommand(t, m, commands.UndoClose)
	if cmd == nil {
		t.Fatal("expected non-nil launch cmd from undo")
	}
	if len(m.pendingUndo) != 0 {
		t.Fatalf("expected pendingUndo drained after undo, got %d", len(m.pendingUndo))
	}
	var aggText string
	for _, t := range m.toasts {
		if t.id == undoCloseToastID {
			aggText = t.Text
		}
	}
	if !strings.Contains(aggText, "restored "+first) {
		t.Errorf("expected restored toast for %q, got %q", first, aggText)
	}
	assertStatusBarContains(t, m, "restored "+first)
}

// TestUndoLastWithoutEntriesIsOfferedDisabled verifies that with nothing to
// undo the palette says so before the keystroke rather than after. The old `u`
// key could only answer by doing nothing, which on screen is indistinguishable
// from a key that is not bound at all; a dimmed entry carrying its reason is the
// same guard made visible.
func TestUndoLastWithoutEntriesIsOfferedDisabled(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, keyColon)
	if m.mode != ModeCommandPalette {
		t.Fatalf("expected the command palette, got mode %d", m.mode)
	}
	entry := commandByID(t, commands.UndoClose)
	availability := entry.AvailabilityFor(m, m.commandContext)
	if availability.Available {
		t.Fatal("Undo Last Close is available with nothing to undo")
	}
	if availability.Reason != commands.ReasonNothingToUndo {
		t.Errorf("reason: got %q, want %q", availability.Reason, commands.ReasonNothingToUndo)
	}
}

// TestUndoKeyIsRetiredAtTheList is the other half: `u` was undo's direct
// spelling and is unbound rather than rebound, so it does not consume a live
// entry that the palette would happily restore.
func TestUndoKeyIsRetiredAtTheList(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.pendingUndo = []undoEntry{{label: "phantom", deadline: time.Now().Add(undoWindow)}}

	updated, cmd := m.Update(keyPress("u"))
	m2 := updated.(Model)
	if cmd != nil {
		t.Errorf("%q dispatched a command", "u")
	}
	if m2.mode != ModeList {
		t.Errorf("%q left ModeList for mode=%d", "u", m2.mode)
	}
	if len(m2.pendingUndo) != 1 {
		t.Errorf("%q consumed the pending undo entry, %d left", "u", len(m2.pendingUndo))
	}
}

// TestExpiredUndoEntriesAreSwept verifies sweepUndo prunes pending entries
// past their deadline so the in-memory list stays bounded.
func TestExpiredUndoEntriesAreSwept(t *testing.T) {
	m := testModelWithSessions()
	m.pendingUndo = []undoEntry{
		{label: "expired", deadline: time.Now().Add(-time.Second)},
		{label: "live", deadline: time.Now().Add(undoWindow)},
	}
	m.sweepUndo()
	if len(m.pendingUndo) != 1 {
		t.Fatalf("expected 1 entry after sweep, got %d", len(m.pendingUndo))
	}
	if m.pendingUndo[0].label != "live" {
		t.Errorf("expected live entry to survive sweep, got %q", m.pendingUndo[0].label)
	}
}

// TestExpiredUndoEntryIsNeitherOfferedNorRevived verifies the deadline is
// enforced twice: the palette stops offering Undo Last Close once the window
// closes, and undoLastCmd refuses the stale entry anyway. The second half is
// what matters — availability is evaluated when the palette paints, so an entry
// that expires between the paint and the enter must still be refused rather
// than relaunching a session minutes after it was closed.
func TestExpiredUndoEntryIsNeitherOfferedNorRevived(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.pendingUndo = []undoEntry{
		{label: "stale", deadline: time.Now().Add(-time.Second)},
	}

	palette := pressKey(m, keyColon)
	if availability := commandByID(t, commands.UndoClose).AvailabilityFor(palette, palette.commandContext); availability.Available {
		t.Error("Undo Last Close is offered for an entry past its deadline")
	}

	if cmd := m.undoLastCmd(); cmd != nil {
		t.Errorf("expected no cmd when the only undo entry has expired, got %v", cmd)
	}
	if len(m.pendingUndo) != 0 {
		// popUndo also discards stale entries it encounters.
		t.Errorf("expected stale entry dropped, got %d remaining", len(m.pendingUndo))
	}
}

// TestTransientCloseSkipsUndo verifies a session whose profile is missing
// from the loaded config still closes but omits the undo affordance —
// there's no relaunch path without the account.
func TestTransientCloseSkipsUndo(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 99, Base: "orphan", Account: "unknown", Profile: "unknown"},
	})
	m2, cmd := runRowAction(t, m, keyClose)
	if cmd == nil {
		t.Fatal("expected close cmd even for transient session")
	}
	if len(m2.pendingUndo) != 0 {
		t.Errorf("expected no pendingUndo for transient session, got %d", len(m2.pendingUndo))
	}
	if len(m2.toasts) != 1 {
		t.Fatalf("expected single transient toast, got %d", len(m2.toasts))
	}
	if strings.Contains(m2.toasts[0].Text, "undo") {
		t.Errorf("expected toast without undo affordance, got %q", m2.toasts[0].Text)
	}
}

// TestRestartConfirmsUnderConfirmDestructive verifies restart arms the modal
// confirmation flow, with its own consequence text, before dispatching.
//
// The old scoped exception is gone with the key that needed it: a direct `r`
// always confirmed, even under the default confirm_destructive=false, because a
// single keystroke on the dashboard could restart a session with nothing in
// between. Restart is two deliberate presses through the actions overlay now,
// so it follows the operator's configured setting like every other destructive
// entry — TestActionsOverlayRestartStillFiresToastByDefault is the default-config
// half of the pair.
func TestRestartConfirmsUnderConfirmDestructive(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m2, cmd := runRowAction(t, m, keyR)
	if m2.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after restart, got %d", m2.mode)
	}
	if cmd != nil {
		t.Fatal("expected no immediate restart cmd before confirmation")
	}
	if m2.confirmAction != "restart" {
		t.Fatalf("expected confirmAction=restart, got %q", m2.confirmAction)
	}
	if m2.confirmConsequence != restartConsequence {
		t.Fatalf("expected restart consequence, got %q", m2.confirmConsequence)
	}
	if len(m2.pendingUndo) != 0 {
		t.Errorf("expected no pendingUndo from restart, got %d", len(m2.pendingUndo))
	}
	if len(m2.toasts) != 0 {
		t.Fatalf("expected no restart toast before confirmation, got %d", len(m2.toasts))
	}
}

// TestActionsOverlayCloseFiresToast verifies the same close-toast path
// dispatches when the user reaches `X` via the actions overlay (a → X).
func TestActionsOverlayCloseFiresToast(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	updated, cmd := m.Update(keyPress("X"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after X from overlay, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected close+toast cmd from overlay X")
	}
	if len(m2.pendingUndo) != 1 {
		t.Fatalf("expected 1 pending undo, got %d", len(m2.pendingUndo))
	}
}

// TestActionsOverlayRestartStillFiresToastByDefault verifies that under the
// default confirm_destructive=false the overlay's restart dispatches straight
// away and reports itself with a toast.
func TestActionsOverlayRestartStillFiresToastByDefault(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	updated, cmd := m.Update(keyPress("r"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after overlay r, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected restart cmd from overlay r")
	}
	if len(m2.pendingUndo) != 0 {
		t.Errorf("expected no pendingUndo from restart, got %d", len(m2.pendingUndo))
	}
	if len(m2.toasts) != 1 {
		t.Fatalf("expected 1 restart toast, got %d", len(m2.toasts))
	}
	if !strings.Contains(m2.toasts[0].Text, "restarted") {
		t.Errorf("expected restart toast text, got %q", m2.toasts[0].Text)
	}
}

// TestUndoCloseRunsFromTheCommandPalette verifies the surviving route reaches a
// live entry from the dashboard: `:` then the command by name, no close
// required first.
func TestUndoCloseRunsFromTheCommandPalette(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.pendingUndo = []undoEntry{{label: "phantom", deadline: time.Now().Add(undoWindow)}}

	m2, cmd := runDashboardCommand(t, m, commands.UndoClose)
	if cmd == nil {
		t.Fatal("expected Undo Last Close to dispatch an undo cmd")
	}
	if len(m2.pendingUndo) != 0 {
		t.Errorf("expected pending undo consumed, got %d", len(m2.pendingUndo))
	}
}
