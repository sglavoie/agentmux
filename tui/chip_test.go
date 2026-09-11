package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// ---- Render: chip line ----

func chipTestModel() Model {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
	}
	m := Model{cfg: cfg, width: 120}
	m.sessions = []session.RunningSession{
		{Base: "x", State: "bolt", Account: "work"},
	}
	return m
}

func TestRenderChipsEmptyCollapses(t *testing.T) {
	m := chipTestModel()
	if got := renderChips(m, m.width); got != "" {
		t.Errorf("expected empty chip line when no filter, got %q", got)
	}
}

func TestRenderChipsOne(t *testing.T) {
	m := chipTestModel()
	m.activeFilter = dashboard.Parse("api")
	out := stripAnsi(renderChips(m, m.width))
	if !strings.Contains(out, "▌/api ✕") {
		t.Errorf("expected substring chip body, got %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected single trailing newline, got %q", out)
	}
}

func TestRenderChipsAllKindsOnOneLine(t *testing.T) {
	m := chipTestModel()
	m.activeFilter = dashboard.Parse("api @work !key")
	out := stripAnsi(renderChips(m, m.width))
	for _, want := range []string{"▌/api ✕", "▌@work ✕", "▌!key ✕"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in chip line, got %q", want, out)
		}
	}
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
		t.Errorf("expected chips on a single visual line at width=120, got %q", out)
	}
}

func TestRenderChipsWrapAtNarrowWidth(t *testing.T) {
	m := chipTestModel()
	m.activeFilter = dashboard.Parse("aaaa bbbb cccc dddd")
	// Each chip is ~9 cells (▌/aaaa ✕). Set width tight enough to force wrap.
	out := stripAnsi(renderChips(m, 25))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected chips to wrap onto multiple lines at width=25, got:\n%s", out)
	}
}

// TestRenderChipsHaveNoFocusedSpelling is the render half of the ring's
// retirement. A chip used to draw `[✕]` and an underline when it was the focused
// one; with no focus to have, every chip in the row renders identically, and no
// keystroke can make one of them look selected.
func TestRenderChipsHaveNoFocusedSpelling(t *testing.T) {
	m := chipTestModel()
	m.activeFilter = dashboard.Parse("api @work !key")
	out := stripAnsi(renderChips(m, m.width))
	if strings.Contains(out, "[✕]") {
		t.Errorf("expected no bracketed remove marker anywhere in the chip row, got %q", out)
	}
	if n := strings.Count(out, "✕"); n != 3 {
		t.Errorf("expected one plain ✕ per chip, got %d in %q", n, out)
	}
}

// ---- Retired: the numeric and chip-focus vocabulary ----

// TestRetiredChipKeysDoNotDispatch presses every key the chip ring used to own
// and asserts the filter is untouched. These are unbound now rather than
// rebound, so the evidence that they are gone is that a keypress changes nothing
// — the dashboard binding table reports an unknown key as unclaimed and the
// dispatcher drops it.
func TestRetiredChipKeysDoNotDispatch(t *testing.T) {
	retired := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "backspace", "x", "X"}
	for _, key := range retired {
		t.Run(key, func(t *testing.T) {
			m := loadSessions(testModel(), makeSessions())
			m.activeFilter = dashboard.Parse("api @work !key")
			before := m.activeFilter

			m = pressKey(m, key)

			if m.mode != ModeList {
				t.Fatalf("%q left ModeList for mode=%d", key, m.mode)
			}
			if m.activeFilter.Raw != before.Raw || m.activeFilter.Account != before.Account ||
				m.activeFilter.State != before.State || len(m.activeFilter.Substrings) != len(before.Substrings) {
				t.Errorf("%q changed the filter: %+v -> %+v", key, before, m.activeFilter)
			}
		})
	}
}

// TestTabWithChipsSwitchesNothingWithoutAnInspector is the other half: tab kept
// its key but lost this meaning. At a width with no inspector pane on screen
// there is no second pane to hand the keyboard to, so the key answers nothing
// rather than falling back to the ring it used to cycle.
func TestTabWithChipsSwitchesNothingWithoutAnInspector(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 60, 24
	m.activeFilter = dashboard.Parse("api @work !key")
	if listInspectorShown(m) {
		t.Fatalf("setup: expected no inspector at width=%d", m.width)
	}
	before := m.activeFilter

	for _, key := range []string{"tab", "shift+tab", "tab"} {
		m = pressKey(m, key)
		if m.paneFocus != focusQueue {
			t.Fatalf("%q moved focus off the queue with no second pane on screen", key)
		}
		if m.activeFilter.Raw != before.Raw {
			t.Fatalf("%q changed the filter: %+v", key, m.activeFilter)
		}
	}
}

// ---- Retained: individual filter editing and removal ----

// TestSlashPrefillsTheActiveFilterForEditing is the keyboard route that replaced
// the ring. Entering the filter editor must carry the filter already on screen
// into the buffer, because "remove one chip" is now "backspace one component".
func TestSlashPrefillsTheActiveFilterForEditing(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = dashboard.Parse("api @work")
	raw := m.activeFilter.Raw

	m = pressKey(m, keySlash)
	if m.mode != ModeSearch {
		t.Fatalf("expected ModeSearch after %q, got mode=%d", keySlash, m.mode)
	}
	if m.filterText != raw {
		t.Fatalf("filter buffer = %q, want the active filter %q", m.filterText, raw)
	}

	// Backspacing the trailing component off the buffer removes exactly that one
	// chip and leaves the rest of the expression standing.
	for range "@work" {
		m = pressKey(m, "backspace")
	}
	m = pressKey(m, "enter")
	if m.activeFilter.Account != "" {
		t.Errorf("expected @work removed, got account %q", m.activeFilter.Account)
	}
	if len(m.activeFilter.Substrings) != 1 || m.activeFilter.Substrings[0] != "api" {
		t.Errorf("expected the remaining component preserved, got %v", m.activeFilter.Substrings)
	}
}

// TestEscRestoresTheFilterTheEditorWasEnteredFrom guards the cost of prefilling:
// `/` now applies the operator's own filter live on entry, so aborting must put
// back what was there rather than leaving the buffer's parse behind.
func TestEscRestoresTheFilterTheEditorWasEnteredFrom(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = dashboard.Parse("api @work")

	m = pressKey(m, keySlash)
	m = pressKey(m, "backspace")

	// A partial component reopens the completion menu, and esc closes that
	// first — the editor's own ladder, unchanged by the prefill. The rung under
	// it is the one this test is about.
	if m.completion.Visible {
		m = pressKey(m, keyEsc)
		if m.mode != ModeSearch {
			t.Fatalf("esc closed the editor rather than the completion menu; mode=%d", m.mode)
		}
	}
	m = pressKey(m, keyEsc)

	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc, got mode=%d", m.mode)
	}
	if m.activeFilter.Account != "work" || len(m.activeFilter.Substrings) != 1 {
		t.Errorf("esc left filter %+v, want the pre-entry api @work", m.activeFilter)
	}
}

// Clicking a chip removes that one chip; that path is asserted end to end
// through the rendered hit map in TestMouseProjectSwitcherHeaderChipAndScopeRow.

func TestEnterFocusesTheRowWithChipsActive(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = dashboard.Parse("api")
	updated, cmd := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil commands.Focus after enter")
	}
}

func TestEscClearsChips(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = dashboard.Parse("api @work")
	m = pressKey(m, keyEsc)
	if !m.activeFilter.IsEmpty() {
		t.Errorf("expected filter cleared, got %+v", m.activeFilter)
	}
}

// ---- Footer hint ----

// TestFooterOmitsRetiredChipHints states that the footer stopped advertising the
// ring. Both hints named keys that no longer do anything, and a footer that
// names a dead key is worse than one that says nothing.
func TestFooterOmitsRetiredChipHints(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = dashboard.Parse("api @work")
	footer := stripAnsi(renderFooter(m))
	for _, gone := range []string{"remove chip", "focus next", "1-2", "jump chord", "jump + focus"} {
		if strings.Contains(footer, gone) {
			t.Errorf("footer still advertises %q:\n%s", gone, footer)
		}
	}
	if !strings.Contains(footer, "esc clear filters") {
		t.Errorf("expected the surviving filter hint, got:\n%s", footer)
	}
}
