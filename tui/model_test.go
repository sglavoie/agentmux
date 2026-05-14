package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// testModel returns a Model with a minimal config and mock client.
func testModel() Model {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "green"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	return NewModel(cfg, client)
}

// loadSessions feeds a set of RunningSession values into the model
// as if they came from a Kitty query.
func loadSessions(m Model, sessions []session.RunningSession) Model {
	updated, _ := m.Update(sessionsMsg{sessions: sessions})
	return updated.(Model)
}

func pressKey(m Model, key string) Model {
	updated, _ := m.Update(keyPress(key))
	return updated.(Model)
}

// keyPress creates a KeyPressMsg whose String() returns the given key string.
func keyPress(key string) tea.KeyPressMsg {
	switch key {
	case "j":
		return tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"})
	case "k":
		return tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"})
	case "g":
		return tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"})
	case "G":
		return tea.KeyPressMsg(tea.Key{Code: 'G', Text: "G"})
	case "/":
		return tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"})
	case "f":
		return tea.KeyPressMsg(tea.Key{Code: 'f', Text: "f"})
	case "q":
		return tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"})
	case "a":
		return tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"})
	case "e":
		return tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"})
	case "o":
		return tea.KeyPressMsg(tea.Key{Code: 'o', Text: "o"})
	case "p":
		return tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"})
	case "y":
		return tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"})
	case "n":
		return tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"})
	case " ":
		return tea.KeyPressMsg(tea.Key{Code: ' '}) // no Text: String() returns "space"
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	case "backspace":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})
	default:
		return tea.KeyPressMsg(tea.Key{Code: rune(key[0]), Text: key})
	}
}

func makeSessions() []session.RunningSession {
	return []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", Directory: "/home/user/api", State: ""},
		{TabID: 2, Base: "W:backend", Account: "work", Directory: "/home/user/backend", State: "bolt"},
		{TabID: 3, Base: "P:dotfiles", Account: "personal", Directory: "/home/user/dotfiles", State: ""},
	}
}

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
	if m.activeSearch != "api" {
		t.Fatalf("expected activeSearch=api, got %q", m.activeSearch)
	}
}

func TestModeListToAccountFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "f")
	if m.mode != ModeAccountFilter {
		t.Fatalf("expected ModeAccountFilter after f, got %d", m.mode)
	}
}

func TestModeAccountFilterEscReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "f")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after Esc, got %d", m.mode)
	}
}

func TestModeAccountFilterEnterAppliesFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "f")
	// acctCursor starts at 0 (All); press j to move to first account.
	m = pressKey(m, "j")
	m = pressKey(m, "enter")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after Enter, got %d", m.mode)
	}
	if m.activeAccount == "" {
		t.Fatal("expected activeAccount to be set after filter selection")
	}
	for _, s := range m.filtered {
		if s.Account != m.activeAccount {
			t.Fatalf("filtered session %q has wrong account %q, want %q", s.Base, s.Account, m.activeAccount)
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

func TestNavGTop(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m = pressKey(m, "g")
	if m.cursor != 0 {
		t.Fatalf("expected cursor=0 after g, got %d", m.cursor)
	}
}

func TestNavGBottom(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "G")
	want := len(m.filtered) - 1
	if m.cursor != want {
		t.Fatalf("expected cursor=%d after G, got %d", want, m.cursor)
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
	m = pressKey(m, "G")
	last := m.cursor
	m = pressKey(m, "j")
	if m.cursor != last {
		t.Fatalf("expected cursor to stay at %d after j at bottom, got %d", last, m.cursor)
	}
}

// ---- Search filtering tests ----

func TestSearchFilterByTitle(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeSearch = "api"
	m.filtered = applyFilter(m.sessions, m.activeSearch, m.activeAccount)
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 result for 'api', got %d", len(m.filtered))
	}
	if m.filtered[0].Base != "W:api" {
		t.Fatalf("wrong session: %q", m.filtered[0].Base)
	}
}

func TestSearchFilterByDirectory(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = applyFilter(m.sessions, "dotfiles", "")
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 result for 'dotfiles' dir, got %d", len(m.filtered))
	}
}

func TestSearchFilterByAccount(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = applyFilter(m.sessions, "work", "")
	if len(m.filtered) != 2 {
		t.Fatalf("expected 2 results for account search 'work', got %d", len(m.filtered))
	}
}

func TestAccountFilterNarrowsList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = applyFilter(m.sessions, "", "personal")
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 personal session, got %d", len(m.filtered))
	}
}

func TestSearchCombinedWithAccountFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = applyFilter(m.sessions, "api", "work")
	if len(m.filtered) != 1 || m.filtered[0].Base != "W:api" {
		t.Fatalf("unexpected combined filter result: %v", m.filtered)
	}
}

// ---- Refresh tick test ----

func TestTickTriggersRefetch(t *testing.T) {
	m := testModel()
	updated, cmd := m.Update(tickMsg{})
	if updated.(Model).err != nil {
		t.Fatal("unexpected error after tick")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after tickMsg (should re-schedule fetch + tick)")
	}
}

// ---- Error display test ----

func TestErrMsgSetsErr(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(errMsg{err: errors.New("kitty offline")})
	if updated.(Model).err == nil {
		t.Fatal("expected err to be set after errMsg")
	}
}

func TestSessionsMsgClearsErr(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(errMsg{err: errors.New("kitty offline")})
	m = updated.(Model)
	m = loadSessions(m, makeSessions())
	if m.err != nil {
		t.Fatalf("expected err cleared after sessionsMsg, got %v", m.err)
	}
}

// ---- Footer content test ----

func TestFooterChangesWithMode(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 120, 40

	listFooter := renderFooter(m)
	if !strings.Contains(listFooter, "/: search") {
		t.Errorf("list footer missing search hint, got:\n%s", listFooter)
	}

	m.mode = ModeSearch
	searchFooter := renderFooter(m)
	if !strings.Contains(searchFooter, "enter: apply") {
		t.Errorf("search footer missing apply hint, got:\n%s", searchFooter)
	}

	m.mode = ModeAccountFilter
	filterFooter := renderFooter(m)
	if !strings.Contains(filterFooter, "enter: select") {
		t.Errorf("account filter footer missing select hint, got:\n%s", filterFooter)
	}
}

// ---- Session directory display test ----

func TestSelectedDirectoryDisplayed(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 120, 40
	content := m.render()
	if !strings.Contains(content, "/home/user/api") {
		t.Errorf("expected selected session directory in view, got:\n%s", content)
	}
}

// ---- Actions mode tests ----

func TestEnterOpensActions(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter")
	if m.mode != ModeActions {
		t.Fatalf("expected ModeActions after enter, got %d", m.mode)
	}
	if m.actionTarget.Base != "W:api" {
		t.Fatalf("expected actionTarget=W:api, got %q", m.actionTarget.Base)
	}
}

func TestAKeyOpensActions(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "a")
	if m.mode != ModeActions {
		t.Fatalf("expected ModeActions after a, got %d", m.mode)
	}
}

func TestActionsEscReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from actions, got %d", m.mode)
	}
}

func TestActionsNavigation(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // open actions, cursor=0 (close)
	if m.actionCursor != 0 {
		t.Fatalf("expected actionCursor=0, got %d", m.actionCursor)
	}
	m = pressKey(m, "j") // move to restart
	if m.actionCursor != 1 {
		t.Fatalf("expected actionCursor=1 after j, got %d", m.actionCursor)
	}
	m = pressKey(m, "j") // move to override config
	if m.actionCursor != 2 {
		t.Fatalf("expected actionCursor=2 after second j, got %d", m.actionCursor)
	}
	m = pressKey(m, "j") // clamp at bottom
	if m.actionCursor != 2 {
		t.Fatalf("expected actionCursor clamped at 2, got %d", m.actionCursor)
	}
	m = pressKey(m, "k") // back to restart
	if m.actionCursor != 1 {
		t.Fatalf("expected actionCursor=1 after k, got %d", m.actionCursor)
	}
	m = pressKey(m, "k") // back to close
	if m.actionCursor != 0 {
		t.Fatalf("expected actionCursor=0 after second k, got %d", m.actionCursor)
	}
}

func TestActionsEnterOpensConfirm(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // open actions (cursor=0: close)
	m = pressKey(m, "enter") // confirm close
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after enter in actions, got %d", m.mode)
	}
	if m.confirmAction != "close" {
		t.Fatalf("expected confirmAction=close, got %q", m.confirmAction)
	}
}

func TestActionsEnterRestartOpensConfirm(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // open actions
	m = pressKey(m, "j")     // select restart
	m = pressKey(m, "enter") // confirm restart
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm, got %d", m.mode)
	}
	if m.confirmAction != "restart" {
		t.Fatalf("expected confirmAction=restart, got %q", m.confirmAction)
	}
}

// ---- Confirm mode tests ----

func TestConfirmNReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // → ModeActions
	m = pressKey(m, "enter") // → ModeConfirm (close)
	m = pressKey(m, "n")     // cancel
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after n in confirm, got %d", m.mode)
	}
}

func TestConfirmEscReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // → ModeActions
	m = pressKey(m, "enter") // → ModeConfirm
	m = pressKey(m, "esc")   // cancel
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc in confirm, got %d", m.mode)
	}
}

func TestConfirmYEmitsCloseCmd(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // → ModeActions
	m = pressKey(m, "enter") // → ModeConfirm (close)
	updated, cmd := m.Update(keyPress("y"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after y, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after y in confirm (close)")
	}
}

func TestConfirmYEmitsRestartCmd(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // → ModeActions
	m = pressKey(m, "j")     // select restart
	m = pressKey(m, "enter") // → ModeConfirm (restart)
	updated, cmd := m.Update(keyPress("y"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after y, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after y in confirm (restart)")
	}
}

// ---- Picker mode tests ----

func testModelWithSessions() Model {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
			{Title: "backend", Directory: "/home/user/backend", Account: "work"},
			{Title: "dotfiles", Directory: "/home/user/dotfiles", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	return NewModel(cfg, client)
}

func TestLKeyOpensPicker(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	if len(m.pickerSessions) != 3 {
		t.Fatalf("expected 3 picker sessions (from config), got %d", len(m.pickerSessions))
	}
}

func TestPickerEscReturnsToList(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from picker, got %d", m.mode)
	}
}

func TestPickerSpaceTogglesSelection(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	// pickerCursor=0, not selected yet
	if m.pickerSelected[0] {
		t.Fatal("expected session not selected initially")
	}
	m = pressKey(m, " ") // toggle on
	if !m.pickerSelected[0] {
		t.Fatal("expected session selected after space")
	}
	m = pressKey(m, " ") // toggle off
	if m.pickerSelected[0] {
		t.Fatal("expected session deselected after second space")
	}
}

func TestPickerEnterEmitsLaunchCmd(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	m = pressKey(m, " ") // select first session
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after enter in picker, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after enter in picker (launch)")
	}
}

func TestPickerRunningStatus(t *testing.T) {
	running := makeSessions() // W:api, W:backend, P:dotfiles
	m := loadSessions(testModelWithSessions(), running)
	m = pressKey(m, "l")
	// All 3 config sessions match the running sessions.
	for _, sess := range m.cfg.Sessions {
		baseTitle := session.FormatTitle(sess)
		if !m.pickerRunning[baseTitle] {
			t.Errorf("expected %q to be marked as running in picker", baseTitle)
		}
	}
}

func TestBuildPickerRunning(t *testing.T) {
	running := makeSessions()
	got := buildPickerRunning(running)
	if !got["W:api"] {
		t.Error("expected W:api in running set")
	}
	if !got["W:backend"] {
		t.Error("expected W:backend in running set")
	}
	if !got["P:dotfiles"] {
		t.Error("expected P:dotfiles in running set")
	}
	if got["W:other"] {
		t.Error("expected W:other NOT in running set")
	}
}

// ---- Footer hints for new modes ----

func TestFooterActionsHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeActions
	footer := renderFooter(m)
	if !strings.Contains(footer, "enter: confirm action") {
		t.Errorf("actions footer missing confirm hint, got:\n%s", footer)
	}
}

func TestFooterConfirmHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeConfirm
	footer := renderFooter(m)
	if !strings.Contains(footer, "y: yes") {
		t.Errorf("confirm footer missing y hint, got:\n%s", footer)
	}
}

func TestFooterPickerHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModePicker
	footer := renderFooter(m)
	if !strings.Contains(footer, "space: toggle") {
		t.Errorf("picker footer missing space hint, got:\n%s", footer)
	}
}

func TestFooterListHintIncludesActionsAndPicker(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	footer := renderFooter(m)
	if !strings.Contains(footer, "enter/a: actions") {
		t.Errorf("list footer missing actions hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "l: pick & launch") {
		t.Errorf("list footer missing picker hint, got:\n%s", footer)
	}
}

// ---- Config picker mode tests ----

func TestActionsOverrideOpensConfigPicker(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // → ModeActions (cursor=0: close)
	m = pressKey(m, "j")     // cursor=1: restart
	m = pressKey(m, "j")     // cursor=2: override config
	m = pressKey(m, "enter") // → ModeConfigPicker
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after selecting override config, got %d", m.mode)
	}
	if len(m.configPickerAccounts) != 2 {
		t.Fatalf("expected 2 accounts in picker, got %d", len(m.configPickerAccounts))
	}
}

func TestConfigPickerEscReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // → ModeActions
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m = pressKey(m, "enter") // → ModeConfigPicker
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from config picker, got %d", m.mode)
	}
}

func TestConfigPickerNavigation(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter")
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m = pressKey(m, "enter") // → ModeConfigPicker, cursor=0
	if m.configPickerCursor != 0 {
		t.Fatalf("expected configPickerCursor=0, got %d", m.configPickerCursor)
	}
	m = pressKey(m, "j") // cursor=1
	if m.configPickerCursor != 1 {
		t.Fatalf("expected configPickerCursor=1 after j, got %d", m.configPickerCursor)
	}
	m = pressKey(m, "j") // clamp at 1 (only 2 accounts)
	if m.configPickerCursor != 1 {
		t.Fatalf("expected configPickerCursor clamped at 1, got %d", m.configPickerCursor)
	}
	m = pressKey(m, "k") // cursor=0
	if m.configPickerCursor != 0 {
		t.Fatalf("expected configPickerCursor=0 after k, got %d", m.configPickerCursor)
	}
}

func TestConfigPickerSameAccountNoOp(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// First session is W:api with account "work", which is index 0 in testModel accounts.
	m = pressKey(m, "enter") // → ModeActions
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m = pressKey(m, "enter") // → ModeConfigPicker
	// configPickerCurrent = "work", cursor should be at index 0 (work)
	if m.configPickerCurrent != "work" {
		t.Fatalf("expected configPickerCurrent=work, got %q", m.configPickerCurrent)
	}
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after selecting same account, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd when selecting same account (no-op)")
	}
}

func TestConfigPickerDifferentAccountEmitsCmd(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "enter") // → ModeActions
	m = pressKey(m, "j")
	m = pressKey(m, "j")
	m = pressKey(m, "enter") // → ModeConfigPicker (current=work, cursor=0)
	m = pressKey(m, "j")     // cursor=1 (personal)
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after selecting different account, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd when selecting different account")
	}
}

func TestFooterConfigPickerHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeConfigPicker
	footer := renderFooter(m)
	if !strings.Contains(footer, "enter: select") {
		t.Errorf("config picker footer missing select hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "esc: cancel") {
		t.Errorf("config picker footer missing cancel hint, got:\n%s", footer)
	}
}

// ---- Picker open directory tests ----

func TestPickerOKeyEmitsCmd(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	updated, cmd := m.Update(keyPress("o"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("expected ModePicker after o (stays in picker), got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after o in picker")
	}
}

func TestPickerOKeyEmptyIsNoOp(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
		},
		Sessions: []config.Session{},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	updated, cmd := m.Update(keyPress("o"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("expected ModePicker after o on empty picker, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd after o on empty picker")
	}
}

// ---- Peek mode tests ----

func TestEKeyReturnsPeekCmd(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	_, cmd := m.Update(keyPress("e"))
	if cmd == nil {
		t.Fatal("expected non-nil cmd after e with sessions")
	}
}

func TestEKeyEmptyListIsNoOp(t *testing.T) {
	m := loadSessions(testModel(), nil)
	_, cmd := m.Update(keyPress("e"))
	if cmd != nil {
		t.Fatal("expected nil cmd after e with no sessions")
	}
}

func TestPeekDoneMsgErrorSetsActionErr(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(peekDoneMsg{err: errors.New("window gone")})
	if updated.(Model).actionErr == nil {
		t.Fatal("expected actionErr set after peekDoneMsg with error")
	}
}

func TestFooterListHintIncludesPeek(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	footer := renderFooter(m)
	if !strings.Contains(footer, "e: peek") {
		t.Errorf("list footer missing peek hint, got:\n%s", footer)
	}
}

func TestFooterPickerOpenDirHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModePicker
	footer := renderFooter(m)
	if !strings.Contains(footer, "o: open dir") {
		t.Errorf("picker footer missing open dir hint, got:\n%s", footer)
	}
}
