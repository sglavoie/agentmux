package commands

import (
	"strings"
	"testing"
)

// The palette hint row is the one piece of footer chrome this package renders
// itself, so its bindings and its zero-width fallback are pinned here rather
// than in the root's cross-mode footer table.
func TestPaletteHintLineZeroWidthIsEmpty(t *testing.T) {
	if got := paletteHintLine(0, false, false); got != "" {
		t.Fatalf("zero-width command-palette hint = %q, want empty fallback", got)
	}
}

func TestPaletteHintLineAccentsKeys(t *testing.T) {
	got := paletteHintLine(80, false, false)
	if want := styleFooterKey.Render("enter"); !strings.Contains(got, want) {
		t.Fatalf("hint row does not accent enter:\n%s", got)
	}
}
