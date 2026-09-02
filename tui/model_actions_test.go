package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

// ---- Direct-action shortcut tests ----

func TestXKeyDirectClose(t *testing.T) {
	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressKey(m, "X")
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after X, got %d", m.mode)
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

	mx := pressKey(m, "X")
	if mx.mode != ModeList {
		t.Fatalf("expected ModeList after X on empty list, got %d", mx.mode)
	}

	mr := pressKey(m, "r")
	if mr.mode != ModeList {
		t.Fatalf("expected ModeList after r on empty list, got %d", mr.mode)
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
		t.Fatal("expected non-nil commands.Focus after enter")
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
		t.Fatal("expected non-nil commands.Focus after space")
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
	if !strings.Contains(footer, "C launch") {
		t.Errorf("attention footer missing C launch, got:\n%s", footer)
	}
	if !strings.Contains(footer, "x clear") {
		t.Errorf("attention footer missing x clear, got:\n%s", footer)
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
