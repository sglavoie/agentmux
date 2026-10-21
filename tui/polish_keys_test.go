package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
)

func TestSessionDetailQuestionMarkOpensHelpAndReturns(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeDetail
	m.detailMetadata = true
	m = pressKey(m, "?")
	if m.mode != ModeHelp || m.helpReturnMode != ModeDetail {
		t.Fatalf("? in session detail = mode %v returning to %v, want help over detail", m.mode, m.helpReturnMode)
	}
	m = pressKey(m, "esc")
	if m.mode != ModeDetail || !m.detailMetadata {
		t.Fatalf("closing help = mode %v metadata %v, want detail as it was", m.mode, m.detailMetadata)
	}
}

func TestHistoryOnlyFooterSaysEscQuits(t *testing.T) {
	m := Model{mode: ModeSessionSearch, width: 220, historyOnly: true}
	if got := stripAnsi(renderFooter(m)); !strings.Contains(got, "esc quit") || strings.Contains(got, "esc back") {
		t.Fatalf("standalone Past Sessions footer = %q, want esc quit", got)
	}
	m.sessionQuery = "api"
	if got := stripAnsi(renderFooter(m)); !strings.Contains(got, "esc back") {
		t.Fatalf("with a query to clear the footer = %q, want the shared label", got)
	}
	m = Model{mode: ModeSessionSearch, width: 220}
	if got := stripAnsi(renderFooter(m)); !strings.Contains(got, "esc back") {
		t.Fatalf("Past Sessions over the dashboard = %q, want esc back", got)
	}
}

func TestDashboardHelpListsRecoverKey(t *testing.T) {
	found := false
	for _, line := range helpDashboardSection() {
		if strings.HasPrefix(line, "  u ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("generated Dashboard help omits u:\n%s", strings.Join(helpDashboardSection(), "\n"))
	}
	binding, ok := dashboardBindings().Of(commands.UndoClose)
	if !ok || binding.Key() != "u" {
		t.Fatal("u is no longer the recover binding")
	}
}

func TestCtrlFAndCtrlBPageTheDashboardLikePgDnPgUp(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.height = 12
	paged := pressKey(m, "pgdown")
	ctrlF := pressKey(m, "ctrl+f")
	if paged.cursor == m.cursor || ctrlF.cursor != paged.cursor {
		t.Fatalf("ctrl+f cursor %d, pgdn cursor %d, from %d", ctrlF.cursor, paged.cursor, m.cursor)
	}
	if back := pressKey(ctrlF, "ctrl+b"); back.cursor != pressKey(paged, "pgup").cursor {
		t.Fatalf("ctrl+b cursor %d, want pgup's", back.cursor)
	}
}

// Esc inside a list's query prompt cancels the edit, restoring the query the
// prompt opened on, as the dashboard's filter prompt does; enter keeps it.
func TestEscInQueryPromptsRestoresThePreviousQuery(t *testing.T) {
	m := testModel()
	m.mode = ModeSessionSearch
	m.sessionQuery = "old"
	m = typeString(pressKey(m, "/"), "x")
	if m.sessionQuery != "oldx" {
		t.Fatalf("typing appended %q", m.sessionQuery)
	}
	if got := pressKey(m, "esc"); got.sessionSearchMode || got.sessionQuery != "old" {
		t.Fatalf("Past Sessions esc = search %v query %q, want old restored", got.sessionSearchMode, got.sessionQuery)
	}
	if got := pressKey(m, "enter"); got.sessionQuery != "oldx" {
		t.Fatalf("Past Sessions enter = %q, want the edit kept", got.sessionQuery)
	}

	p := testModel()
	p.mode = ModePlanSearch
	p.planQuery = "old"
	p = typeString(pressKey(p, "/"), "x")
	if got := pressKey(p, "esc"); got.planSearchMode || got.planQuery != "old" {
		t.Fatalf("Plans esc = search %v query %q, want old restored", got.planSearchMode, got.planQuery)
	}

	w := testModel()
	w.mode = ModeWorktrees
	w.worktreeFilter = "old"
	w = typeString(pressKey(w, "/"), "x")
	if got := pressKey(w, "esc"); got.worktreeFilterMode || got.worktreeFilter != "old" {
		t.Fatalf("Worktrees esc = input %v filter %q, want old restored", got.worktreeFilterMode, got.worktreeFilter)
	}
}
