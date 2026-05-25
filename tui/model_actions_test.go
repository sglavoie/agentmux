package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

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
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessColors: map[string]string{"claude": "blue"},
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

func TestPickerOpenEditorKeyEmitsCmd(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	updated, cmd := m.Update(keyPress("O"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("expected ModePicker after O (stays in picker), got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after O in picker")
	}
}

func TestPickerOpenEditorKeyEmptyIsNoOp(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessColors: map[string]string{"claude": "blue"},
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
	updated, cmd := m.Update(keyPress("O"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("expected ModePicker after O on empty picker, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd after O on empty picker")
	}
}

func TestPickerOpenDoneMsgQuitsOnSuccess(t *testing.T) {
	m := testModel()
	_, cmd := m.Update(pickerOpenDoneMsg{err: nil})
	if cmd == nil {
		t.Fatal("expected non-nil cmd on success")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg on success, got %T", msg)
	}
}

func TestPickerOpenDoneMsgStaysOpenOnError(t *testing.T) {
	m := testModel()
	updated, cmd := m.Update(pickerOpenDoneMsg{err: errors.New("kitten exploded")})
	if updated.(Model).actionErr == nil {
		t.Fatal("expected actionErr set on error")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd (fetchCmd) on error")
	}
	// fetchCmd would emit sessionsMsg or errMsg; it must not be tea.Quit.
	// Invoking it requires a live Kitty client, so we only assert by type
	// of cmd indirectly: invoke and confirm we did NOT get a QuitMsg.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); ok {
		t.Fatal("expected non-QuitMsg on error, got tea.QuitMsg")
	}
}

func TestFooterPickerOpenEditorHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModePicker
	footer := renderFooter(m)
	if !strings.Contains(footer, "O: open in editor") {
		t.Errorf("picker footer missing open in editor hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "o: open dir") {
		t.Errorf("picker footer missing open dir hint, got:\n%s", footer)
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
	// Order after sort: api(""), backend(bolt), dotfiles("") — move
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
	// Bolt sessions sort before empty-state sessions within Active, so backend
	// (bolt) is first, then api, dotfiles.
	if m.targetSession.Base != "backend" {
		t.Fatalf("expected actionTarget=backend, got %q", m.targetSession.Base)
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
	// Order after sort: backend (bolt), api, dotfiles → j moves cursor to 1.
	if m.targetSession.Base != "api" {
		t.Fatalf("expected actionTarget=api, got %q", m.targetSession.Base)
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
	harnessColor := map[string]string{"work": "blue"}
	s := session.RunningSession{
		TabID: 1, Base: "api", Account: "work",
		Directory: "/home/user/api", State: "bolt",
	}
	row := formatRow(s, acctColor, harnessColor, 120, "")
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
		{TabID: 5, Base: "attention3", Account: "work", State: "error"},
	}
	m = loadSessions(m, sessions)

	// Attention (key, stop, error) comes first; then Active with icon (bolt)
	// before empty-state Active sessions (per the no-record→Active design).
	expected := []string{"attention1", "attention2", "attention3", "active1", "idle1"}
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
	harnessColor := map[string]string{"work": "blue"}
	s := session.RunningSession{
		TabID: 1, Base: "api", Account: "work",
		Directory: "/home/user/api", State: "",
	}
	row := formatRow(s, acctColor, harnessColor, 120, "")
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
		{TabID: 1, Base: "api", Account: "work", State: "key"},
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
		{TabID: 1, Base: "api", Account: "work", State: "bolt"},
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
