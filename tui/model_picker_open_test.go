package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
	m := pressKey(testModel(), "l")
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
	m := pressKey(testModel(), "l")
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
