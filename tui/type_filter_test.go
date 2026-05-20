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

// ---- Type-buffer policy across mode transitions ----
//
// Policy (documented near the keyQuestion / keyL handlers in model.go):
//   `/` carries typeBuffer forward as ModeSearch prefill.
//   `f` clears typeBuffer (entering search with an `@` prefill instead).
//   `?` (help) and `l` (picker) *preserve* typeBuffer + completion state
//   so returning to ModeList resumes the user's in-progress narrow.
//   `Esc` and `Enter` (in ModeList) clear typeBuffer.

// TestTypeBufferPersistsAcrossHelp verifies that entering ModeHelp leaves
// the type buffer and live-applied activeFilter untouched, and that the
// state survives a round trip back to ModeList via esc.
func TestTypeBufferPersistsAcrossHelp(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "b") // start type buffer (b is unbound)
	m = pressKey(m, "a") // diverts into buffer since non-empty
	if m.typeBuffer != "ba" {
		t.Fatalf("setup: expected typeBuffer=ba, got %q", m.typeBuffer)
	}
	priorFilterRaw := m.activeFilter.Raw

	m = pressKey(m, "?")
	if m.mode != ModeHelp {
		t.Fatalf("expected ModeHelp after ?, got %d", m.mode)
	}
	if m.typeBuffer != "ba" {
		t.Errorf("expected typeBuffer preserved in ModeHelp, got %q", m.typeBuffer)
	}
	if m.activeFilter.Raw != priorFilterRaw {
		t.Errorf("expected activeFilter preserved in ModeHelp: was %q, got %q",
			priorFilterRaw, m.activeFilter.Raw)
	}

	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from help, got %d", m.mode)
	}
	if m.typeBuffer != "ba" {
		t.Errorf("expected typeBuffer preserved on return from help, got %q", m.typeBuffer)
	}
	if m.activeFilter.Raw != priorFilterRaw {
		t.Errorf("expected activeFilter preserved on return: was %q, got %q",
			priorFilterRaw, m.activeFilter.Raw)
	}
}

// TestTypeBufferPersistsAcrossPicker verifies that opening the picker
// preserves the user's in-progress ModeList type buffer and that exiting
// the picker via esc restores the buffer + filter unchanged.
func TestTypeBufferPersistsAcrossPicker(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "b") // start type buffer
	m = pressKey(m, "a")
	if m.typeBuffer != "ba" {
		t.Fatalf("setup: expected typeBuffer=ba, got %q", m.typeBuffer)
	}
	priorFilterRaw := m.activeFilter.Raw

	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	if m.typeBuffer != "ba" {
		t.Errorf("expected typeBuffer preserved when entering picker, got %q", m.typeBuffer)
	}
	if m.activeFilter.Raw != priorFilterRaw {
		t.Errorf("expected activeFilter preserved when entering picker: was %q, got %q",
			priorFilterRaw, m.activeFilter.Raw)
	}

	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from picker, got %d", m.mode)
	}
	if m.typeBuffer != "ba" {
		t.Errorf("expected typeBuffer preserved on return from picker, got %q", m.typeBuffer)
	}
	if m.activeFilter.Raw != priorFilterRaw {
		t.Errorf("expected activeFilter preserved on return from picker: was %q, got %q",
			priorFilterRaw, m.activeFilter.Raw)
	}
}

// TestCompletionPopupSurvivesHelpRoundTrip locks in that the completion
// popup state is preserved across a ModeHelp round trip — consistent with
// typeBuffer preservation since the popup is computed from the buffer.
// Without this, pressing `?` while a sigil completion is active would
// silently drop the popup and require the user to re-trigger it.
func TestCompletionPopupSurvivesHelpRoundTrip(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "@") // sigil opens completion popup
	if !m.completion.Visible {
		t.Fatalf("setup: expected completion popup visible after @")
	}
	priorCands := append([]string(nil), m.completion.Candidates...)

	m = pressKey(m, "?")
	if m.mode != ModeHelp {
		t.Fatalf("expected ModeHelp after ?, got %d", m.mode)
	}
	if !m.completion.Visible {
		t.Errorf("expected completion popup state preserved in ModeHelp")
	}

	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc, got %d", m.mode)
	}
	if !m.completion.Visible {
		t.Errorf("expected completion popup visible on return from help")
	}
	if len(m.completion.Candidates) != len(priorCands) {
		t.Errorf("expected %d candidates preserved, got %d",
			len(priorCands), len(m.completion.Candidates))
	}
}

// TestCompletionPopupSurvivesPickerRoundTrip mirrors the help round-trip
// for the picker — the popup state must be consistent when control
// returns to ModeList because it's tied to the preserved typeBuffer.
func TestCompletionPopupSurvivesPickerRoundTrip(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "@")
	if !m.completion.Visible {
		t.Fatalf("setup: expected completion popup visible after @")
	}

	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	if !m.completion.Visible {
		t.Errorf("expected completion popup state preserved in ModePicker")
	}

	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc, got %d", m.mode)
	}
	if !m.completion.Visible {
		t.Errorf("expected completion popup visible on return from picker")
	}
}

// TestCompletionPopupClearedOnEnterCommit asserts the documented clearing
// behavior: pressing Enter while the type buffer is non-empty commits the
// filter, empties the buffer, AND closes the popup so subsequent row-action
// keys are routed normally.
func TestCompletionPopupClearedOnEnterCommit(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "b")
	m = pressKey(m, "a")
	if m.typeBuffer == "" {
		t.Fatalf("setup: expected non-empty typeBuffer")
	}
	m = pressKey(m, "enter")
	if m.typeBuffer != "" {
		t.Errorf("expected typeBuffer cleared after enter commit, got %q", m.typeBuffer)
	}
	if m.completion.Visible {
		t.Errorf("expected completion popup closed after enter commit")
	}
}

// TestCompletionPopupClearedOnEscClear asserts that Esc in ModeList with a
// non-empty type buffer clears both the buffer and the completion popup.
func TestCompletionPopupClearedOnEscClear(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "@") // opens completion popup
	if !m.completion.Visible {
		t.Fatalf("setup: expected completion popup visible")
	}
	// Esc on the popup branch (line 346) closes it but keeps the buffer.
	m = pressKey(m, "esc")
	if m.completion.Visible {
		t.Errorf("expected completion popup closed after esc on popup, got visible")
	}
}

// TestDigitsAppendToTypeBufferOnceActive verifies that once the user has
// started typing into the type-to-filter buffer, the digit keys 1..9 stop
// behaving as chip-removal shortcuts and instead append to the buffer.
// Otherwise titles containing digits (e.g. "v2", "py3") could not be
// matched mid-typing — the keystroke would silently no-op when no chip
// occupies that slot, or worse, delete a chip the user did not intend
// to remove.
func TestDigitsAppendToTypeBufferOnceActive(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "z") // start buffer with an unbound printable
	if m.typeBuffer != "z" {
		t.Fatalf("setup: expected typeBuffer=z, got %q", m.typeBuffer)
	}
	m = pressKey(m, "2") // digit should divert into the buffer
	if m.typeBuffer != "z2" {
		t.Errorf("expected typeBuffer=z2 after digit divert, got %q", m.typeBuffer)
	}
	if m.mode != ModeList {
		t.Errorf("expected to stay in ModeList, got %d", m.mode)
	}
}

// TestDigitsRemoveChipsWhenBufferEmpty pins down the dual role of the digit
// keys: when the buffer is empty they still remove the Nth chip from the
// active filter, so the documented shortcut keeps working.
func TestDigitsRemoveChipsWhenBufferEmpty(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Install a two-component filter by going through ModeSearch so the
	// activeFilter exposes two removable chips.
	m = pressKey(m, "/")
	for _, ch := range "@work" {
		m = pressKey(m, string(ch))
	}
	// Insert an account completion if a popup is open.
	if m.completion.Visible {
		m = pressKey(m, "enter")
	}
	// Add a substring chip too.
	for _, ch := range "api" {
		m = pressKey(m, string(ch))
	}
	m = pressKey(m, "enter") // commit, back to ModeList
	if m.mode != ModeList {
		t.Fatalf("setup: expected ModeList after enter, got %d", m.mode)
	}
	before := len(m.activeFilter.Components())
	if before < 1 {
		t.Fatalf("setup: expected at least one chip, got %d", before)
	}
	if m.typeBuffer != "" {
		t.Fatalf("setup: expected empty typeBuffer, got %q", m.typeBuffer)
	}
	m = pressKey(m, "1") // remove first chip
	if got := len(m.activeFilter.Components()); got != before-1 {
		t.Errorf("expected chip count to drop from %d to %d, got %d", before, before-1, got)
	}
}

