package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/tui/commands"
)

// ---- Completion popup from explicit dashboard search ----

func TestTypingSigilDoesNotOpenCompletionInListMode(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "@")
	if m.completion.Visible {
		t.Fatalf("expected no completion popup before /, got %+v", m.completion)
	}
	if m.mode != ModeList {
		t.Fatalf("expected to stay in ModeList, got %d", m.mode)
	}
	if !m.activeFilter.IsEmpty() {
		t.Fatalf("expected bare @ not to filter in ModeList, got %+v", m.activeFilter)
	}
}

func TestCompletionEnterInsertsFromExplicitSearch(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = pressKey(m, "@")
	if !m.completion.Visible {
		t.Fatalf("setup: expected completion popup visible")
	}
	updated, _ := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModeSearch {
		t.Fatalf("expected to stay in ModeSearch after completion insert, got %d", m2.mode)
	}
	if !strings.HasPrefix(m2.filterText, "@") {
		t.Errorf("expected filterText to start with @, got %q", m2.filterText)
	}
	if !strings.Contains(m2.filterText, " ") {
		t.Errorf("expected trailing space after insert, got %q", m2.filterText)
	}
}

// ---- Dashboard filter hint rendering ----

func TestRenderHeaderShowsFilterHintBeforeHealthPips(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width = 100
	m.daemonAlive = true
	m.daemonHealthCheckedAt = time.Now()
	m.hookHealth = commands.HookHealth{InSync: []string{"work"}}
	m.hookHealthCheckedAt = time.Now()

	firstLine := strings.Split(stripAnsi(renderHeader(m)), "\n")[0]
	filterIdx := strings.Index(firstLine, inlineFilterPlaceholder)
	daemonIdx := strings.Index(firstLine, "daemon")
	if filterIdx < 0 {
		t.Fatalf("header filter hint missing; header=%q", firstLine)
	}
	if daemonIdx < 0 {
		t.Fatalf("daemon pip missing; header=%q", firstLine)
	}
	if filterIdx > daemonIdx {
		t.Errorf("filter hint should render before daemon/hooks pips; header=%q", firstLine)
	}
}

func TestRenderInlineFilterSpacer(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	out := stripAnsi(renderInlineFilter(m))
	if out != "\n" {
		t.Errorf("expected one blank spacer line, got %q", out)
	}
	if strings.Contains(out, inlineFilterPlaceholder) {
		t.Errorf("inline spacer should not repeat dashboard filter hint, got %q", out)
	}
	if strings.Contains(out, "type to filter") {
		t.Errorf("dashboard hint should not advertise implicit typing, got %q", out)
	}
}

func TestRenderInlineFilterSuppressedOutsideModeList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeSearch
	out := renderInlineFilter(m)
	if out != "" {
		t.Errorf("expected spacer empty outside ModeList, got %q", out)
	}
}

func TestDigitsRemoveChipsFromExplicitDashboardFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = typeString(m, "@work")
	if m.completion.Visible {
		m = pressKey(m, "esc")
	}
	m = pressKey(m, "enter")
	if m.mode != ModeList {
		t.Fatalf("setup: expected ModeList after enter, got %d", m.mode)
	}
	before := len(m.activeFilter.Components())
	if before < 1 {
		t.Fatalf("setup: expected at least one chip, got %d", before)
	}
	m = pressKey(m, "1")
	if got := len(m.activeFilter.Components()); got != before-1 {
		t.Errorf("expected chip count to drop from %d to %d, got %d", before, before-1, got)
	}
}
