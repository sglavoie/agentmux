package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// ParseUITestKey converts a scenario key spec into a Bubble Tea key-press
// message. It mirrors the key-code choices used by the in-package test helper
// (see keyPress in model_test.go) so harness-driven key events behave exactly
// like production key handling once fed to Model.Update.
//
// Accepted specs:
//   - named keys: enter, esc, tab, backspace, space, up, down, left, right,
//     home, end, pgup, pgdown
//   - control chords: ctrl+<printable>, e.g. ctrl+c, ctrl+d
//   - any single printable rune, e.g. "j", "/", "?"
func ParseUITestKey(spec string) (tea.KeyPressMsg, error) {
	if spec == "" {
		return tea.KeyPressMsg{}, fmt.Errorf("uitest: empty key spec")
	}

	if named, ok := namedUITestKey(spec); ok {
		return named, nil
	}

	if rest, ok := strings.CutPrefix(spec, "ctrl+"); ok {
		r, err := singleUITestRune(rest)
		if err != nil {
			return tea.KeyPressMsg{}, fmt.Errorf("uitest: invalid ctrl chord %q: %w", spec, err)
		}
		return tea.KeyPressMsg(tea.Key{Code: r, Mod: tea.ModCtrl}), nil
	}

	r, err := singleUITestRune(spec)
	if err != nil {
		return tea.KeyPressMsg{}, fmt.Errorf("uitest: unknown key %q: %w", spec, err)
	}
	return tea.KeyPressMsg(tea.Key{Code: r, Text: spec}), nil
}

// namedUITestKey resolves the fixed set of named keys the harness supports.
// Space is intentionally created without Text so its String() reports "space",
// matching how production key handling sees the spacebar.
func namedUITestKey(spec string) (tea.KeyPressMsg, bool) {
	switch spec {
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}), true
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}), true
	case "tab":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}), true
	case "backspace":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}), true
	case "space":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeySpace}), true
	case "up":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}), true
	case "down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}), true
	case "left":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}), true
	case "right":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}), true
	case "home":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}), true
	case "end":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}), true
	case "pgup":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}), true
	case "pgdown":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}), true
	default:
		return tea.KeyPressMsg{}, false
	}
}

// singleUITestRune returns the sole rune of s, erroring when s is empty, holds
// invalid UTF-8, or carries more than one rune.
func singleUITestRune(s string) (rune, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return 0, fmt.Errorf("invalid UTF-8")
	}
	if size != len(s) {
		return 0, fmt.Errorf("expected a single character, got %q", s)
	}
	return r, nil
}
