package tui

import (
	"strings"
	"testing"
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

// ---- Inline filter rendering ----

func TestRenderInlineFilterPlaceholder(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	out := stripAnsi(renderInlineFilter(m))
	if !strings.Contains(out, inlineFilterPlaceholder) {
		t.Errorf("expected placeholder in inline filter, got %q", out)
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
		t.Errorf("expected inline filter empty outside ModeList, got %q", out)
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
