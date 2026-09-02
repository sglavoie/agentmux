package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestParseUITestKeyNamedAndPrintable checks that named keys, control chords,
// and printable runes all parse to messages whose String() matches how the
// production model sees the key. String() is the identity used by the model's
// key handling, so asserting on it guarantees behavioral parity.
func TestParseUITestKeyNamedAndPrintable(t *testing.T) {
	cases := []struct {
		spec string
		want string
	}{
		{"enter", "enter"},
		{"esc", "esc"},
		{"tab", "tab"},
		{"backspace", "backspace"},
		{"space", "space"},
		{"up", "up"},
		{"down", "down"},
		{"left", "left"},
		{"right", "right"},
		{"home", "home"},
		{"end", "end"},
		{"pgup", "pgup"},
		{"pgdown", "pgdown"},
		{"ctrl+c", "ctrl+c"},
		{"ctrl+d", "ctrl+d"},
		{"j", "j"},
		{"/", "/"},
		{"?", "?"},
		{"G", "G"},
	}
	for _, c := range cases {
		t.Run(c.spec, func(t *testing.T) {
			msg, err := parseUITestKey(c.spec)
			if err != nil {
				t.Fatalf("parseUITestKey(%q) error: %v", c.spec, err)
			}
			if got := msg.String(); got != c.want {
				t.Errorf("parseUITestKey(%q).String() = %q, want %q", c.spec, got, c.want)
			}
		})
	}
}

// TestParseUITestKeySpaceHasNoText verifies the spacebar is created without
// Text so it reports "space" — matching the existing keyPress helper.
func TestParseUITestKeySpaceHasNoText(t *testing.T) {
	msg, err := parseUITestKey("space")
	if err != nil {
		t.Fatalf("parseUITestKey(space): %v", err)
	}
	if tea.Key(msg).Text != "" {
		t.Errorf("space key Text = %q, want empty", tea.Key(msg).Text)
	}
}

// TestParseUITestKeyCtrlChordCarriesModifier verifies control chords set the
// ctrl modifier on the underlying rune rather than emitting literal text.
func TestParseUITestKeyCtrlChordCarriesModifier(t *testing.T) {
	msg, err := parseUITestKey("ctrl+c")
	if err != nil {
		t.Fatalf("parseUITestKey(ctrl+c): %v", err)
	}
	key := tea.Key(msg)
	if key.Code != 'c' {
		t.Errorf("ctrl+c Code = %q, want 'c'", key.Code)
	}
	if !key.Mod.Contains(tea.ModCtrl) {
		t.Errorf("ctrl+c should carry ModCtrl, got mod %v", key.Mod)
	}
}

// TestParseUITestKeyErrors confirms malformed specs fail closed instead of
// silently producing a surprising key.
func TestParseUITestKeyErrors(t *testing.T) {
	for _, spec := range []string{"", "nope", "ArrowUp", "ab", "ctrl+", "ctrl+ab"} {
		t.Run(spec, func(t *testing.T) {
			if _, err := parseUITestKey(spec); err == nil {
				t.Errorf("parseUITestKey(%q) = nil error, want error", spec)
			}
		})
	}
}
