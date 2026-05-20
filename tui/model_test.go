package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
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
	case "x":
		return tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"})
	case "r":
		return tea.KeyPressMsg(tea.Key{Code: 'r', Text: "r"})
	case "p":
		return tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"})
	case "y":
		return tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"})
	case "n":
		return tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"})
	case "?":
		return tea.KeyPressMsg(tea.Key{Code: '?', Text: "?"})
	case "c":
		return tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"})
	case "C":
		return tea.KeyPressMsg(tea.Key{Code: 'C', Text: "C"})
	case " ":
		return tea.KeyPressMsg(tea.Key{Code: ' '}) // no Text: String() returns "space"
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	case "backspace":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return tea.KeyPressMsg(tea.Key{Code: rune(key[0]), Text: key})
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
	if len(m.filtered) != 1 || m.filtered[0].Base != "W:api" {
		t.Fatalf("expected live-filtered list to contain only W:api, got %v", m.filtered)
	}
}

func TestFKeyEntersFilterWithAccountPrefill(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "f")
	if m.mode != ModeSearch {
		t.Fatalf("expected ModeSearch (unified filter) after f, got %d", m.mode)
	}
	if m.filterText != "@" {
		t.Fatalf("expected filterText=@, got %q", m.filterText)
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

func TestFKeyTypingNarrowsByAccount(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "f") // filterText="@"
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
	m.activeFilter = Parse("api")
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 result for 'api', got %d", len(m.filtered))
	}
	if m.filtered[0].Base != "W:api" {
		t.Fatalf("wrong session: %q", m.filtered[0].Base)
	}
}

func TestSearchFilterByDirectory(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = applyFilter(m.sessions, Parse("dotfiles"), m.lastActivity, m.idleThreshold)
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 result for 'dotfiles' dir, got %d", len(m.filtered))
	}
}

func TestSearchFilterByAccountSubstring(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Bare-token 'work' matches both Account field and Base prefix W: lowercase 'w'... actually
	// substring scans Base/Directory/Account so 'work' hits two work sessions via account match.
	m.filtered = applyFilter(m.sessions, Parse("work"), m.lastActivity, m.idleThreshold)
	if len(m.filtered) != 2 {
		t.Fatalf("expected 2 results for substring 'work', got %d", len(m.filtered))
	}
}

func TestAccountFilterNarrowsList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = applyFilter(m.sessions, Parse("@personal"), m.lastActivity, m.idleThreshold)
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 personal session, got %d", len(m.filtered))
	}
}

func TestSearchCombinedWithAccountFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered = applyFilter(m.sessions, Parse("api @work"), m.lastActivity, m.idleThreshold)
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
	if !strings.Contains(listFooter, "/, f: filter") {
		t.Errorf("list footer missing unified filter hint, got:\n%s", listFooter)
	}

	m.mode = ModeSearch
	searchFooter := renderFooter(m)
	if !strings.Contains(searchFooter, "enter: apply") {
		t.Errorf("filter footer missing apply hint, got:\n%s", searchFooter)
	}
	if !strings.Contains(searchFooter, "@profile") {
		t.Errorf("filter footer missing DSL hint, got:\n%s", searchFooter)
	}
}

// ---- Confirm mode tests ----

// modalConfirmModel mirrors testModel but opts into the legacy y/n confirm
// flow via defaults.confirm_destructive=true. Used by the confirm-mode
// tests below so they keep exercising the modal path now that toast+undo
// is the default for x/r.
func modalConfirmModel() Model {
	m := testModel()
	m.cfg.Defaults.ConfirmDestructive = true
	return m
}

func TestConfirmNReturnsToList(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressKey(m, "x") // → ModeConfirm (close)
	m = pressKey(m, "n") // cancel
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after n in confirm, got %d", m.mode)
	}
}

func TestConfirmEscReturnsToList(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressKey(m, "x") // → ModeConfirm
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc in confirm, got %d", m.mode)
	}
}

func TestConfirmYEmitsCloseCmd(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressKey(m, "x") // → ModeConfirm (close)
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
	m = pressKey(m, "r") // → ModeConfirm (restart)
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
	// No running sessions so every config row is a launch candidate. With
	// the new per-launch progress flow, dedup of already-running rows
	// would return nil — this test asserts the launch path itself.
	m := loadSessions(testModelWithSessions(), nil)
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
		key := session.SessionKey(sess.EffectiveProfile(), sess.Title)
		if !m.pickerRunning[key] {
			t.Errorf("expected %q to be marked as running in picker", key)
		}
	}
}

func TestBuildPickerRunning(t *testing.T) {
	running := makeSessions()
	got := buildPickerRunning(running)
	if !got[session.SessionKey("work", "api")] {
		t.Error("expected work/api in running set")
	}
	if !got[session.SessionKey("work", "backend")] {
		t.Error("expected work/backend in running set")
	}
	if !got[session.SessionKey("personal", "dotfiles")] {
		t.Error("expected personal/dotfiles in running set")
	}
	if got[session.SessionKey("work", "other")] {
		t.Error("expected work/other NOT in running set")
	}
}

// ---- Footer hints for new modes ----

func TestFooterConfirmHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeConfirm
	footer := renderFooter(m)
	if !strings.Contains(footer, "[y]es") {
		t.Errorf("confirm footer missing [y]es hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "[esc] cancel") {
		t.Errorf("confirm footer missing [esc] cancel hint, got:\n%s", footer)
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

func TestFooterListHintNoSessions(t *testing.T) {
	m := loadSessions(testModel(), nil) // empty list
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "l launch") {
		t.Errorf("expected l launch in no-session footer, got:\n%s", footer)
	}
	if !strings.Contains(footer, "? help") {
		t.Errorf("expected ? help in no-session footer, got:\n%s", footer)
	}
}

func TestFooterIncludesDirectKeys(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	footer := stripAnsi(renderFooter(m))
	for _, frag := range []string{"x close", "r restart"} {
		if !strings.Contains(footer, frag) {
			t.Errorf("list footer missing direct key %q, got:\n%s", frag, footer)
		}
	}
	lines := strings.Split(footer, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2-line footer, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "x close") {
		t.Error("expected actions on line 1")
	}
	if !strings.Contains(lines[1], "j/k: up/down") {
		t.Error("expected navigation on line 2")
	}
	if strings.Contains(footer, "a: more actions") {
		t.Error("actions menu hint should not appear in footer")
	}
}

func TestFooterEscHintShownWhenFiltered(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("test")
	footer := renderFooter(m)
	if !strings.Contains(footer, "esc: clear filters") {
		t.Errorf("expected esc hint when filters active, got:\n%s", footer)
	}

	m2 := loadSessions(testModel(), makeSessions())
	footer2 := renderFooter(m2)
	if strings.Contains(footer2, "esc: clear filters") {
		t.Errorf("esc hint should not appear without active filters, got:\n%s", footer2)
	}
}

// ---- Config picker mode tests ----

func TestCKeyOpensConfigPicker(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "c")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after c, got %d", m.mode)
	}
	if len(m.configPickerAccounts) != 2 {
		t.Fatalf("expected 2 accounts in picker, got %d", len(m.configPickerAccounts))
	}
}

func TestConfigPickerEscReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "c")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from config picker, got %d", m.mode)
	}
}

func TestConfigPickerNavigation(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "c") // → ModeConfigPicker, cursor=0
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
	m = pressKey(m, "c") // → ModeConfigPicker
	// After state-group sort cursor=0 is W:backend (bolt), account="work".
	// configPickerCurrent should be "work", cursor at index 0 (work).
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
	m = pressKey(m, "c")     // → ModeConfigPicker (current=work, cursor=0)
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
	// Order after sort: W:api(""), W:backend(bolt), P:dotfiles("") — move
	// cursor onto the bolt session so the active footer branch fires.
	m = pressKey(m, "j")
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "e peek") {
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

// ---- Direct-action shortcut tests ----

func TestXKeyDirectClose(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressKey(m, "x")
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after x, got %d", m.mode)
	}
	if m.confirmAction != "close" {
		t.Fatalf("expected confirmAction=close, got %q", m.confirmAction)
	}
	// Bolt sessions sort before empty-state sessions within Active, so W:backend
	// (bolt) is first, then W:api, P:dotfiles.
	if m.targetSession.Base != "W:backend" {
		t.Fatalf("expected actionTarget=W:backend, got %q", m.targetSession.Base)
	}
}

func TestRKeyDirectRestart(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressKey(m, "j") // move to second session
	m = pressKey(m, "r")
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after r, got %d", m.mode)
	}
	if m.confirmAction != "restart" {
		t.Fatalf("expected confirmAction=restart, got %q", m.confirmAction)
	}
	// Order after sort: W:backend (bolt), W:api, P:dotfiles → j moves cursor to 1.
	if m.targetSession.Base != "W:api" {
		t.Fatalf("expected actionTarget=W:api, got %q", m.targetSession.Base)
	}
}

func TestDirectKeyEmptyListNoOp(t *testing.T) {
	m := loadSessions(testModel(), nil)

	mx := pressKey(m, "x")
	if mx.mode != ModeList {
		t.Fatalf("expected ModeList after x on empty list, got %d", mx.mode)
	}

	mr := pressKey(m, "r")
	if mr.mode != ModeList {
		t.Fatalf("expected ModeList after r on empty list, got %d", mr.mode)
	}
}

// ---- Icon-first row layout tests ----

func TestFormatRowIconFirst(t *testing.T) {
	acctColor := map[string]string{"work": "blue"}
	s := session.RunningSession{
		TabID: 1, Base: "W:api", Account: "work",
		Directory: "/home/user/api", State: "bolt",
	}
	row := formatRow(s, acctColor, 120, "")
	// The bolt icon (⚡) should appear before the badge [W].
	boltIdx := strings.Index(row, "⚡")
	badgeIdx := strings.Index(row, "[W]")
	if boltIdx < 0 {
		t.Fatalf("expected bolt icon in row, got: %q", row)
	}
	if badgeIdx < 0 {
		t.Fatalf("expected [W] badge in row, got: %q", row)
	}
	if boltIdx > badgeIdx {
		t.Errorf("expected icon before badge: icon@%d badge@%d in %q", boltIdx, badgeIdx, row)
	}
}

// ---- State group sorting tests ----

func TestStateGroupSorting(t *testing.T) {
	m := testModel()
	sessions := []session.RunningSession{
		{TabID: 1, Base: "idle1", Account: "work", State: ""},
		{TabID: 2, Base: "active1", Account: "work", State: "bolt"},
		{TabID: 3, Base: "attention1", Account: "personal", State: "key"},
		{TabID: 4, Base: "attention2", Account: "personal", State: "stop"},
		{TabID: 5, Base: "idle2", Account: "work", State: "error"},
	}
	m = loadSessions(m, sessions)

	// Attention (key, stop) comes first; then Active with icon (bolt, error)
	// before empty-state Active sessions (per the no-record→Active design).
	expected := []string{"attention1", "attention2", "active1", "idle2", "idle1"}
	if len(m.filtered) != len(expected) {
		t.Fatalf("expected %d filtered sessions, got %d", len(expected), len(m.filtered))
	}
	for i, want := range expected {
		if m.filtered[i].Base != want {
			t.Errorf("filtered[%d]: expected %q, got %q", i, want, m.filtered[i].Base)
		}
	}
}

func TestRenderListGroupDividers(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 40
	sessions := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work", State: "key"},
		{TabID: 2, WindowID: 12, Base: "backend", Account: "work", State: "bolt"},
		{TabID: 3, WindowID: 13, Base: "docs", Account: "work", State: ""},
	}
	m = loadSessions(m, sessions)
	// Mark docs as stale so it lands in Idle rather than the default Active
	// no-record bucket.
	m.idleThreshold = time.Second
	m.lastActivity = map[int]time.Time{13: time.Now().Add(-time.Hour)}
	output := renderList(m)
	if !strings.Contains(output, "Needs Attention (1)") {
		t.Errorf("expected attention divider, got:\n%s", output)
	}
	if !strings.Contains(output, "Active (1)") {
		t.Errorf("expected active divider, got:\n%s", output)
	}
	if !strings.Contains(output, "Idle (1)") {
		t.Errorf("expected idle divider, got:\n%s", output)
	}
}

func TestSingleGroupNoDivider(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 40
	// Both empty-state with no activity record → both go to the Active bucket,
	// so the renderer should omit the group divider entirely.
	sessions := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: ""},
		{TabID: 2, Base: "backend", Account: "work", State: ""},
	}
	m = loadSessions(m, sessions)
	output := renderList(m)
	if strings.Contains(output, "──") {
		t.Errorf("expected no dividers when all sessions are in one group, got:\n%s", output)
	}
}


func TestFormatRowNoStateIcon(t *testing.T) {
	acctColor := map[string]string{"work": "blue"}
	s := session.RunningSession{
		TabID: 1, Base: "W:api", Account: "work",
		Directory: "/home/user/api", State: "",
	}
	row := formatRow(s, acctColor, 120, "")
	// No state icon, but row should start with 2-space placeholder then space then badge.
	if !strings.HasPrefix(row, "   ") {
		t.Errorf("expected row to start with placeholder spaces, got: %q", row)
	}
	if !strings.Contains(row, "[W]") {
		t.Errorf("expected [W] badge in row, got: %q", row)
	}
}

// ---- Enter / actions overlay tests ----

func TestEnterFocusesTab(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	updated, cmd := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil focusCmd after enter")
	}
}

func TestAOpensActionsOverlay(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	updated, cmd := m.Update(keyPress("a"))
	m2 := updated.(Model)
	if m2.mode != ModeActions {
		t.Fatalf("expected ModeActions after a, got %d", m2.mode)
	}
	if cmd != nil {
		t.Errorf("expected no immediate cmd; opening the overlay should not dispatch one")
	}
	if m2.targetSession.TabID == 0 {
		t.Error("expected targetSession populated when entering actions overlay")
	}
}

func TestSpaceFocusesTab(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	updated, cmd := m.Update(keyPress(" "))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after space, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil focusCmd after space")
	}
}

// ---- Help mode tests ----

func TestQuestionMarkOpensModeHelp(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "?")
	if m.mode != ModeHelp {
		t.Fatalf("expected ModeHelp after ?, got %d", m.mode)
	}
}

func TestHelpEscReturnsToList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "?")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from help, got %d", m.mode)
	}
}

func TestHelpQuestionMarkToggles(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "?") // open
	m = pressKey(m, "?") // close
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after second ?, got %d", m.mode)
	}
}

func TestFooterHelp(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	footer := renderFooter(m)
	if !strings.Contains(footer, "esc") {
		t.Errorf("help footer missing esc hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "j/k") {
		t.Errorf("help footer missing j/k scroll hint, got:\n%s", footer)
	}
}

// ---- Context-aware footer tests ----

func TestFooterListContextAwareAttention(t *testing.T) {
	sessions := []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", State: "key"},
	}
	m := loadSessions(testModel(), sessions)
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "c profile") {
		t.Errorf("attention footer missing c profile, got:\n%s", footer)
	}
	if !strings.Contains(footer, "C clear") {
		t.Errorf("attention footer missing C clear, got:\n%s", footer)
	}
}

func TestFooterListContextAwareActive(t *testing.T) {
	sessions := []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", State: "bolt"},
	}
	m := loadSessions(testModel(), sessions)
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "e peek") {
		t.Errorf("active footer missing e peek, got:\n%s", footer)
	}
}

// ---- Picker count tests ----

func TestPickerSelectedCountInHeader(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l") // → ModePicker

	// Nothing selected: header should not contain count.
	output := renderPicker(m)
	if strings.Contains(output, "selected]") {
		t.Errorf("expected no selected count when none selected, got:\n%s", output)
	}

	// Select one.
	m = pressKey(m, " ")
	output = renderPicker(m)
	if !strings.Contains(output, "[1 selected]") {
		t.Errorf("expected [1 selected] after selecting one, got:\n%s", output)
	}

	// Select a second.
	m = pressKey(m, "j")
	m = pressKey(m, " ")
	output = renderPicker(m)
	if !strings.Contains(output, "[2 selected]") {
		t.Errorf("expected [2 selected] after selecting two, got:\n%s", output)
	}
}

// ---- Attention row smoke test ----

func TestRenderListAttentionRowDoesNotPanic(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 40
	sessions := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "key"},
		{TabID: 2, Base: "backend", Account: "work", State: "bolt"},
	}
	m = loadSessions(m, sessions)
	_ = renderList(m) // should not panic; attention row gets orange style
}

// ---- Activity map + Idle-threshold tests ----

func TestSessionsMsgStoresActivity(t *testing.T) {
	m := testModel()
	now := time.Now()
	activity := map[int]time.Time{42: now}
	updated, _ := m.Update(sessionsMsg{
		sessions: makeSessions(),
		activity: activity,
	})
	m2 := updated.(Model)
	if m2.lastActivity == nil {
		t.Fatal("expected lastActivity to be set after sessionsMsg")
	}
	if !m2.lastActivity[42].Equal(now) {
		t.Errorf("expected activity[42]=%v, got %v", now, m2.lastActivity[42])
	}
}

// TestSessionsMsgStoresWatchdog asserts that the watchdog snapshot rides on
// sessionsMsg alongside the existing activity map and is stored on the model
// for the next render pass to consume.
func TestSessionsMsgStoresWatchdog(t *testing.T) {
	m := testModel()
	wd := map[int]daemon.WatchdogState{
		7: {LastAction: "active", LastEventAt: time.Now().Unix(), ThresholdSec: 600},
	}
	updated, _ := m.Update(sessionsMsg{
		sessions: makeSessions(),
		watchdog: wd,
	})
	m2 := updated.(Model)
	if m2.watchdog == nil {
		t.Fatal("expected watchdog to be set after sessionsMsg")
	}
	got, ok := m2.watchdog[7]
	if !ok {
		t.Fatal("expected watchdog entry for window 7")
	}
	if got.LastAction != "active" || got.ThresholdSec != 600 {
		t.Errorf("watchdog entry mismatch: %+v", got)
	}
}

func TestSessionsMsgNilActivityClassifiesAsActive(t *testing.T) {
	// With no activity map, an empty-state session must not flip to Idle.
	m := testModel()
	sessions := []session.RunningSession{
		{TabID: 1, WindowID: 1, Base: "fresh", Account: "work", State: ""},
	}
	m = loadSessions(m, sessions)
	if g := stateGroup(m.filtered[0], m.lastActivity, m.idleThreshold); g != 1 {
		t.Errorf("expected fresh empty-state session to be Active (1), got %d", g)
	}
}

func TestSessionsMsgStaleActivityFlipsToIdle(t *testing.T) {
	m := testModel()
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 9, Base: "stale", Account: "work", State: ""},
	})
	// Force a tight threshold and a stale timestamp.
	m.idleThreshold = time.Second
	m.lastActivity = map[int]time.Time{9: time.Now().Add(-time.Hour)}
	if g := stateGroup(m.filtered[0], m.lastActivity, m.idleThreshold); g != 2 {
		t.Errorf("expected stale empty-state session to be Idle (2), got %d", g)
	}
}

func TestNewModelDefaultsIdleThreshold(t *testing.T) {
	// Zero IdleThreshold in config should still produce a non-zero duration —
	// otherwise stateGroup's threshold check disables itself.
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, IdleThreshold: 0},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	if m.idleThreshold <= 0 {
		t.Errorf("expected non-zero idleThreshold default, got %v", m.idleThreshold)
	}
}

func TestNewModelHonorsConfiguredIdleThreshold(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, IdleThreshold: 42},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	if want := 42 * time.Second; m.idleThreshold != want {
		t.Errorf("expected idleThreshold=%v, got %v", want, m.idleThreshold)
	}
}

// ---- C key (clear status) tests ----

func TestCapitalCKeyReturnsClearCmd(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	updated, cmd := m.Update(keyPress("C"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList to remain after C, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from C key with sessions present")
	}
}

func TestCapitalCKeyEmptyListIsNoOp(t *testing.T) {
	m := loadSessions(testModel(), nil)
	updated, cmd := m.Update(keyPress("C"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after C on empty list, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd after C with no sessions")
	}
}

func TestCapitalCDoesNotEnterConfigPicker(t *testing.T) {
	// Confirm the case-sensitive split: uppercase C must not be aliased to
	// lowercase c (which opens the config picker).
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "C")
	if m.mode == ModeConfigPicker {
		t.Fatal("uppercase C should not open ModeConfigPicker")
	}
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after C, got %d", m.mode)
	}
}

// ---- Profile override persistence tests ----

func TestPickerProfileOverride_Persists(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "overrides.json"))

	// testModelWithSessions has: work/api, work/backend, personal/dotfiles.
	// sortPickerSessions sorts by account then title:
	//   [0] personal/dotfiles  [1] work/api  [2] work/backend
	m := testModelWithSessions()
	m = pressKey(m, "l") // open picker; cursor=0 → dotfiles (personal)

	// Open config picker for dotfiles. Accounts: [work(0), personal(1)].
	// configPickerCursor starts at 1 (personal matches dotfiles' default profile).
	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after C in picker, got %d", m.mode)
	}

	// Move cursor to work (index 0) and confirm.
	m = pressKey(m, "k")
	m = pressKey(m, "enter")

	if m.mode != ModePicker {
		t.Fatalf("expected to return to ModePicker, got %d", m.mode)
	}
	if m.pickerProfileOverride[0] != "work" {
		t.Fatalf("expected in-memory override[0]=work, got %q", m.pickerProfileOverride[0])
	}

	// Simulate a fresh dashboard: new model, open picker — override must reload from disk.
	m2 := testModelWithSessions()
	m2 = pressKey(m2, "l")
	if m2.pickerProfileOverride[0] != "work" {
		t.Fatalf("expected persisted override[0]=work after reload, got %q", m2.pickerProfileOverride[0])
	}
}

func TestClearStatusCmdSetsTabTitle(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, IdleThreshold: 300},
		Accounts: []config.Account{{Name: "work", Color: "blue"}},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")

	target := session.RunningSession{
		TabID: 7, WindowID: 99, Base: "W:api", Account: "work", Starred: false,
	}
	msg := clearStatusCmd(client, target)()
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("expected actionDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Errorf("expected nil error from mock Kitty client, got %v", done.err)
	}
	// Confirm the mock saw a kitten set-tab-title call scoped to the window.
	found := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		hasCmd, hasMatch := false, false
		for _, a := range c.Args {
			if a == "set-tab-title" {
				hasCmd = true
			}
			if a == "--match=window_id:99" {
				hasMatch = true
			}
		}
		if hasCmd && hasMatch {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected kitten set-tab-title call with --match=window_id:99, got calls: %v", runner.Calls)
	}
	_ = cfg // cfg is built to mirror real model construction; only the runner is asserted
}

// ---- Empty-list cursor safety tests ----
//
// Every `m.filtered[m.cursor]` access in ModeList is guarded by either a
// `len(m.filtered) > 0` precondition or a clamp that returns the low bound
// when the list is empty. These tests drive every interactive key against
// an empty list and assert no panic occurs, the mode stays coherent, and
// the cursor never goes negative.

// pressKeySafe wraps pressKey with a recover so a panic on one key under
// test is reported but doesn't abort the rest of the suite.
func pressKeySafe(t *testing.T, m Model, key string) Model {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic on key %q: %v", key, r)
		}
	}()
	return pressKey(m, key)
}

// TestEmptyListNavigationKeysAreSafe drives every navigation key against
// a zero-row filtered list and confirms no panic, cursor stays at 0, and
// mode stays ModeList.
func TestEmptyListNavigationKeysAreSafe(t *testing.T) {
	m := loadSessions(testModel(), nil)
	if len(m.filtered) != 0 {
		t.Fatalf("setup: expected empty filtered list, got %d", len(m.filtered))
	}
	for _, k := range []string{"j", "k", "g", "G", "enter", " "} {
		m = pressKeySafe(t, m, k)
		if m.mode != ModeList {
			t.Errorf("expected ModeList after %q on empty list, got %d", k, m.mode)
			m.mode = ModeList
		}
		if m.cursor < 0 {
			t.Errorf("cursor went negative after %q: %d", k, m.cursor)
			m.cursor = 0
		}
	}
}

// TestEmptyListRowActionKeysAreNoOp drives every row-action key against a
// zero-row filtered list and confirms each is a no-op: mode stays ModeList,
// no cmd is returned, and no state is mutated.
func TestEmptyListRowActionKeysAreNoOp(t *testing.T) {
	for _, k := range []string{"a", "e", "r", "x", "c", "y", "o", "C"} {
		t.Run("key="+k, func(t *testing.T) {
			m := loadSessions(testModel(), nil)
			updated, cmd := m.Update(keyPress(k))
			m2 := updated.(Model)
			if m2.mode != ModeList {
				t.Errorf("expected ModeList after %q on empty list, got %d", k, m2.mode)
			}
			if cmd != nil {
				t.Errorf("expected nil cmd after %q on empty list, got non-nil", k)
			}
		})
	}
}

// TestEmptyFilteredListAfterTypeBufferIsSafe drives the empty-list path via
// the type-to-filter buffer (rather than nil sessions) — exercises the
// codepath where m.sessions is non-empty but m.filtered collapses to zero.
func TestEmptyFilteredListAfterTypeBufferIsSafe(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Type a buffer that matches nothing.
	m = pressKey(m, "z")
	m = pressKey(m, "z")
	m = pressKey(m, "z")
	if len(m.filtered) != 0 {
		t.Fatalf("setup: expected zero matches for 'zzz', got %d", len(m.filtered))
	}
	// Cursor must clamp to 0 after the live narrow.
	if m.cursor != 0 {
		t.Errorf("expected cursor=0 on empty filtered, got %d", m.cursor)
	}
	// Drive j/k/G/enter/space — none must panic and cursor stays at 0.
	for _, k := range []string{"j", "k", "G", "g", "enter", " "} {
		m = pressKeySafe(t, m, k)
		if m.cursor < 0 || m.cursor > 0 {
			t.Errorf("expected cursor=0 after %q on empty filtered, got %d", k, m.cursor)
			m.cursor = 0
		}
	}
}

// TestEmptyListEscClearsTypeBufferAndRestoresList verifies the recovery
// path: after a type-buffer narrows the list to empty, Esc clears both
// the buffer and the filter so the full session list is visible again.
func TestEmptyListEscClearsTypeBufferAndRestoresList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "z")
	m = pressKey(m, "z")
	if len(m.filtered) != 0 {
		t.Fatalf("setup: expected zero matches, got %d", len(m.filtered))
	}
	m = pressKey(m, "esc")
	if m.typeBuffer != "" {
		t.Errorf("expected typeBuffer cleared after esc, got %q", m.typeBuffer)
	}
	if !m.activeFilter.IsEmpty() {
		t.Errorf("expected filter cleared after esc, got %+v", m.activeFilter)
	}
	if len(m.filtered) != len(makeSessions()) {
		t.Errorf("expected full list restored, got %d rows", len(m.filtered))
	}
}

// TestEmptyListFooterRendersWithoutPanic confirms the footer renders a
// stable list of action keys (in unavailable state) even when no row is
// selected — the renderer must not deref m.filtered[m.cursor] before the
// hasRow gate.
func TestEmptyListFooterRendersWithoutPanic(t *testing.T) {
	m := loadSessions(testModel(), nil)
	m.width, m.height = 120, 40
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("footer render panicked on empty list: %v", r)
		}
	}()
	out := stripAnsi(renderFooter(m))
	// At least one app-section key must still render — the palette is
	// fixed-order and reserves space for the row keys in their unavailable
	// style regardless of selection.
	if !strings.Contains(out, "? help") {
		t.Errorf("expected ? help in empty-list footer, got:\n%s", out)
	}
}
