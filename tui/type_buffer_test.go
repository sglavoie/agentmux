package tui

import (
	"strings"
	"testing"
)

// ---- Dashboard filter entry tests ----

func TestUnboundPrintableDoesNotStartDashboardFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	full := len(m.filtered)

	m = pressKey(m, "z")

	if m.mode != ModeList {
		t.Fatalf("expected to stay in ModeList, got %d", m.mode)
	}
	if !m.activeFilter.IsEmpty() {
		t.Fatalf("expected no active filter from bare printable key, got %+v", m.activeFilter)
	}
	if got := len(m.filtered); got != full {
		t.Fatalf("expected list to stay at %d rows, got %d", full, got)
	}
	if m.filterText != "" {
		t.Fatalf("expected search input to stay empty, got %q", m.filterText)
	}
}

func TestSlashEntersDashboardSearch(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())

	m = pressKey(m, "/")

	if m.mode != ModeSearch {
		t.Fatalf("expected ModeSearch after /, got %d", m.mode)
	}
	if m.filterText != "" {
		t.Fatalf("expected empty search input on entry, got %q", m.filterText)
	}
}

func TestDashboardSearchNarrowsOnlyAfterSlash(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = typeString(m, "zz")
	if !m.activeFilter.IsEmpty() {
		t.Fatalf("typing before / must not filter, got %+v", m.activeFilter)
	}

	m = pressKey(m, "/")
	m = typeString(m, "bac")

	if m.mode != ModeSearch {
		t.Fatalf("expected to remain in ModeSearch while typing, got %d", m.mode)
	}
	if m.filterText != "bac" {
		t.Fatalf("expected filterText=bac, got %q", m.filterText)
	}
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 filtered row, got %d (filter=%+v)", len(m.filtered), m.activeFilter)
	}
	if !strings.Contains(m.filtered[0].Base, "back") {
		t.Errorf("expected filtered row to contain back, got %q", m.filtered[0].Base)
	}
}

// A row action is still one keystroke away from a list that is not filtering:
// `a` opens the overlay against the cursor row, and the mnemonic inside it is
// the letter the dashboard used to bind. The point this file makes is that the
// list does not swallow those letters into a type-ahead buffer — the dashboard
// only starts filtering after `/`.
func TestRowActionStillWorksWithoutSearchMode(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.cfg.Defaults.ConfirmDestructive = true

	m = pressRowAction(t, m, keyR)

	if m.mode != ModeConfirm {
		t.Fatalf("expected ModeConfirm after restart, got %d", m.mode)
	}
	if m.confirmAction != "restart" {
		t.Errorf("expected restart confirm, got %q", m.confirmAction)
	}
}

func TestEnterCommitsExplicitDashboardSearch(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	m = typeString(m, "bac")

	updated, cmd := m.Update(keyPress("enter"))
	m2 := updated.(Model)

	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", m2.mode)
	}
	if m2.activeFilter.IsEmpty() {
		t.Errorf("expected filter to remain applied after commit, got empty")
	}
	if cmd != nil {
		t.Errorf("expected no cmd on search commit, got non-nil")
	}
}

func TestBackspaceDoesNotModifyFilterBeforeSlash(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	full := len(m.filtered)

	m = pressKey(m, "backspace")

	if !m.activeFilter.IsEmpty() {
		t.Fatalf("expected backspace outside search to leave filter empty, got %+v", m.activeFilter)
	}
	if got := len(m.filtered); got != full {
		t.Fatalf("expected backspace outside search to leave %d rows, got %d", full, got)
	}
}

func TestQQuitsFromListWithoutSearchMode(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	_, cmd := m.Update(keyPress("q"))
	if cmd == nil {
		t.Error("expected q to dispatch a quit cmd")
	}
}
