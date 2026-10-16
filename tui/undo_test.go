package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// undoTestNow is the instant every deadline in this file is expressed against.
// Nothing here sleeps: the model's clock is frozen at it and moved explicitly
// when a test is about expiry.
var undoTestNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// frozenClock pins the model's render clock, which is the same clock the toast
// TTLs and the recovery deadlines now read.
func frozenClock(m Model, at *time.Time) Model {
	m.now = func() time.Time { return *at }
	return m
}

// undoTestModel is a dashboard with sessions loaded and a frozen clock.
func undoTestModel(at *time.Time) Model {
	return loadSessions(frozenClock(testModelWithSessions(), at), makeSessions())
}

// reportClose answers the close the model has in flight, the way the effect's
// ActionDoneMsg does when Kitty reports back. The request identity is read from
// the controller rather than guessed, which is what makes the completion land on
// the dispatch the test made.
func reportClose(t *testing.T, m Model, completion commands.ActionDoneMsg) Model {
	t.Helper()
	inflight := m.sessionActions.InFlight()
	if len(inflight) != 1 {
		t.Fatalf("in-flight dispatches = %d, want exactly the close", len(inflight))
	}
	completion.Request = uint64(inflight[0].Request)
	updated, _ := m.Update(completion)
	return updated.(Model)
}

// closeAndConfirm is the whole successful path: choose close from the actions
// overlay, then report that the tab was observed to go.
func closeAndConfirm(t *testing.T, m Model) Model {
	t.Helper()
	return reportClose(t, pressRowAction(t, m, keyClose), commands.ActionDoneMsg{})
}

// selectedLabel is what the row under the cursor is called in a sentence. The
// tests below assert against it rather than against a hard-coded title, because
// which of the fixture rows sorts first is a property of the list view and not
// of the close they are about.
func selectedLabel(t *testing.T, m Model) string {
	t.Helper()
	s, ok := m.selectedSession()
	if !ok {
		t.Fatal("no row is selected")
	}
	return dashboard.SessionDisplayTitle(s)
}

// toastByID returns the text carried on an addressable channel.
func toastByID(m Model, id int) string {
	for _, toast := range m.toasts {
		if toast.id == id {
			return toast.Text
		}
	}
	return ""
}

// recoveryWith builds a stack of confirmed closes without dispatching any, for
// the tests whose subject is what `u` does rather than how an entry got there.
func recoveryWith(at time.Time, labels ...string) sessionactions.Recovery {
	r := sessionactions.Recovery{}
	for i, label := range labels {
		request := sessionactions.RequestID(i + 1)
		r = r.Reserve(request, sessionactions.Snapshot{
			Session: config.Session{Title: label, Directory: "/home/user/" + label, Account: "work"},
			Account: config.Account{Name: "work"},
			Label:   label,
		})
		r, _ = r.Confirm(request, at.Add(undoWindow))
	}
	return r
}

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

// TestCloseIsAnnouncedBeforeItIsRecoverable is the premature-success guard at
// the dashboard: the dispatch says what it is doing, and only the confirmed
// completion says it is done and offers the undo.
func TestCloseIsAnnouncedBeforeItIsRecoverable(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	label := selectedLabel(t, m)

	dispatched, cmd := runRowAction(t, m, keyClose)
	if dispatched.mode != ModeList {
		t.Fatalf("expected ModeList after close, got %d", dispatched.mode)
	}
	if cmd == nil {
		t.Fatal("expected a close+toast cmd, got nil")
	}
	if got := dispatched.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("a close was recoverable before it was confirmed: %v", got)
	}
	if len(dispatched.toasts) != 1 || !strings.Contains(dispatched.toasts[0].Text, "closing "+label) {
		t.Fatalf("in-flight toast = %v, want a pending close", dispatched.toasts)
	}
	if strings.Contains(dispatched.toasts[0].Text, "undo") {
		t.Errorf("an undo was advertised for an unconfirmed close: %q", dispatched.toasts[0].Text)
	}

	confirmed := reportClose(t, dispatched, commands.ActionDoneMsg{})
	if got := confirmed.activeUndoEntries(); len(got) != 1 || got[0].Label != label {
		t.Fatalf("recoverable closes after confirmation = %v, want %s", got, label)
	}
	text := toastByID(confirmed, undoCloseToastID)
	if !strings.Contains(text, "closed "+label) || !strings.Contains(text, keyUndoClose+" relaunch") {
		t.Fatalf("recovery toast = %q, want the close and the undo key", text)
	}
	assertStatusBarContains(t, confirmed, keyUndoClose+" relaunch")
	// The per-dispatch channel has nothing left to say once the recovery toast
	// carries the result.
	if got := toastByID(confirmed, actionFeedbackToastID(1)); got != "" {
		t.Errorf("the dispatch channel still holds %q beside the recovery toast", got)
	}
}

// TestUnconfirmedCloseOffersNoRecovery is the false-recovery guard. A close
// whose tab was never observed to go may still be running, and relaunching it
// would produce a second copy rather than an undo.
func TestUnconfirmedCloseOffersNoRecovery(t *testing.T) {
	at := undoTestNow
	m := reportClose(t, pressRowAction(t, undoTestModel(&at), keyClose), commands.ActionDoneMsg{Unconfirmed: true})

	if got := m.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("an unconfirmed close left a recovery behind: %v", got)
	}
	if got := toastByID(m, undoCloseToastID); got != "" {
		t.Errorf("an unconfirmed close raised the recovery toast: %q", got)
	}
	text := toastByID(m, actionFeedbackToastID(1))
	if !strings.Contains(text, "not confirmed") {
		t.Fatalf("outcome toast = %q, want it to say the close was not confirmed", text)
	}
	if strings.Contains(text, "closed api") {
		t.Errorf("an unconfirmed close was reported in the past tense: %q", text)
	}
}

// TestFailedCloseOffersNoRecovery is the other half: an attempt that reached
// Kitty and failed names the failure and leaves nothing to undo.
func TestFailedCloseOffersNoRecovery(t *testing.T) {
	at := undoTestNow
	m := reportClose(t, pressRowAction(t, undoTestModel(&at), keyClose), commands.ActionDoneMsg{Err: errors.New("no such tab")})

	if got := m.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("a failed close left a recovery behind: %v", got)
	}
	text := toastByID(m, actionFeedbackToastID(1))
	if !strings.Contains(text, "close failed") || !strings.Contains(text, "no such tab") {
		t.Fatalf("failure toast = %q, want the verb and the reason", text)
	}
}

// TestCloseIsRecoverableWithConfirmationEnabled covers the second eligible
// route: the operator opted into the modal, answered it, and the close that
// followed is as recoverable as one dispatched straight away.
func TestCloseIsRecoverableWithConfirmationEnabled(t *testing.T) {
	at := undoTestNow
	m := loadSessions(frozenClock(modalConfirmModel(), &at), makeSessions())

	prompted := pressRowAction(t, m, keyClose)
	if prompted.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm under opt-out, got %d", prompted.mode)
	}
	if got := prompted.activeUndoEntries(); len(got) != 0 {
		t.Errorf("a recovery was published before the confirmation was answered: %v", got)
	}

	answered := pressKey(prompted, "y")
	confirmed := reportClose(t, answered, commands.ActionDoneMsg{})
	if got := confirmed.activeUndoEntries(); len(got) != 1 {
		t.Fatalf("recoverable closes after a confirmed close = %v, want one", got)
	}
}

// TestTheWindowStartsWhenTheCloseIsConfirmed: a close that takes two seconds to
// report back still offers the operator thirty seconds of undo.
func TestTheWindowStartsWhenTheCloseIsConfirmed(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	dispatched := pressRowAction(t, m, keyClose)

	at = at.Add(2 * time.Second)
	confirmed := reportClose(t, dispatched, commands.ActionDoneMsg{})

	at = at.Add(undoWindow - time.Millisecond)
	if got := confirmed.activeUndoEntries(); len(got) != 1 {
		t.Fatalf("the entry expired early: the window ran from the dispatch, not the confirmation")
	}
	at = at.Add(time.Millisecond)
	if got := confirmed.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("the entry outlived its deadline: %v", got)
	}
}

// TestMultipleClosesAggregateToSingleToast verifies that successive confirmed
// closes stack behind the addressable recovery toast rather than pushing fresh
// ones.
func TestMultipleClosesAggregateToSingleToast(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	for range 3 {
		// Each pass acts on the row under the cursor; the snapshots are
		// distinct, but the toast collapses into a single slot.
		m = closeAndConfirm(t, m)
	}
	if got := m.activeUndoEntries(); len(got) != 3 {
		t.Fatalf("recoverable closes = %d, want 3", len(got))
	}
	undoToasts := 0
	for _, toast := range m.toasts {
		if toast.id == undoCloseToastID {
			undoToasts++
		}
	}
	if undoToasts != 1 {
		t.Fatalf("expected exactly 1 aggregating undo toast, got %d (toasts=%v)", undoToasts, m.toasts)
	}
	if text := toastByID(m, undoCloseToastID); !strings.Contains(text, "3 sessions") {
		t.Errorf("expected aggregated toast to mention count, got %q", text)
	}
}

// TestUndoLastConsumesNewestEntry verifies Undo Last Close takes the most recent
// entry and produces a launch cmd, and that the entry is gone only once the
// relaunch reports success.
func TestUndoLastConsumesNewestEntry(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	m.recovery = recoveryWith(at, "first", "second")

	claimed, cmd := runDashboardCommand(t, m, commands.UndoClose)
	if cmd == nil {
		t.Fatal("expected non-nil launch cmd from undo")
	}
	if got := claimed.activeUndoEntries(); len(got) != 1 || got[0].Label != "first" {
		t.Fatalf("remaining entries = %v, want first alone", got)
	}
	if text := toastByID(claimed, undoCloseToastID); !strings.Contains(text, "restoring second") {
		t.Fatalf("in-flight toast = %q, want the restore in progress", text)
	}

	updated, _ := claimed.Update(recoveryDoneMsg{request: 1, label: "second"})
	restored := updated.(Model)
	if got := restored.activeUndoEntries(); len(got) != 1 || got[0].Label != "first" {
		t.Fatalf("entries after a successful restore = %v, want first alone", got)
	}
	found := false
	for _, toast := range restored.toasts {
		if strings.Contains(toast.Text, "restored second") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no toast reported the restore: %v", restored.toasts)
	}
}

// TestRepeatedUndoWalksBackThroughRepeatedCloses is the repeated-undo guard: a
// second `u` while the first relaunch is still in flight takes the close below
// it rather than launching the same session twice.
func TestRepeatedUndoWalksBackThroughRepeatedCloses(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	m.recovery = recoveryWith(at, "first", "second")

	first := pressKey(m, keyUndoClose)
	if got := first.activeUndoEntries(); len(got) != 1 || got[0].Label != "first" {
		t.Fatalf("after one undo = %v, want first alone", got)
	}
	second := pressKey(first, keyUndoClose)
	if got := second.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("after two undos = %v, want none left to offer", got)
	}
	third := pressKey(second, keyUndoClose)
	if got := third.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("a third undo found something to claim: %v", got)
	}
}

// TestFailedRelaunchIsRetryableUntilTheOriginalDeadline: a relaunch that failed
// puts the entry back with the time it already had. Retrying is allowed; buying
// more time is not, because the window is about how recent the close is.
func TestFailedRelaunchIsRetryableUntilTheOriginalDeadline(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	m.recovery = recoveryWith(at, "api")

	claimed := pressKey(m, keyUndoClose)
	at = at.Add(time.Second)
	updated, _ := claimed.Update(recoveryDoneMsg{request: 1, label: "api", err: errors.New("launch refused")})
	released := updated.(Model)

	if got := released.activeUndoEntries(); len(got) != 1 || got[0].Label != "api" {
		t.Fatalf("entries after a failed relaunch = %v, want the entry back", got)
	}
	found := false
	for _, toast := range released.toasts {
		if strings.Contains(toast.Text, "restore failed for api") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no toast reported the failed restore: %v", released.toasts)
	}

	at = undoTestNow.Add(undoWindow)
	if got := released.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("a retried entry outlived the original deadline: %v", got)
	}
}

// TestUndoLastWithoutEntriesIsOfferedDisabled verifies that with nothing to
// undo the palette says so before the keystroke rather than after.
func TestUndoLastWithoutEntriesIsOfferedDisabled(t *testing.T) {
	at := undoTestNow
	m := pressKey(undoTestModel(&at), keyColon)
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

// TestUndoKeyAndPaletteAgree: `u` is bound at the dashboard again, and the two
// routes reach the same effect against the same stack. The key exists because
// the affordance lasts thirty seconds — long enough to press one letter, not long
// enough to open a palette and type a name.
func TestUndoKeyAndPaletteAgree(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	m.recovery = recoveryWith(at, "api")

	viaKey, keyCmd := m.Update(keyPress(keyUndoClose))
	viaPalette, paletteCmd := runDashboardCommand(t, m, commands.UndoClose)
	if keyCmd == nil || paletteCmd == nil {
		t.Fatalf("cmds: key=%v palette=%v, want both to dispatch", keyCmd != nil, paletteCmd != nil)
	}
	if got := viaKey.(Model).activeUndoEntries(); len(got) != 0 {
		t.Errorf("`%s` left %d entries, want the claim to have taken it", keyUndoClose, len(got))
	}
	if got := viaPalette.activeUndoEntries(); len(got) != 0 {
		t.Errorf("the palette left %d entries, want the claim to have taken it", len(got))
	}
	if viaKey.(Model).mode != ModeList {
		t.Errorf("`%s` left ModeList for mode=%d", keyUndoClose, viaKey.(Model).mode)
	}
}

// TestUndoKeyIsOrdinaryTextInsideSearchControls: `u` is printable, so every
// surface that accepts typed text keeps it as a character. A key that undid a
// close while the operator was typing a filter would be the worst possible
// place to spend a letter.
func TestUndoKeyIsOrdinaryTextInsideSearchControls(t *testing.T) {
	at := undoTestNow
	base := undoTestModel(&at)
	base.recovery = recoveryWith(at, "api")

	filtering := pressKey(base, keySlash)
	if filtering.mode != ModeSearch {
		t.Fatalf("expected the filter control, got mode %d", filtering.mode)
	}
	typed := pressKey(filtering, keyUndoClose)
	if !strings.Contains(typed.filterText, keyUndoClose) {
		t.Errorf("filter text = %q, want the typed character", typed.filterText)
	}
	if got := typed.activeUndoEntries(); len(got) != 1 {
		t.Errorf("typing in the filter consumed a recovery entry, %d left", len(got))
	}

	palette := pressKey(base, keyColon)
	if palette.mode != ModeCommandPalette {
		t.Fatalf("expected the command palette, got mode %d", palette.mode)
	}
	queried := pressKey(palette, keyUndoClose)
	if !strings.Contains(queried.commandQuery, keyUndoClose) {
		t.Errorf("palette query = %q, want the typed character", queried.commandQuery)
	}
	if got := queried.activeUndoEntries(); len(got) != 1 {
		t.Errorf("typing in the palette consumed a recovery entry, %d left", len(got))
	}
}

// TestExpiredRecoveryIsSwept verifies the tick prunes entries past their
// deadline so the in-memory stack stays bounded.
func TestExpiredRecoveryIsSwept(t *testing.T) {
	at := undoTestNow
	m := frozenClock(testModelWithSessions(), &at)
	m.recovery = recoveryWith(at, "expired")
	m.recovery = m.recovery.Reserve(9, sessionactions.Snapshot{
		Session: config.Session{Title: "live"},
		Account: config.Account{Name: "work"},
		Label:   "live",
	})
	m.recovery, _ = m.recovery.Confirm(9, at.Add(2*undoWindow))

	at = at.Add(undoWindow + time.Second)
	m.sweepRecovery()
	got := m.activeUndoEntries()
	if len(got) != 1 || got[0].Label != "live" {
		t.Fatalf("entries after sweep = %v, want live alone", got)
	}
}

// TestExpiredRecoveryIsNeitherOfferedNorRevived verifies the deadline is
// enforced twice: the palette stops offering Undo Last Close once the window
// closes, and undoLastCmd refuses the stale entry anyway. The second half is
// what matters — availability is evaluated when the palette paints, so an entry
// that expires between the paint and the enter must still be refused rather
// than relaunching a session minutes after it was closed.
func TestExpiredRecoveryIsNeitherOfferedNorRevived(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	m.recovery = recoveryWith(at, "stale")
	at = at.Add(undoWindow + time.Second)

	palette := pressKey(m, keyColon)
	if availability := commandByID(t, commands.UndoClose).AvailabilityFor(palette, palette.commandContext); availability.Available {
		t.Error("Undo Last Close is offered for an entry past its deadline")
	}

	if cmd := m.undoLastCmd(); cmd != nil {
		t.Errorf("expected no cmd when the only undo entry has expired, got %v", cmd)
	}
	if got := m.activeUndoEntries(); len(got) != 0 {
		t.Errorf("expected the stale entry dropped, got %v", got)
	}
}

// TestTransientCloseSkipsUndo verifies a session whose profile is missing
// from the loaded config still closes but omits the undo affordance —
// there's no relaunch path without the account.
func TestTransientCloseSkipsUndo(t *testing.T) {
	at := undoTestNow
	m := loadSessions(frozenClock(testModelWithSessions(), &at), []session.RunningSession{
		{TabID: 99, Base: "orphan", Account: "unknown", Profile: "unknown"},
	})
	dispatched, cmd := runRowAction(t, m, keyClose)
	if cmd == nil {
		t.Fatal("expected close cmd even for transient session")
	}
	closed := reportClose(t, dispatched, commands.ActionDoneMsg{})
	if got := closed.activeUndoEntries(); len(got) != 0 {
		t.Errorf("expected no recovery for a transient session, got %v", got)
	}
	if got := toastByID(closed, undoCloseToastID); got != "" {
		t.Errorf("expected no recovery toast, got %q", got)
	}
	if text := toastByID(closed, actionFeedbackToastID(1)); !strings.Contains(text, "closed orphan") {
		t.Errorf("expected the close still reported, got %q", text)
	}
}

// TestClosingTheHostingTabOffersNoRecovery: the dashboard is standing in that
// tab, so the surface that would show the undo — and the thirty seconds it would
// last — end with the close.
func TestClosingTheHostingTabOffersNoRecovery(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	selected, ok := m.selectedSession()
	if !ok {
		t.Fatal("no row is selected")
	}
	m.actionHost = sessionactions.Host{TabID: selected.TabID}
	label := dashboard.SessionDisplayTitle(selected)

	closed := closeAndConfirm(t, m)
	if got := closed.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("a recovery was published for the tab the dashboard exits with: %v", got)
	}
	if text := toastByID(closed, actionFeedbackToastID(1)); !strings.Contains(text, "closed "+label) {
		t.Errorf("expected the close still reported, got %q", text)
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
	at := undoTestNow
	m := loadSessions(frozenClock(modalConfirmModel(), &at), makeSessions())
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
	if m2.confirmConsequence != wantRestartConsequence {
		t.Fatalf("expected restart consequence, got %q", m2.confirmConsequence)
	}
	if got := m2.activeUndoEntries(); len(got) != 0 {
		t.Errorf("expected no recovery from restart, got %v", got)
	}
	if len(m2.toasts) != 0 {
		t.Fatalf("expected no restart toast before confirmation, got %d", len(m2.toasts))
	}
}

// TestActionsOverlayCloseFiresToast verifies the same close path dispatches when
// the user reaches `X` via the actions overlay (a → X).
func TestActionsOverlayCloseFiresToast(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	m = openActionsOverlay(t, m)
	updated, cmd := m.Update(keyPress("X"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after X from overlay, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected close+toast cmd from overlay X")
	}
	if got := reportClose(t, m2, commands.ActionDoneMsg{}).activeUndoEntries(); len(got) != 1 {
		t.Fatalf("recoverable closes = %v, want one", got)
	}
}

// TestActionsOverlayRestartStillFiresToastByDefault verifies that under the
// default confirm_destructive=false the overlay's restart dispatches straight
// away, says so while it is in flight, and reports the result when it lands.
func TestActionsOverlayRestartStillFiresToastByDefault(t *testing.T) {
	at := undoTestNow
	m := openActionsOverlay(t, undoTestModel(&at))
	updated, cmd := m.Update(keyPress("r"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after overlay r, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected restart cmd from overlay r")
	}
	if len(m2.toasts) != 1 || !strings.Contains(m2.toasts[0].Text, "restarting") {
		t.Fatalf("in-flight toast = %v, want a pending restart", m2.toasts)
	}
	if got := m2.activeUndoEntries(); len(got) != 0 {
		t.Errorf("expected no recovery from restart, got %v", got)
	}

	inflight := m2.sessionActions.InFlight()
	if len(inflight) != 1 {
		t.Fatalf("in-flight dispatches = %d, want the restart", len(inflight))
	}
	done, _ := m2.Update(commands.ActionDoneMsg{Request: uint64(inflight[0].Request)})
	finished := done.(Model)
	if text := toastByID(finished, actionFeedbackToastID(inflight[0].Request)); !strings.Contains(text, "restarted") {
		t.Errorf("expected restart toast text, got %q", text)
	}
}

// TestClearStatusDescribesTheRequest: the daemon's renderer owns the tab title,
// so the dashboard never learns whether the icon changed. It says what it asked
// for rather than claiming a state it cannot see.
func TestClearStatusDescribesTheRequest(t *testing.T) {
	at := undoTestNow
	// Every row carries the attention badge, because clear icon is dimmed on a
	// row with nothing to clear and the overlay helper refuses a dimmed entry.
	rows := makeSessions()
	for i := range rows {
		rows[i].State = "key"
	}
	m := loadSessions(frozenClock(testModelWithSessions(), &at), rows)
	dispatched := pressRowAction(t, m, keyClearStatus)

	inflight := dispatched.sessionActions.InFlight()
	if len(inflight) != 1 {
		t.Fatalf("in-flight dispatches = %d, want the clear", len(inflight))
	}
	done, _ := dispatched.Update(commands.ActionDoneMsg{Request: uint64(inflight[0].Request)})
	text := toastByID(done.(Model), actionFeedbackToastID(inflight[0].Request))
	if !strings.Contains(text, "requested status clear") {
		t.Fatalf("clear-status toast = %q, want it to describe the request", text)
	}
	if strings.Contains(text, "cleared") {
		t.Errorf("clear-status claimed a verified state change: %q", text)
	}
}

// TestUndoCloseRunsFromTheCommandPalette verifies the by-name route reaches a
// live entry from the dashboard: `:` then the command, no close required first.
func TestUndoCloseRunsFromTheCommandPalette(t *testing.T) {
	at := undoTestNow
	m := undoTestModel(&at)
	m.recovery = recoveryWith(at, "phantom")

	m2, cmd := runDashboardCommand(t, m, commands.UndoClose)
	if cmd == nil {
		t.Fatal("expected Undo Last Close to dispatch an undo cmd")
	}
	if got := m2.activeUndoEntries(); len(got) != 0 {
		t.Errorf("expected the entry claimed, got %v", got)
	}
}
