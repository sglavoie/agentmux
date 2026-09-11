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

// ---- Dashboard header spacer ----

// The header no longer advertises `/`. The footer's own `/ filter` binding is
// the one declaration of it, and it is essential-band so it survives every
// width the footer does; a second spelling in the header was the duplication the
// quiet header exists to remove.
func TestRenderHeaderDoesNotRepeatTheFilterBinding(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width = 100
	m.daemonAlive = true
	m.daemonHealthCheckedAt = time.Now()
	m.hookHealth = commands.HookHealth{InSync: []string{"work"}}
	m.hookHealthCheckedAt = time.Now()

	header := stripAnsi(renderHeader(m))
	if strings.Contains(header, "to filter") {
		t.Errorf("header still advertises the filter key: %q", header)
	}
	if !strings.Contains(stripAnsi(renderFooter(m)), "/ filter") {
		t.Errorf("footer lost the filter binding the header stopped repeating:\n%s", stripAnsi(renderFooter(m)))
	}
}

func TestRenderHeaderSpacerIsOneBlankLine(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	out := stripAnsi(renderHeaderSpacer(m))
	if out != "\n" {
		t.Errorf("expected one blank spacer line, got %q", out)
	}
}

func TestRenderHeaderSpacerSuppressedOutsideModeList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeSearch
	out := renderHeaderSpacer(m)
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
