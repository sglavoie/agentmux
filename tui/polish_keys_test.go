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
	if got := stripAnsi(renderFooter(m)); !strings.Contains(got, "esc clear search") {
		t.Fatalf("with a query to clear the footer = %q, want esc clear search", got)
	}
	m = Model{mode: ModeSessionSearch, width: 220}
	if got := stripAnsi(renderFooter(m)); !strings.Contains(got, "esc back") {
		t.Fatalf("Past Sessions over the dashboard = %q, want esc back", got)
	}
}

// Esc clears a set query or filter before it leaves Past Sessions, Plans, or
// Worktrees, so the footer names that first step rather than promising back.
func TestFooterEscLabelClearsFilterFirst(t *testing.T) {
	cases := []struct {
		name  string
		m     Model
		set   func(*Model)
		clear string
	}{
		{"past sessions", Model{mode: ModeSessionSearch, width: 220}, func(m *Model) { m.sessionQuery = "api" }, "esc clear search"},
		{"plans", Model{mode: ModePlanSearch, width: 220}, func(m *Model) { m.planQuery = "api" }, "esc clear search"},
		{"worktrees", Model{mode: ModeWorktrees, width: 220}, func(m *Model) { m.worktreeFilter = "api" }, "esc clear filter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.m
			if got := stripAnsi(renderFooter(m)); !strings.Contains(got, "esc back") || strings.Contains(got, tc.clear) {
				t.Fatalf("without a filter the footer = %q, want esc back", got)
			}
			tc.set(&m)
			if got := stripAnsi(renderFooter(m)); !strings.Contains(got, tc.clear) || strings.Contains(got, "esc back") {
				t.Fatalf("with a filter set the footer = %q, want %s", got, tc.clear)
			}
		})
	}
}

// q leaves Past Sessions, Plans, and Worktrees in one step, as it leaves the
// launcher, even with a query set; esc is the key that clears it first.
func TestQLeavesListViewsInOneStep(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{"past sessions", Model{mode: ModeSessionSearch, width: 220, sessionQuery: "api"}},
		{"plans", Model{mode: ModePlanSearch, width: 220, planQuery: "api"}},
		{"worktrees", Model{mode: ModeWorktrees, width: 220, worktreeFilter: "api"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pressKey(tc.m, "q"); got.mode == tc.m.mode {
				t.Fatalf("q with a query set stayed in mode %v", got.mode)
			}
			if got := pressKey(tc.m, "esc"); got.mode != tc.m.mode {
				t.Fatalf("esc with a query set left for mode %v; it clears the query first", got.mode)
			}
		})
	}
}

func TestSentenceCaseCapitalizesAWideFirstLetter(t *testing.T) {
	if got := sentenceCase("échec de la copie"); got != "Échec de la copie" {
		t.Fatalf("sentenceCase = %q", got)
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
