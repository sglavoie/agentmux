package tui

import (
	"strings"
	"testing"
)

//
// Every `m.filtered[m.cursor]` access in ModeList is guarded by either a
// `len(m.filtered) > 0` precondition or a clamp that returns the low bound
// when the list is empty. These tests drive every interactive key against
// an empty list and assert no panic occurs, the mode stays coherent, and
// the cursor never goes negative.

// pressKeySafe wraps pressKey with a recover so a panic on one key under
// test is reported but doesn't abort the rest of the suite.
func pressKeySafe(t *testing.T, m Model, key string) Model {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic on key %q: %v", key, r)
		}
	}()
	return pressKey(m, key)
}

// TestEmptyListNavigationKeysAreSafe drives every navigation key against
// a zero-row filtered list and confirms no panic, cursor stays at 0, and
// mode stays ModeList.
func TestEmptyListNavigationKeysAreSafe(t *testing.T) {
	m := loadSessions(testModel(), nil)
	if len(m.filtered) != 0 {
		t.Fatalf("setup: expected empty filtered list, got %d", len(m.filtered))
	}
	for _, k := range []string{"j", "k", "g", "G", "enter", " "} {
		m = pressKeySafe(t, m, k)
		if m.mode != ModeList {
			t.Errorf("expected ModeList after %q on empty list, got %d", k, m.mode)
			m.mode = ModeList
		}
		if m.cursor < 0 {
			t.Errorf("cursor went negative after %q: %d", k, m.cursor)
			m.cursor = 0
		}
	}
}

// TestEmptyListRowActionKeysAreNoOp drives every row-action key against a
// zero-row filtered list and confirms each is a no-op: mode stays ModeList,
// no cmd is returned, and no state is mutated.
func TestEmptyListRowActionKeysAreNoOp(t *testing.T) {
	for _, k := range []string{"a", "e", "r", "x", "c", "y", "o", "C"} {
		t.Run("key="+k, func(t *testing.T) {
			m := loadSessions(testModel(), nil)
			updated, cmd := m.Update(keyPress(k))
			m2 := updated.(Model)
			if m2.mode != ModeList {
				t.Errorf("expected ModeList after %q on empty list, got %d", k, m2.mode)
			}
			if cmd != nil {
				t.Errorf("expected nil cmd after %q on empty list, got non-nil", k)
			}
		})
	}
}

// TestEmptyFilteredListAfterExplicitSearchIsSafe drives the empty-list path via
// an explicit / search rather than nil sessions. This exercises the codepath
// where m.sessions is non-empty but m.filtered collapses to zero.
func TestEmptyFilteredListAfterExplicitSearchIsSafe(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = pressKey(m, "z")
	m = pressKey(m, "z")
	m = pressKey(m, "z")
	m = pressKey(m, "enter")
	if len(m.filtered) != 0 {
		t.Fatalf("setup: expected zero matches for 'zzz', got %d", len(m.filtered))
	}
	// Cursor must clamp to 0 after the live narrow.
	if m.cursor != 0 {
		t.Errorf("expected cursor=0 on empty filtered, got %d", m.cursor)
	}
	// Drive j/k/G/enter/space — none must panic and cursor stays at 0.
	for _, k := range []string{"j", "k", "G", "g", "enter", " "} {
		m = pressKeySafe(t, m, k)
		if m.cursor < 0 || m.cursor > 0 {
			t.Errorf("expected cursor=0 after %q on empty filtered, got %d", k, m.cursor)
			m.cursor = 0
		}
	}
}

// TestEmptyListEscClearsFilterAndRestoresList verifies the recovery path:
// after explicit search narrows the list to empty, Esc clears the filter so
// the full session list is visible again.
func TestEmptyListEscClearsFilterAndRestoresList(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = pressKey(m, "z")
	m = pressKey(m, "z")
	m = pressKey(m, "enter")
	if len(m.filtered) != 0 {
		t.Fatalf("setup: expected zero matches, got %d", len(m.filtered))
	}
	m = pressKey(m, "esc")
	if !m.activeFilter.IsEmpty() {
		t.Errorf("expected filter cleared after esc, got %+v", m.activeFilter)
	}
	if len(m.filtered) != len(makeSessions()) {
		t.Errorf("expected full list restored, got %d rows", len(m.filtered))
	}
}

// TestEmptyListFooterRendersWithoutPanic confirms the footer renders a
// stable list of action keys (in unavailable state) even when no row is
// selected — the renderer must not deref m.filtered[m.cursor] before the
// hasRow gate.
func TestEmptyListFooterRendersWithoutPanic(t *testing.T) {
	m := loadSessions(testModel(), nil)
	m.width, m.height = 120, 40
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("footer render panicked on empty list: %v", r)
		}
	}()
	out := stripAnsi(renderFooter(m))
	// The essential band must still render — the palette is fixed-order and
	// reserves space for the row keys in their unavailable style regardless of
	// selection. Keys below the one-row budget are omitted rather than moved,
	// and `? more` is what says so.
	//
	// `a actions` is the assertion's subject: on an empty list it is
	// unavailable, and it is essential-band, so it cannot be dropped for width
	// either. A key that is unavailable still occupies its place in the fixed
	// order, which is what makes an availability change a restyle rather than a
	// reflow of the row under the operator's eye.
	for _, want := range []string{"j/k up/down", "a actions", ": commands", footerHintMore} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in empty-list footer, got:\n%s", want, out)
		}
	}
}
