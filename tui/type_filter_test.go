package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
)

// ---- Type-to-filter dispatcher tests ----

func TestUnboundPrintableStartsTypeBuffer(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "z")
	if m.typeBuffer != "z" {
		t.Fatalf("expected typeBuffer=z, got %q", m.typeBuffer)
	}
	if m.mode != ModeList {
		t.Fatalf("expected to stay in ModeList, got %d", m.mode)
	}
}

func TestTypeBufferNarrowsList(t *testing.T) {
	// makeSessions has W:api, W:backend, P:dotfiles. We can't lead with "a"
	// (opens the actions overlay) so we type "bac" — `b` is unbound and
	// starts the buffer, then `a` and `c` divert into the buffer since it's
	// non-empty.
	m := loadSessions(testModel(), makeSessions())
	for _, ch := range "bac" {
		m = pressKey(m, string(ch))
	}
	if m.typeBuffer != "bac" {
		t.Fatalf("expected typeBuffer=bac, got %q", m.typeBuffer)
	}
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 filtered row, got %d (filter=%+v)", len(m.filtered), m.activeFilter)
	}
	if !strings.Contains(m.filtered[0].Base, "back") {
		t.Errorf("expected filtered row to contain back, got %q", m.filtered[0].Base)
	}
}

func TestRowActionDivertsWhenBufferNonEmpty(t *testing.T) {
	// Once the buffer has at least one char, pressing `r` should NOT enter
	// restart-confirm; it should append to the buffer.
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "z") // start buffer
	m = pressKey(m, "r") // would normally restart; now diverts
	if m.mode != ModeList {
		t.Fatalf("expected to stay in ModeList, got %d", m.mode)
	}
	if m.typeBuffer != "zr" {
		t.Errorf("expected typeBuffer=zr, got %q", m.typeBuffer)
	}
}

func TestRowActionStillWorksWhenBufferEmpty(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.cfg.Defaults.ConfirmDestructive = true
	m = pressKey(m, "r")
	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after r with empty buffer, got %d", m.mode)
	}
	if m.confirmAction != "restart" {
		t.Errorf("expected restart confirm, got %q", m.confirmAction)
	}
}

func TestEscClearsTypeBufferAndFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	for _, ch := range "bac" {
		m = pressKey(m, string(ch))
	}
	if m.typeBuffer == "" {
		t.Fatal("setup: expected non-empty typeBuffer")
	}
	m = pressKey(m, "esc")
	if m.typeBuffer != "" {
		t.Errorf("expected typeBuffer cleared, got %q", m.typeBuffer)
	}
	if !m.activeFilter.IsEmpty() {
		t.Errorf("expected activeFilter cleared, got %+v", m.activeFilter)
	}
}

func TestBackspacePopsTypeBuffer(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "z")
	m = pressKey(m, "y") // y is a row action; diverts since buffer non-empty
	if m.typeBuffer != "zy" {
		t.Fatalf("setup: expected zy, got %q", m.typeBuffer)
	}
	m = pressKey(m, "backspace")
	if m.typeBuffer != "z" {
		t.Errorf("expected backspace to pop one char, got %q", m.typeBuffer)
	}
	m = pressKey(m, "backspace")
	if m.typeBuffer != "" {
		t.Errorf("expected backspace to empty buffer, got %q", m.typeBuffer)
	}
}

func TestEnterCommitsTypeBuffer(t *testing.T) {
	// With a non-empty buffer, enter should clear the buffer but keep the
	// filter applied; mode stays ModeList.
	m := loadSessions(testModel(), makeSessions())
	for _, ch := range "bac" {
		m = pressKey(m, string(ch))
	}
	if m.typeBuffer != "bac" || m.activeFilter.IsEmpty() {
		t.Fatalf("setup: expected buffered filter, got buf=%q filter=%+v", m.typeBuffer, m.activeFilter)
	}
	updated, cmd := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", m2.mode)
	}
	if m2.typeBuffer != "" {
		t.Errorf("expected typeBuffer cleared on commit, got %q", m2.typeBuffer)
	}
	if m2.activeFilter.IsEmpty() {
		t.Errorf("expected filter to remain applied after commit, got empty")
	}
	if cmd != nil {
		t.Errorf("expected no cmd on commit, got non-nil")
	}
}

func TestNavigationLiveDuringTypeBuffer(t *testing.T) {
	// j/k should still move the cursor even when the type buffer has content.
	// Use sessions whose Base shares a substring "wo" (matching their account
	// "work") so the buffer leaves multiple rows visible to navigate.
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, Base: "W:alpha", Account: "work"},
		{TabID: 2, Base: "W:beta", Account: "work"},
		{TabID: 3, Base: "W:gamma", Account: "work"},
	})
	m = pressKey(m, "w") // unbound; starts buffer
	m = pressKey(m, "o")
	if m.typeBuffer != "wo" {
		t.Fatalf("setup: expected typeBuffer=wo, got %q", m.typeBuffer)
	}
	if len(m.filtered) < 2 {
		t.Fatalf("setup: expected >1 filtered rows, got %d", len(m.filtered))
	}
	prev := m.cursor
	m = pressKey(m, "j")
	if m.cursor == prev {
		t.Errorf("expected cursor to move on j with buffer; cursor stayed at %d", m.cursor)
	}
}

func TestSlashCarriesTypeBufferIntoModeSearch(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	for _, ch := range "ba" {
		m = pressKey(m, string(ch))
	}
	m = pressKey(m, "/")
	if m.mode != ModeSearch {
		t.Fatalf("expected ModeSearch after /, got %d", m.mode)
	}
	if m.filterText != "ba" {
		t.Errorf("expected prefill to carry typeBuffer (ba), got %q", m.filterText)
	}
	if m.typeBuffer != "" {
		t.Errorf("expected typeBuffer cleared after handoff, got %q", m.typeBuffer)
	}
}

func TestQQuitsRegardlessOfTypeBuffer(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	for _, ch := range "bac" {
		m = pressKey(m, string(ch))
	}
	_, cmd := m.Update(keyPress("q"))
	if cmd == nil {
		t.Error("expected q to dispatch a quit cmd even with non-empty buffer")
	}
}

// ---- Completion popup from ModeList ----

func TestTypingSigilOpensCompletionInListMode(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "@")
	if !m.completion.Visible {
		t.Fatalf("expected completion popup visible after @; got %+v", m.completion)
	}
	if m.completion.Sigil != '@' {
		t.Errorf("expected sigil @, got %q", m.completion.Sigil)
	}
}

func TestCompletionEnterInsertsFromList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "@")
	if !m.completion.Visible {
		t.Fatalf("setup: expected completion popup visible")
	}
	updated, _ := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected to stay in ModeList after enter on popup, got %d", m2.mode)
	}
	if !strings.HasPrefix(m2.typeBuffer, "@") {
		t.Errorf("expected typeBuffer to start with @, got %q", m2.typeBuffer)
	}
	if !strings.Contains(m2.typeBuffer, " ") {
		t.Errorf("expected trailing space after insert, got %q", m2.typeBuffer)
	}
}

// ---- Inline filter rendering ----

func TestRenderInlineFilterPlaceholder(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	out := stripAnsi(renderInlineFilter(m))
	if !strings.Contains(out, inlineFilterPlaceholder) {
		t.Errorf("expected placeholder in inline filter, got %q", out)
	}
}

func TestRenderInlineFilterWithBuffer(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.typeBuffer = "ap"
	out := stripAnsi(renderInlineFilter(m))
	if strings.Contains(out, inlineFilterPlaceholder) {
		t.Errorf("expected placeholder hidden when buffer non-empty, got %q", out)
	}
	if !strings.Contains(out, "ap") {
		t.Errorf("expected buffer in inline filter, got %q", out)
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

