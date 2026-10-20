package sendform

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A draft wraps between words; only a word wider than the row is broken.
func TestEditorRowsWrapBetweenWords(t *testing.T) {
	text := "keep the existing behavior and " + strings.Repeat("x", 30)
	rows := editorRows(text, 21)
	var got []string
	for i, row := range rows {
		if i > 0 && row.start != rows[i-1].end {
			t.Fatalf("rows are not contiguous: %+v", rows)
		}
		got = append(got, text[row.start:row.end])
	}
	want := []string{"keep the existing ", "behavior and ", strings.Repeat("x", 20), strings.Repeat("x", 10)}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

// A screen that cannot be shown is one sentence led by its label, not a
// label alone on a row above a message.
func TestUnreadableScreenLeadsWithItsLabel(t *testing.T) {
	out := ansi.Strip(render(State{Title: "web", ScreenLoaded: true, ScreenErr: "no terminal backend"}, 56, 24))
	if !strings.Contains(out, "Its screen: could not be read: no terminal backend") {
		t.Fatalf("form = %q, want the label leading the sentence", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "Its screen" {
			t.Fatalf("label sits alone on a row:\n%s", out)
		}
	}
}
