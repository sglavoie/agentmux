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

// Removing a single chip from the keyboard is an edit of the query that
// produced it: `/` reopens the buffer on the committed filter and backspace
// takes the last component off the end. The chip ring that `1`–`9` used to
// focus is retired, so this is the whole keyboard route, and it is the reason
// entering the editor stopped clearing the thing being edited.
func TestBackspaceInTheFilterEditorRemovesOneChip(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = typeString(m, "@work api")
	if m.completion.Visible {
		m = pressKey(m, "esc")
	}
	m = pressKey(m, "enter")
	if m.mode != ModeList {
		t.Fatalf("setup: expected ModeList after enter, got %d", m.mode)
	}
	before := m.activeFilter.Components()
	if len(before) != 2 {
		t.Fatalf("setup: expected two chips, got %d (%+v)", len(before), m.activeFilter)
	}

	m = pressKey(m, "/")
	if m.filterText != "@work api" {
		t.Fatalf("the editor opened on %q, want the committed filter", m.filterText)
	}
	for range len("api") {
		m = pressKey(m, keyBackspace)
	}
	if m.completion.Visible {
		m = pressKey(m, "esc")
	}
	m = pressKey(m, "enter")

	after := m.activeFilter.Components()
	if len(after) != 1 {
		t.Fatalf("expected one chip left, got %d (%+v)", len(after), m.activeFilter)
	}
	// The chips render in their own order rather than the buffer's, so the
	// survivor is named rather than indexed: what backspace took off the end of
	// the text is what must be missing here.
	if got := after[0].Label(); got != "@work" {
		t.Errorf("backspacing the last component left the %q chip, want the untouched @work", got)
	}
	if m.activeFilter.Substrings != nil {
		t.Errorf("the substring chip survived as %v", m.activeFilter.Substrings)
	}
}

// TestDigitsAreInertAtTheDashboard is the other half: the digits addressed
// chips by position, and a list that no longer numbers them must not act on one
// either.
func TestDigitsAreInertAtTheDashboard(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = typeString(m, "@work")
	if m.completion.Visible {
		m = pressKey(m, "esc")
	}
	m = pressKey(m, "enter")
	before := len(m.activeFilter.Components())
	if before < 1 {
		t.Fatalf("setup: expected at least one chip, got %d", before)
	}

	for _, key := range []string{"1", "2", "9"} {
		updated, cmd := m.Update(keyPress(key))
		got := updated.(Model)
		if cmd != nil {
			t.Errorf("%q dispatched a command", key)
		}
		if n := len(got.activeFilter.Components()); n != before {
			t.Errorf("%q changed the chip count %d -> %d", key, before, n)
		}
		if got.mode != ModeList {
			t.Errorf("%q left ModeList for mode=%d", key, got.mode)
		}
	}
}
