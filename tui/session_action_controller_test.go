package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// The confirmation wording is no longer declared in this package: both dashboard
// entry points reach it through the shared controller, so it is stated once in
// tui/sessionactions beside the verbs it pairs with. These read it back rather
// than restating it, which is what keeps a copy edit there from needing an edit
// here to stay honest.
var (
	wantCloseConsequence   = mustConsequence(commands.CloseSession)
	wantRestartConsequence = mustConsequence(commands.RestartSession)
)

func mustConsequence(id commands.ID) string {
	prompt, ok := sessionactions.ConfirmPromptFor(id, "")
	if !ok {
		panic("no confirmation prompt declared for " + string(id))
	}
	return prompt.Consequence
}

// sessionActionModel is a dashboard holding one row, with confirm_destructive
// as the case asks for it.
func sessionActionModel(confirms bool, rows ...session.RunningSession) Model {
	m := testModelWithSessions()
	m.client = newUITestKittyClient(nil)
	m.width = 160
	m.height = 50
	m.cfg.Defaults.ConfirmDestructive = confirms
	return loadSessions(m, rows)
}

func sessionActionRow() session.RunningSession {
	return session.RunningSession{
		TabID: 22, WindowID: 202, Base: "api", Project: "api", Profile: "work",
		LaunchID: "launch-1", HarnessSessionID: "sess-1",
	}
}

// TestBothSurfacesShareConfirmationPolicy is the regression the shared
// controller exists for: the palette used to confirm every restart while the
// per-row menu honored confirm_destructive, so the same act had two answers
// depending on how it was reached.
func TestBothSurfacesShareConfirmationPolicy(t *testing.T) {
	row := sessionActionRow()
	for _, tc := range []struct {
		name     string
		confirms bool
		wantMode Mode
	}{
		{name: "confirm_destructive on", confirms: true, wantMode: ModeConfirm},
		{name: "confirm_destructive off", confirms: false, wantMode: ModeList},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viaPalette := sessionActionModel(tc.confirms, row)
			ctx := captureCommandContext(viaPalette)
			palette, _ := commandByID(t, commands.RestartSession).Run(viaPalette, ctx)

			viaMenu := sessionActionModel(tc.confirms, row)
			viaMenu.targetSession = row
			menu, _ := rowActionRunners()[commands.RestartSession](viaMenu)

			if palette.mode != tc.wantMode || menu.mode != tc.wantMode {
				t.Fatalf("palette mode = %v, menu mode = %v, want %v for both", palette.mode, menu.mode, tc.wantMode)
			}
			if palette.confirmAction != menu.confirmAction {
				t.Fatalf("confirm action: palette %q, menu %q", palette.confirmAction, menu.confirmAction)
			}
			if palette.confirmConsequence != menu.confirmConsequence {
				t.Fatalf("consequence: palette %q, menu %q", palette.confirmConsequence, menu.confirmConsequence)
			}
		})
	}
}

// TestConfirmedCloseRunsTheSameEffectAsAnUnconfirmedOne is the other drift the
// controller removed. Answering the prompt used to dispatch a raw close from the
// confirm handler, so a confirmed close recorded no undo entry and raised no
// toast while an unconfirmed one did both.
func TestConfirmedCloseRunsTheSameEffectAsAnUnconfirmedOne(t *testing.T) {
	row := sessionActionRow()

	direct := sessionActionModel(false, row)
	direct.targetSession = row
	direct, directCmd := rowActionRunners()[commands.CloseSession](direct)

	confirmed := sessionActionModel(true, row)
	confirmed.targetSession = row
	confirmed, cmd := rowActionRunners()[commands.CloseSession](confirmed)
	if confirmed.mode != ModeConfirm || cmd != nil {
		t.Fatalf("setup: mode = %v, cmd = %v, want a prompt and no dispatch", confirmed.mode, cmd != nil)
	}
	updated, confirmedCmd := confirmed.Update(keyPress(keyY))
	confirmed = updated.(Model)

	if directCmd == nil || confirmedCmd == nil {
		t.Fatalf("commands: direct = %v, confirmed = %v, want both", directCmd != nil, confirmedCmd != nil)
	}
	if len(confirmed.sessionActions.InFlight()) != len(direct.sessionActions.InFlight()) {
		t.Fatalf("in-flight dispatches: direct %d, confirmed %d", len(direct.sessionActions.InFlight()), len(confirmed.sessionActions.InFlight()))
	}
	if len(confirmed.toasts) != len(direct.toasts) {
		t.Fatalf("toasts: direct %v, confirmed %v", direct.toasts, confirmed.toasts)
	}
	if confirmed.mode != ModeList {
		t.Fatalf("mode after confirming = %v, want %v", confirmed.mode, ModeList)
	}
	if confirmed.sessionActions.Busy() {
		t.Fatal("controller still busy after the confirmation was answered")
	}
}

// TestCancellingReturnsToTheActionsOverlay is the return-navigation contract at
// the menu entry point: esc out of the nested confirmation puts the operator
// back in the menu, on the row and at the scroll position they chose from.
func TestCancellingReturnsToTheActionsOverlay(t *testing.T) {
	row := sessionActionRow()
	m := sessionActionModel(true, row)
	m.targetSession = row
	m.actionsOffset = 0

	m, cmd := rowActionRunners()[commands.CloseSession](m)
	if m.mode != ModeConfirm || cmd != nil {
		t.Fatalf("setup: mode = %v, cmd = %v", m.mode, cmd != nil)
	}
	updated, _ := m.Update(keyPress(keyEsc))
	m = updated.(Model)

	if m.mode != ModeActions {
		t.Fatalf("mode = %v, want %v", m.mode, ModeActions)
	}
	if m.targetSession.WindowID != row.WindowID {
		t.Fatalf("target window = %d, want the row the action was chosen on (%d)", m.targetSession.WindowID, row.WindowID)
	}
	if m.confirmAction != "" || m.confirmConsequence != "" {
		t.Fatalf("prompt state survived the cancellation: action=%q consequence=%q", m.confirmAction, m.confirmConsequence)
	}
	if m.sessionActions.Busy() {
		t.Fatal("controller still busy after a cancellation")
	}
}

// TestCancellingReturnsToThePaletteQuery is the same contract at the palette
// entry point. The palette tears its query down before the command executes, so
// the query, cursor, and scroll position are restored from the captured origin
// rather than from anything left in the model.
func TestCancellingReturnsToThePaletteQuery(t *testing.T) {
	row := sessionActionRow()
	m := sessionActionModel(true, row)
	m.mode = ModeCommandPalette
	m.commandContext = captureCommandContext(m)
	m.commandQuery = "rest"
	m.commandResults = commands.PaletteResults(commandCatalog(), m, m.commandContext, m.commandQuery)
	if len(m.commandResults) == 0 {
		t.Fatal("setup: no palette results for query \"rest\"")
	}
	m.commandCursor = paletteIndexOf(t, m, commands.RestartSession)

	updated, _ := m.Update(keyPress(keyEnter))
	m = updated.(Model)
	if m.mode != ModeConfirm {
		t.Fatalf("setup: mode = %v, want %v", m.mode, ModeConfirm)
	}
	if m.commandQuery != "" {
		t.Fatalf("setup: palette query %q survived execution; the restore would be reading live state", m.commandQuery)
	}

	updated, _ = m.Update(keyPress(keyEsc))
	m = updated.(Model)

	if m.mode != ModeCommandPalette {
		t.Fatalf("mode = %v, want %v", m.mode, ModeCommandPalette)
	}
	if m.commandQuery != "rest" {
		t.Fatalf("query = %q, want the restored \"rest\"", m.commandQuery)
	}
	if got := paletteIndexOf(t, m, commands.RestartSession); m.commandCursor != got {
		t.Fatalf("cursor = %d, want the restart row at %d", m.commandCursor, got)
	}
}

// TestCancellingRestoresThePaletteSelectionByIdentity is why the captured
// origin carries a command ID and not only a row number.
//
// The palette's results are recomputed on restore, against a model that has
// moved on while the nested interaction was up. A command that became visible
// above the selection shifts every row below it, so the index the operator was
// on now names a different command — and putting them back on a command they
// never chose is a worse answer than the drift it was meant to fix.
func TestCancellingRestoresThePaletteSelectionByIdentity(t *testing.T) {
	row := sessionActionRow()
	m := sessionActionModel(true, row)
	m.mode = ModeCommandPalette
	m.commandContext = captureCommandContext(m)
	m.commandResults = commands.PaletteResults(commandCatalog(), m, m.commandContext, "")
	m.commandCursor = paletteIndexOf(t, m, commands.RestartSession)
	before := m.commandCursor
	scrollCommandPaletteToCursor(&m)

	updated, _ := m.Update(keyPress(keyEnter))
	m = updated.(Model)
	if m.mode != ModeConfirm {
		t.Fatalf("setup: mode = %v, want %v", m.mode, ModeConfirm)
	}

	// Clear Filters is hidden until a filter is applied, and it sits in the
	// Navigate block above every session command. Applying one while the
	// confirmation is up moves the restart row one place down, which is exactly
	// the drift an index-only restore cannot see.
	m.activeFilter = dashboard.Parse("api")

	updated, _ = m.Update(keyPress(keyEsc))
	m = updated.(Model)

	if m.mode != ModeCommandPalette {
		t.Fatalf("mode = %v, want %v", m.mode, ModeCommandPalette)
	}
	want := paletteIndexOf(t, m, commands.RestartSession)
	if want == before {
		t.Fatalf("the result set did not shift under the cancellation; this test proves nothing at index %d", before)
	}
	if m.commandCursor != want {
		t.Fatalf("cursor = %d, want the restart row at its new index %d", m.commandCursor, want)
	}
	if !m.commandContext.HasTarget || m.commandContext.Target.WindowID != row.WindowID {
		t.Fatalf("captured target = %+v, want window %d", m.commandContext, row.WindowID)
	}
	assertCommandPaletteSelectionVisible(t, m)
}

// TestCancellingTheProfilePickerReturnsToItsOrigin covers the deeper nesting:
// the picker is one step further in than the confirmation, and cancelling it
// still returns to the surface the action was chosen from.
func TestCancellingTheProfilePickerReturnsToItsOrigin(t *testing.T) {
	row := sessionActionRow()
	m := sessionActionModel(true, row)
	m.targetSession = row

	m, _ = rowActionRunners()[commands.OverrideProfile](m)
	if m.mode != ModeConfigPicker {
		t.Fatalf("setup: mode = %v, want %v", m.mode, ModeConfigPicker)
	}
	updated, _ := m.Update(keyPress(keyEsc))
	m = updated.(Model)

	if m.mode != ModeActions {
		t.Fatalf("mode = %v, want %v", m.mode, ModeActions)
	}
	if m.sessionActions.Busy() {
		t.Fatal("controller still busy after cancelling the picker")
	}
}

// TestProfileReplacementConfirmsAfterTheChoice is the ordering context.md asks
// for: a replacement profile is selected before the confirmation, so the prompt
// names the profile it is about to restart under.
func TestProfileReplacementConfirmsAfterTheChoice(t *testing.T) {
	row := sessionActionRow()
	m := sessionActionModel(true, row)
	m.targetSession = row

	m, _ = rowActionRunners()[commands.OverrideProfile](m)
	if m.mode != ModeConfigPicker {
		t.Fatalf("setup: mode = %v, want %v", m.mode, ModeConfigPicker)
	}
	m.configPickerCursor = accountIndexOf(t, m, "personal")

	updated, cmd := m.Update(keyPress(keyEnter))
	m = updated.(Model)
	if m.mode != ModeConfirm || cmd != nil {
		t.Fatalf("mode = %v, cmd = %v, want a confirmation and no dispatch", m.mode, cmd != nil)
	}
	if m.confirmAction != sessionactions.ConfirmReplaceProfile {
		t.Fatalf("confirm action = %q, want %q", m.confirmAction, sessionactions.ConfirmReplaceProfile)
	}
	if !strings.Contains(m.confirmConsequence, `"personal"`) {
		t.Fatalf("consequence %q does not name the chosen profile", m.confirmConsequence)
	}

	updated, cmd = m.Update(keyPress(keyY))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("confirmed profile replacement dispatched nothing")
	}
	if m.mode != ModeList {
		t.Fatalf("mode = %v, want %v", m.mode, ModeList)
	}
}

// TestChoosingTheCurrentProfileStaysANoOp keeps the picker's long-standing
// behavior through the controller: replacing a profile with the one already in
// use restarts nothing and confirms nothing.
func TestChoosingTheCurrentProfileStaysANoOp(t *testing.T) {
	row := sessionActionRow()
	m := sessionActionModel(true, row)
	m.targetSession = row

	m, _ = rowActionRunners()[commands.OverrideProfile](m)
	m.configPickerCursor = accountIndexOf(t, m, "work")

	updated, cmd := m.Update(keyPress(keyEnter))
	m = updated.(Model)

	if cmd != nil {
		t.Fatal("selecting the current profile dispatched a restart")
	}
	if m.mode != ModeList {
		t.Fatalf("mode = %v, want %v", m.mode, ModeList)
	}
	if m.sessionActions.Busy() {
		t.Fatal("controller still busy after the no-op")
	}
}

// TestStaleTargetIsRefusedRatherThanRetargeted is the captured-target contract
// seen from the host: a row that has gone while the operator sat in the
// confirmation is refused with an explanation, and nothing is dispatched against
// whatever now occupies its address.
func TestStaleTargetIsRefusedRatherThanRetargeted(t *testing.T) {
	row := sessionActionRow()
	other := session.RunningSession{
		TabID: 22, WindowID: 303, Base: "backend", Project: "backend", Profile: "work",
		LaunchID: "launch-2", HarnessSessionID: "sess-2",
	}
	m := sessionActionModel(true, row)
	m.targetSession = row

	m, _ = rowActionRunners()[commands.CloseSession](m)
	if m.mode != ModeConfirm {
		t.Fatalf("setup: mode = %v, want %v", m.mode, ModeConfirm)
	}
	m = loadSessions(m, []session.RunningSession{other})

	updated, cmd := m.Update(keyPress(keyY))
	m = updated.(Model)

	if cmd != nil {
		// A refusal may still carry the toast ticker, but it must not carry work.
		if len(m.client.(*uitestKittyClient).CallsFor("CloseWindow")) != 0 {
			t.Fatal("a stale confirmation closed a window")
		}
	}
	if got := m.activeUndoEntries(); len(got) != 0 {
		t.Fatalf("undo entries = %v, want none for a refused action", got)
	}
	if len(m.toasts) != 1 || m.toasts[0].Severity != toastWarning {
		t.Fatalf("toasts = %+v, want one warning explaining the refusal", m.toasts)
	}
	if m.mode != ModeList {
		t.Fatalf("mode = %v, want %v", m.mode, ModeList)
	}
	if m.sessionActions.Busy() {
		t.Fatal("controller still busy after a refusal")
	}
}

// TestHostTabConfirmationSaysTheDashboardClosesWithIt is the hosting-tab
// warning. The action is still offered — restarting the session beside the
// dashboard is ordinary — but the sentence comes before the keystroke.
func TestHostTabConfirmationSaysTheDashboardClosesWithIt(t *testing.T) {
	row := sessionActionRow()
	m := sessionActionModel(true, row)
	m.targetSession = row
	m.actionHost = sessionactions.Host{TabID: row.TabID, WindowID: 999}

	hosted, _ := rowActionRunners()[commands.CloseSession](m)
	if hosted.mode != ModeConfirm {
		t.Fatalf("mode = %v, want %v", hosted.mode, ModeConfirm)
	}
	if !strings.Contains(hosted.confirmConsequence, hostTabConsequence) {
		t.Fatalf("consequence %q does not warn about the hosting tab", hosted.confirmConsequence)
	}
	if !strings.Contains(hosted.confirmConsequence, wantCloseConsequence) {
		t.Fatalf("consequence %q dropped the shared close wording", hosted.confirmConsequence)
	}

	m.actionHost = sessionactions.Host{TabID: row.TabID + 1, WindowID: 999}
	elsewhere, _ := rowActionRunners()[commands.CloseSession](m)
	if strings.Contains(elsewhere.confirmConsequence, hostTabConsequence) {
		t.Fatalf("consequence %q warns about a tab the dashboard does not live in", elsewhere.confirmConsequence)
	}
}

// TestHostTabIsTrackedIndependentlyOfSelection pins the identity the warning
// rests on. The hosting tab comes from the refresh that resolved it and is not
// moved by a cross-window selection handoff, which names the tab the dashboard
// should SELECT rather than the one it lives in.
func TestHostTabIsTrackedIndependentlyOfSelection(t *testing.T) {
	m := testModelWithSessions()
	m = applySessions(m, commands.SessionsMsg{
		Sessions:     []session.RunningSession{sessionActionRow()},
		HostWindowID: 909,
		HostTabID:    99,
	})
	if m.actionHost != (sessionactions.Host{WindowID: 909, TabID: 99}) {
		t.Fatalf("host = %+v, want window 909 in tab 99", m.actionHost)
	}

	// A refresh that could not resolve the host reports zero, which is "unknown"
	// and must not erase an answer an earlier refresh did have.
	m = applySessions(m, commands.SessionsMsg{
		Sessions:       []session.RunningSession{sessionActionRow()},
		SelectionTabID: 22,
	})
	if m.actionHost != (sessionactions.Host{WindowID: 909, TabID: 99}) {
		t.Fatalf("host = %+v after an unresolved refresh, want it preserved", m.actionHost)
	}
}

// TestActionDoneCorrelatesWithItsDispatch is the completion contract: the reply
// clears the record of the dispatch it answers, and a reply naming nothing in
// flight still reaches the model's own error handling.
func TestActionDoneCorrelatesWithItsDispatch(t *testing.T) {
	row := sessionActionRow()
	m := sessionActionModel(false, row)
	m.targetSession = row

	m, _ = rowActionRunners()[commands.CloseSession](m)
	inflight := m.sessionActions.InFlight()
	if len(inflight) != 1 {
		t.Fatalf("in flight = %+v, want one dispatch", inflight)
	}
	request := inflight[0].Request

	updated, _ := m.Update(commands.ActionDoneMsg{Request: uint64(request)})
	m = updated.(Model)
	if got := m.sessionActions.InFlight(); len(got) != 0 {
		t.Fatalf("in flight = %+v, want the dispatch cleared", got)
	}

	// An uncorrelated completion — an undo, a direct key, a reply already
	// reported — is still the model's to record.
	failure := errors.New("kitty refused")
	updated, _ = m.Update(commands.ActionDoneMsg{Err: failure})
	m = updated.(Model)
	if !errors.Is(m.actionErr, failure) {
		t.Fatalf("actionErr = %v, want %v", m.actionErr, failure)
	}
}

// TestUnrelatedConfirmationsStillReachTheirOwnHandlers is the multiplexing
// guard. The confirm overlay and the profile picker are shared with the
// worktree, handoff, launcher, and ASL flows, and the controller's "is this
// mine" question must answer no for all of them.
func TestUnrelatedConfirmationsStillReachTheirOwnHandlers(t *testing.T) {
	m := sessionActionModel(true, sessionActionRow())
	if m.sessionActionAwaitingConfirm() || m.sessionActionAwaitingProfile() {
		t.Fatal("a fresh model claims a pending session action")
	}

	m.confirmAction = "worktree forget"
	m.confirmReturnMode = ModeWorktrees
	m.mode = ModeConfirm
	if m.sessionActionAwaitingConfirm() {
		t.Fatal("a worktree confirmation was claimed by the session-action controller")
	}
	updated, _ := m.Update(keyPress(keyEsc))
	got := updated.(Model)
	if got.mode != ModeWorktrees {
		t.Fatalf("mode = %v, want the worktree flow's own return mode %v", got.mode, ModeWorktrees)
	}
}

// paletteIndexOf is the row one command occupies in the current results.
func paletteIndexOf(t *testing.T, m Model, id commands.ID) int {
	t.Helper()
	for i, result := range m.commandResults {
		if result.ID == id {
			return i
		}
	}
	t.Fatalf("command %q is not in the palette results", id)
	return -1
}

// accountIndexOf is the row one profile occupies in the open picker.
func accountIndexOf(t *testing.T, m Model, name string) int {
	t.Helper()
	for i, account := range m.configPickerAccounts {
		if account.Name == name {
			return i
		}
	}
	t.Fatalf("profile %q is not in the picker", name)
	return -1
}
