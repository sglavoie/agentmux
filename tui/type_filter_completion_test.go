package tui

import (
	"strings"
	"testing"
)

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
	m = typeString(m, "ba")
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
	m = typeString(m, "ba")
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
	m = typeString(m, "ba")
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
	// Esc closes the popup but keeps the buffer (popup-branch behaviour).
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
	m = typeString(m, "@work")
	// Insert an account completion if a popup is open.
	if m.completion.Visible {
		m = pressKey(m, "enter")
	}
	// Add a substring chip too.
	m = typeString(m, "api")
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
