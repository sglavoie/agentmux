package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

// ---- Per-row capabilities, reached through the actions overlay ----
//
// Close and Restart used to be `X` and `r` pressed straight at the list. Both
// are overlay entries now, so what these assert is that the capability survived
// the re-spelling: the same confirmation, against the same cursor row.

func TestCloseActionConfirmsAgainstTheCursorRow(t *testing.T) {
	t.Parallel()

	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressRowAction(t, m, keyClose)
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after the close action, got %d", m.mode)
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

func TestRestartActionConfirmsAgainstTheCursorRow(t *testing.T) {
	t.Parallel()

	m := loadSessions(modalConfirmModel(), makeSessions())
	m = pressKey(m, "j") // move to second session
	m = pressRowAction(t, m, keyR)
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after the restart action, got %d", m.mode)
	}
	if m.confirmAction != "restart" {
		t.Fatalf("expected confirmAction=restart, got %q", m.confirmAction)
	}
	// Order after sort: backend (bolt), api, dotfiles → j moves cursor to 1.
	if m.targetSession.Base != "api" {
		t.Fatalf("expected actionTarget=api, got %q", m.targetSession.Base)
	}
}

// TestRetiredDirectActionKeysDoNotDispatch is the other half. `X` and `r` are
// unbound at the list rather than rebound, so pressing one changes nothing at
// all — not even on a dashboard with a row under the cursor, which is the state
// where the old binding would have fired.
func TestRetiredDirectActionKeysDoNotDispatch(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"X", "r", "x", "c", "C", "e", "o", "O", "y", "m", "L", " "} {
		t.Run(key, func(t *testing.T) {
			m := loadSessions(modalConfirmModel(), makeSessions())
			before := m.cursor

			m = pressKey(m, key)

			if m.mode != ModeList {
				t.Fatalf("%q left ModeList for mode=%d", key, m.mode)
			}
			if m.cursor != before {
				t.Errorf("%q moved the cursor %d -> %d", key, before, m.cursor)
			}
			if m.confirmAction != "" || m.targetSession.Base != "" {
				t.Errorf("%q armed action %q on %q", key, m.confirmAction, m.targetSession.Base)
			}
		})
	}
}

// The overlay is what refuses on an empty list now, and it refuses by not
// opening: there is no row to act on, so `a` itself is unavailable.
func TestActionsOverlayDoesNotOpenOnAnEmptyList(t *testing.T) {
	t.Parallel()

	m := pressKey(loadSessions(testModel(), nil), keyA)
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after %q on an empty list, got %d", keyA, m.mode)
	}
}

// ---- Enter / actions overlay tests ----

func TestEnterFocusesTab(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

// Space was a second spelling of Enter and is retired: one key focuses a row
// now, and the space bar dispatches nothing.
func TestSpaceNoLongerFocusesTab(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), makeSessions())
	updated, cmd := m.Update(keyPress(" "))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after space, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("space still dispatched a command")
	}
}

// ---- Help mode tests ----

func TestQuestionMarkOpensModeHelp(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "?")
	if m.mode != ModeHelp {
		t.Fatalf("expected ModeHelp after ?, got %d", m.mode)
	}
}

func TestHelpEscReturnsToList(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "?")
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from help, got %d", m.mode)
	}
}

func TestHelpQuestionMarkToggles(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "?") // open
	m = pressKey(m, "?") // close
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after second ?, got %d", m.mode)
	}
}

func TestFooterHelp(t *testing.T) {
	t.Parallel()

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

// ---- The footer no longer varies with the cursor row's state ----

// TestFooterListIsTheSameRowWhateverTheCursorHolds replaces the two
// context-aware footer tests. The footer used to swap per-row command hints in
// and out as the cursor moved between an attention row and an active one, which
// is exactly the churn the small keymap removes: the per-row commands are behind
// `a` now, and `a` says the same thing on every row. Only availability still
// varies, and that is asserted in TestListFooterAvailability.
func TestFooterListIsTheSameRowWhateverTheCursorHolds(t *testing.T) {
	t.Parallel()

	render := func(state string) string {
		m := loadSessions(testModel(), []session.RunningSession{
			{TabID: 1, Base: "api", Account: "work", State: state},
		})
		m.width, m.height = 140, 24
		return stripAnsi(renderFooter(m))
	}

	attention := render("key")
	if !strings.Contains(attention, "a actions") {
		t.Fatalf("footer missing the route to the per-row commands, got:\n%s", attention)
	}
	for _, state := range []string{"bolt", ""} {
		if got := render(state); got != attention {
			t.Errorf("footer for state %q differs from the attention footer:\n%s\n%s", state, attention, got)
		}
	}
}
