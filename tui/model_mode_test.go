package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// ---- Mode transition tests ----

func TestModeListToSearch(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	if m.mode != ModeList {
		t.Fatal("expected ModeList initially")
	}
	m = pressKey(m, "/")
	if m.mode != ModeSearch {
		t.Fatalf("expected ModeSearch after /, got %d", m.mode)
	}
}

func TestModeSearchEscReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after Esc, got %d", m.mode)
	}
}

func TestModeSearchEnterAppliesAndReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = pressKey(m, "a")
	m = pressKey(m, "p")
	m = pressKey(m, "i")
	m = pressKey(m, "enter")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after Enter, got %d", m.mode)
	}
	if m.activeFilter.Raw != "api" {
		t.Fatalf("expected activeFilter.Raw=api, got %q", m.activeFilter.Raw)
	}
}

func TestModeSearchLiveAppliesEachKeystroke(t *testing.T) {
	// Each keystroke should narrow the list before Enter is pressed.
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = pressKey(m, "a")
	m = pressKey(m, "p")
	m = pressKey(m, "i")
	if m.mode != ModeSearch {
		t.Fatalf("expected ModeSearch while typing, got %d", m.mode)
	}
	if len(m.filtered) != 1 || m.filtered[0].Base != "api" {
		t.Fatalf("expected live-filtered list to contain only api, got %v", m.filtered)
	}
}

func TestFKeyRemainsTextInFilterInput(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = pressKey(m, "f")
	if m.mode != ModeSearch {
		t.Fatalf("expected ModeSearch after /f, got %d", m.mode)
	}
	if m.filterText != "f" {
		t.Fatalf("expected filterText=f after /f, got %q", m.filterText)
	}
	if m.activeFilter.Account != "" {
		t.Fatalf("expected f in search input not to apply a profile filter, got %+v", m.activeFilter)
	}
}

func TestVKeyDoesNotLeaveLiveList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "v")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after v, got %d", m.mode)
	}
}

func TestFilterEscRestoresSavedFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Commit a filter first.
	m = pressKey(m, "/")
	m = pressKey(m, "a")
	m = pressKey(m, "p")
	m = pressKey(m, "i")
	m = pressKey(m, "enter")
	// Re-enter filter, type something else, then Esc.
	m = pressKey(m, "/")
	m = pressKey(m, "b")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after Esc, got %d", m.mode)
	}
	if m.activeFilter.Raw != "api" {
		t.Fatalf("expected previous filter restored, got %q", m.activeFilter.Raw)
	}
}

func TestSlashTypingNarrowsByAccount(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = pressKey(m, "@")
	m = pressKey(m, "w")
	m = pressKey(m, "o")
	m = pressKey(m, "r")
	m = pressKey(m, "k")
	if m.activeFilter.Account != "work" {
		t.Fatalf("expected Account=work, got %q", m.activeFilter.Account)
	}
	for _, s := range m.filtered {
		if s.Account != "work" {
			t.Fatalf("filtered session %q has wrong account %q", s.Base, s.Account)
		}
	}
}

// ---- Vim navigation tests ----

func TestNavJK(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	if m.cursor != 0 {
		t.Fatal("expected cursor=0 initially")
	}
	m = pressKey(m, "j")
	if m.cursor != 1 {
		t.Fatalf("expected cursor=1 after j, got %d", m.cursor)
	}
	m = pressKey(m, "k")
	if m.cursor != 0 {
		t.Fatalf("expected cursor=0 after k, got %d", m.cursor)
	}
}

// The two ends of the list are home and end now. `g`/`G` named the same two
// places with keys that only a vim reader guesses, and they are unbound at the
// dashboard rather than rebound — see TestNavGAndCapitalGAreRetiredAtTheList.
// They survive inside the project switcher, the health overlay, and the detail
// timeline, which have their own handlers and their own scroll geometry.
func TestNavHomeGoesToTop(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m = pressKey(m, keyHome)
	if m.cursor != 0 {
		t.Fatalf("expected cursor=0 after home, got %d", m.cursor)
	}
}

func TestNavEndGoesToBottom(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, keyEnd)
	want := len(m.filtered) - 1
	if m.cursor != want {
		t.Fatalf("expected cursor=%d after end, got %d", want, m.cursor)
	}
}

func TestNavGAndCapitalGReachListEnds(t *testing.T) {
	for _, key := range []string{keyG, keyGG} {
		t.Run(key, func(t *testing.T) {
			m := loadSessions(testModel(), makeSessions())
			m = pressKey(m, "j")
			want := 0
			if key == keyGG {
				want = m.listItemCount() - 1
			}
			updated, _ := m.Update(keyPress(key))
			got := updated.(Model)
			if got.cursor != want {
				t.Errorf("%q cursor %d, want %d", key, got.cursor, want)
			}
			if got.mode != ModeList {
				t.Errorf("%q left ModeList for %q", key, uitestModeName(got.mode))
			}
		})
	}
}

func TestNavClampAtBounds(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Pressing k at top should stay at 0.
	m = pressKey(m, "k")
	if m.cursor != 0 {
		t.Fatalf("expected cursor=0 after k at top, got %d", m.cursor)
	}
	// Pressing j past bottom should stay at last.
	m = pressKey(m, keyEnd)
	last := m.cursor
	m = pressKey(m, "j")
	if m.cursor != last {
		t.Fatalf("expected cursor to stay at %d after j at bottom, got %d", last, m.cursor)
	}
}

// ---- Search filtering tests ----

func TestSearchFilterByTitle(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = dashboard.Parse("api")
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.rebuildItems()
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 result for 'api', got %d", len(m.filtered))
	}
	if m.filtered[0].Base != "api" {
		t.Fatalf("wrong session: %q", m.filtered[0].Base)
	}
}

func TestSearchFilterByDirectory(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = dashboard.ApplyFilter(m.sessions, dashboard.Parse("dotfiles"), m.lastActivity, m.idleThreshold)
	m.rebuildItems()
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 result for 'dotfiles' dir, got %d", len(m.filtered))
	}
}

func TestSearchFilterByAccountSubstring(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Bare-token 'work' matches the Account field, so both work sessions survive
	// even though the visible title column is project-only.
	m.filtered = dashboard.ApplyFilter(m.sessions, dashboard.Parse("work"), m.lastActivity, m.idleThreshold)
	m.rebuildItems()
	if len(m.filtered) != 2 {
		t.Fatalf("expected 2 results for substring 'work', got %d", len(m.filtered))
	}
}

func TestAccountFilterNarrowsList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = dashboard.ApplyFilter(m.sessions, dashboard.Parse("@personal"), m.lastActivity, m.idleThreshold)
	m.rebuildItems()
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 personal session, got %d", len(m.filtered))
	}
}

func TestSearchCombinedWithAccountFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = dashboard.ApplyFilter(m.sessions, dashboard.Parse("api @work"), m.lastActivity, m.idleThreshold)
	m.rebuildItems()
	if len(m.filtered) != 1 || m.filtered[0].Base != "api" {
		t.Fatalf("unexpected combined filter result: %v", m.filtered)
	}
}

// ---- Refresh tick test ----

func TestTickTriggersRefetch(t *testing.T) {
	m := testModel()
	updated, cmd := m.Update(commands.TickMsg{})
	if updated.(Model).err != nil {
		t.Fatal("unexpected error after tick")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after commands.TickMsg (should re-schedule fetch + tick)")
	}
}

// ---- Error display test ----

func TestErrMsgSetsErr(t *testing.T) {
	m := applyRefreshErr(testModel(), errors.New("kitty offline"))
	if m.err == nil {
		t.Fatal("expected err to be set after commands.ErrMsg")
	}
}

func TestSessionsMsgClearsErr(t *testing.T) {
	m := applyRefreshErr(testModel(), errors.New("kitty offline"))
	m = loadSessions(m, makeSessions())
	if m.err != nil {
		t.Fatalf("expected err cleared after commands.SessionsMsg, got %v", m.err)
	}
}

// ---- Footer content test ----

func TestFooterChangesWithMode(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 120, 40

	listFooter := stripAnsi(renderFooter(m))
	if !strings.Contains(listFooter, "/ filter") {
		t.Errorf("list footer missing unified filter hint, got:\n%s", listFooter)
	}
	m.mode = ModeSearch
	searchFooter := stripAnsi(renderFooter(m))
	if !strings.Contains(searchFooter, "enter apply") {
		t.Errorf("filter footer missing apply hint, got:\n%s", searchFooter)
	}
	if !strings.Contains(searchFooter, "@profile") {
		t.Errorf("filter footer missing DSL hint, got:\n%s", searchFooter)
	}
}

// ---- Confirm mode tests ----

// The confirm prompt is entered from the actions overlay now — `X` and `r` are
// its entries rather than dashboard keys — but the prompt itself is unchanged,
// and y/n/esc are still its whole vocabulary.

// Cancelling a confirmation raised from the per-row menu returns to that menu
// rather than to the bare list. The nested interaction was entered from the
// overlay, and the operator who backs out of it has not said they are done with
// the row — they have said they are done with this one action.
func TestConfirmNReturnsToTheActionsOverlay(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressRowAction(t, m, keyClose) // → ModeConfirm (close)
	m = pressKey(m, "n")               // cancel
	if m.mode != ModeActions {
		t.Fatalf("expected ModeActions after n in confirm, got %d", m.mode)
	}
	// A second esc is what leaves the row entirely.
	if m = pressKey(m, "esc"); m.mode != ModeList {
		t.Fatalf("expected ModeList after leaving the overlay, got %d", m.mode)
	}
}

func TestConfirmEscReturnsToTheActionsOverlay(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressRowAction(t, m, keyClose) // → ModeConfirm
	m = pressKey(m, "esc")
	if m.mode != ModeActions {
		t.Fatalf("expected ModeActions after esc in confirm, got %d", m.mode)
	}
	if m = pressKey(m, "esc"); m.mode != ModeList {
		t.Fatalf("expected ModeList after leaving the overlay, got %d", m.mode)
	}
}

func TestConfirmYEmitsCloseCmd(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressRowAction(t, m, keyClose) // → ModeConfirm (close)
	updated, cmd := m.Update(keyPress("y"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after y, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after y in confirm (close)")
	}
}

func TestConfirmYEmitsRestartCmd(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressRowAction(t, m, keyR) // → ModeConfirm (restart)
	updated, cmd := m.Update(keyPress("y"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after y, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after y in confirm (restart)")
	}
}
