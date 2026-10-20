package textutil

import (
	"strings"
	"testing"
)

func TestSnippet(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		max      int
		want     string
	}{
		{"empty", "", 10, ""},
		{"short", "hello", 10, "hello"},
		{"whitespace folds", "line1\n\n   line2\tline3\r\nend", 80, "line1 line2 line3 end"},
		{"leading space kept once", "  x", 10, " x"},
		{"exact length", "abcde", 5, "abcde"},
		{"over length", "abcdef", 5, "abcde…"},
		{"runes not bytes", "ééééé", 5, "ééééé"},
		{"runes over", "éééééé", 5, "ééééé…"},
		{"folded spaces do not count", "a    b", 3, "a b"},
		{"trailing run past the cap is not a cut", "abc   ", 4, "abc "},
		{"cut after a space", "abc def", 4, "abc …"},
	} {
		if got := Snippet(tc.in, tc.max); got != tc.want {
			t.Errorf("%s: Snippet(%q, %d) = %q, want %q", tc.name, tc.in, tc.max, got, tc.want)
		}
	}
}

func TestSnippetCapsLongInput(t *testing.T) {
	got := Snippet(strings.Repeat("x", 500), 160)
	if want := strings.Repeat("x", 160) + "…"; got != want {
		t.Errorf("Snippet(500 x) = %q, want 160 x and an ellipsis", got)
	}
}
