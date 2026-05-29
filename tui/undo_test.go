package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
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

// TestXCloseDispatchesToastByDefault verifies that pressing x in ModeList
// with the default config (confirm_destructive=false) skips ModeConfirm,
// records an undoable snapshot, and pushes the aggregating undo toast.
func TestXCloseDispatchesToastByDefault(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	updated, cmd := m.Update(keyPress("x"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after x, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected a close+toast cmd, got nil")
	}
	if len(m2.pendingUndo) != 1 {
		t.Fatalf("expected 1 pending undo entry, got %d", len(m2.pendingUndo))
	}
	if len(m2.toasts) != 1 {
		t.Fatalf("expected 1 toast after x, got %d", len(m2.toasts))
	}
	if !strings.Contains(m2.toasts[0].Text, "u to undo") {
		t.Errorf("expected toast to advertise undo, got %q", m2.toasts[0].Text)
	}
	if m2.toasts[0].id != undoCloseToastID {
		t.Errorf("expected toast id=%d, got %d", undoCloseToastID, m2.toasts[0].id)
	}
}

// TestXCloseUsesModalWhenConfirmDestructive verifies opt-out: with
// confirm_destructive=true, x reverts to the existing ModeConfirm flow and
// records no undo state.
func TestXCloseUsesModalWhenConfirmDestructive(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	updated, _ := m.Update(keyPress("x"))
	m2 := updated.(Model)
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
		updated, _ := m.Update(keyPress("x"))
		m = updated.(Model)
		// Each press selects the new top-of-list session; the snapshots are
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

// TestUndoLastConsumesNewestEntry verifies u pops the most recent undo
// entry and produces a launch cmd. The aggregating toast updates so the
// remaining undo count stays accurate.
func TestUndoLastConsumesNewestEntry(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	updated, _ := m.Update(keyPress("x"))
	m = updated.(Model)
	first := m.pendingUndo[0].label

	updated, cmd := m.Update(keyPress("u"))
	m = updated.(Model)
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
}

// TestUndoLastWithoutEntriesIsNoOp verifies u with nothing to undo returns
// no cmd and leaves model state untouched. Guards against accidental
// dispatches when the user mashes u out of habit.
func TestUndoLastWithoutEntriesIsNoOp(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	updated, cmd := m.Update(keyPress("u"))
	if cmd != nil {
		t.Errorf("expected no cmd when nothing to undo, got %v", cmd)
	}
	if updated.(Model).mode != ModeList {
		t.Errorf("expected ModeList preserved after no-op undo, got %d", updated.(Model).mode)
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

// TestExpiredUndoEntryRejectedOnU verifies popUndo refuses to revive an
// entry past its deadline. Without this guard, a slow user could press u
// and unintentionally relaunch a session minutes after closing it.
func TestExpiredUndoEntryRejectedOnU(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.pendingUndo = []undoEntry{
		{label: "stale", deadline: time.Now().Add(-time.Second)},
	}
	updated, cmd := m.Update(keyPress("u"))
	if cmd != nil {
		t.Errorf("expected no cmd when only undo entry has expired, got %v", cmd)
	}
	if len(updated.(Model).pendingUndo) != 0 {
		// popUndo also discards stale entries it encounters.
		t.Errorf("expected stale entry dropped, got %d remaining", len(updated.(Model).pendingUndo))
	}
}

// TestTransientCloseSkipsUndo verifies a session whose profile is missing
// from the loaded config still closes but omits the undo affordance —
// there's no relaunch path without the account.
func TestTransientCloseSkipsUndo(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 99, Base: "orphan", Account: "unknown", Profile: "unknown"},
	})
	updated, cmd := m.Update(keyPress("x"))
	m2 := updated.(Model)
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

// TestRestartDispatchesToastByDefault verifies r runs restart immediately
// and pushes a confirmation toast (no undo entry — restart is two-step
// internally and undo would be ambiguous).
func TestRestartDispatchesToastByDefault(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	updated, cmd := m.Update(keyPress("r"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after r, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected restart cmd, got nil")
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

// TestActionsOverlayCloseFiresToast verifies the same close-toast path
// dispatches when the user reaches `x` via the actions overlay (a → x).
func TestActionsOverlayCloseFiresToast(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	updated, cmd := m.Update(keyPress("x"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after x from overlay, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected close+toast cmd from overlay x")
	}
	if len(m2.pendingUndo) != 1 {
		t.Fatalf("expected 1 pending undo, got %d", len(m2.pendingUndo))
	}
}

// TestUKeyRunsUndoFromListMode verifies `u` remains a row-level shortcut now
// that dashboard filtering only starts after /.
func TestUKeyRunsUndoFromListMode(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.pendingUndo = []undoEntry{{label: "phantom", deadline: time.Now().Add(undoWindow)}}
	updated, cmd := m.Update(keyPress("u"))
	m2 := updated.(Model)
	if cmd == nil {
		t.Fatal("expected u to dispatch undo cmd")
	}
	if len(m2.pendingUndo) != 0 {
		t.Errorf("expected pending undo consumed, got %d", len(m2.pendingUndo))
	}
}
