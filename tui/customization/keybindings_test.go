package customization

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestKeybindingsViewIncludesHarnessIconPicker(t *testing.T) {
	view := (keybindingsModel{}).View().Content
	// Content may wrap between any two characters at the fallback width.
	flattened := strings.ReplaceAll(stripAnsi(view), "\n", "")
	for _, want := range []string{
		"Sessions",
		"f5",
		"set tab harness icon (overlay picker)",
	} {
		if !strings.Contains(flattened, want) {
			t.Fatalf("keybinding reference missing %q:\n%s", want, view)
		}
	}
}

func keybindingsKey(key string) tea.KeyPressMsg {
	switch key {
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	case "up":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyUp})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	case "ctrl+c":
		return tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	default:
		return tea.KeyPressMsg(tea.Key{Code: rune(key[0]), Text: key})
	}
}

func updateKeybindings(t *testing.T, m keybindingsModel, msg tea.Msg) (keybindingsModel, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	return updated.(keybindingsModel), cmd
}

func TestKeybindingsNavigationAndClamping(t *testing.T) {
	m := keybindingsModel{width: 42, height: 10}
	if m.maxOffset() <= 0 {
		t.Fatal("test viewport unexpectedly fits the complete keybinding reference")
	}

	m, _ = updateKeybindings(t, m, keybindingsKey("j"))
	if m.offset != 1 {
		t.Fatalf("j offset = %d, want 1", m.offset)
	}
	m, _ = updateKeybindings(t, m, keybindingsKey("down"))
	if m.offset != 2 {
		t.Fatalf("down offset = %d, want 2", m.offset)
	}
	m, _ = updateKeybindings(t, m, keybindingsKey("k"))
	m, _ = updateKeybindings(t, m, keybindingsKey("up"))
	m, _ = updateKeybindings(t, m, keybindingsKey("up"))
	if m.offset != 0 {
		t.Fatalf("up boundary offset = %d, want 0", m.offset)
	}

	m, _ = updateKeybindings(t, m, keybindingsKey("G"))
	maxOffset := m.maxOffset()
	if m.offset != maxOffset {
		t.Fatalf("G offset = %d, want %d", m.offset, maxOffset)
	}
	m, _ = updateKeybindings(t, m, keybindingsKey("j"))
	if m.offset != maxOffset {
		t.Fatalf("j at bottom offset = %d, want %d", m.offset, maxOffset)
	}
	m, _ = updateKeybindings(t, m, keybindingsKey("g"))
	if m.offset != 0 {
		t.Fatalf("g offset = %d, want 0", m.offset)
	}
}

func TestKeybindingsShortViewportIndicatorsAndFinalEntry(t *testing.T) {
	m := keybindingsModel{width: 42, height: 10}
	top := stripAnsi(m.View().Content)
	if strings.Contains(top, "▲") || !strings.Contains(top, "▼") {
		t.Fatalf("top indicators are incorrect:\n%s", top)
	}
	if got := len(strings.Split(strings.TrimSuffix(top, "\n"), "\n")); got != m.height {
		t.Fatalf("rendered lines = %d, want viewport height %d", got, m.height)
	}

	m.offset = m.maxOffset()
	bottom := stripAnsi(m.View().Content)
	if !strings.Contains(bottom, "▲") || strings.Contains(bottom, "▼") {
		t.Fatalf("bottom indicators are incorrect:\n%s", bottom)
	}
	lastRow := stripAnsi(m.bodyRows()[len(m.bodyRows())-1])
	if !strings.Contains(bottom, lastRow) {
		t.Fatalf("bottom view does not include final reference row %q:\n%s", lastRow, bottom)
	}
}

func TestKeybindingsBodyWrapsBeforeCalculatingBounds(t *testing.T) {
	wide := keybindingsModel{width: 100, height: 10}
	narrow := keybindingsModel{width: 24, height: 10}
	if len(narrow.bodyRows()) <= len(wide.bodyRows()) {
		t.Fatalf("narrow body rows = %d, want more than wide rows %d", len(narrow.bodyRows()), len(wide.bodyRows()))
	}
	if narrow.maxOffset() <= wide.maxOffset() {
		t.Fatalf("narrow max offset = %d, want more than wide max offset %d", narrow.maxOffset(), wide.maxOffset())
	}
	for _, row := range narrow.bodyRows() {
		if got := layout.StyledWidth(row); got > narrow.width {
			t.Fatalf("wrapped row width = %d, want <= %d: %q", got, narrow.width, stripAnsi(row))
		}
	}
}

func TestKeybindingsResizeClampsStaleOffset(t *testing.T) {
	m := keybindingsModel{width: 24, height: 8}
	m.offset = m.maxOffset()
	if m.offset == 0 {
		t.Fatal("test viewport unexpectedly has no scroll range")
	}

	m, _ = updateKeybindings(t, m, tea.WindowSizeMsg{Width: 100, Height: 100})
	if m.offset != 0 {
		t.Fatalf("offset after expanding viewport = %d, want 0", m.offset)
	}
}

func TestKeybindingsCloseAndIgnoredKeys(t *testing.T) {
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		t.Run(key, func(t *testing.T) {
			_, cmd := updateKeybindings(t, keybindingsModel{}, keybindingsKey(key))
			if cmd == nil {
				t.Fatalf("%s returned nil command, want tea.Quit", key)
			}
		})
	}

	m := keybindingsModel{width: 42, height: 10, offset: 2}
	updated, cmd := updateKeybindings(t, m, keybindingsKey("x"))
	if cmd != nil {
		t.Fatal("unrelated key returned a command, want it ignored")
	}
	if updated.offset != m.offset {
		t.Fatalf("unrelated key changed offset from %d to %d", m.offset, updated.offset)
	}
}

func TestKeybindingsFooterShowsNavigationAndCloseHints(t *testing.T) {
	view := stripAnsi((keybindingsModel{}).View().Content)
	for _, want := range []string{"j/k scroll", "g/G top/bottom", "q/esc close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("keybinding footer missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "any key") {
		t.Fatalf("keybinding footer retains obsolete any-key hint:\n%s", view)
	}
}
