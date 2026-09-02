package commands

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestWrapTextHardBreakIsRuneSafe pins that hard-breaking a word longer
// than the wrap width never slices mid-rune: every emitted line must be
// valid UTF-8 and joining the lines must reproduce the original word.
// Before the fix, word[:width] cut by byte and garbled multi-byte words.
func TestWrapTextHardBreakIsRuneSafe(t *testing.T) {
	word := "ééééééé" // 7 runes, 14 bytes — forces hard breaks at width 5
	lines := wrapText(word, 5)
	if len(lines) < 2 {
		t.Fatalf("expected the word to hard-break into multiple lines, got %q", lines)
	}
	for _, line := range lines {
		if !utf8.ValidString(line) {
			t.Errorf("wrapText emitted invalid UTF-8 line %q", line)
		}
	}
	if got := strings.Join(lines, ""); got != word {
		t.Errorf("wrapText mangled the word: joined lines %q, want %q", got, word)
	}
}
