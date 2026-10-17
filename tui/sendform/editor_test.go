package sendform

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestEditorNavigatesWrappedDraftWithoutSplittingGraphemes(t *testing.T) {
	s := State{}
	s.Insert(strings.Repeat("one two three\n", 20))
	end := s.Cursor
	s.Edit("pgup", 20)
	if s.Cursor >= end {
		t.Fatal("page up did not reveal earlier text")
	}
	if !strings.Contains(strings.Join(editorBlock(s, 20), "\n"), "↓ more text") {
		t.Fatal("hidden later text not indicated")
	}
	s.Edit("ctrl+home", 20)
	s.Insert("👩‍💻é")
	s.Edit("backspace", 20)
	if !strings.HasPrefix(s.Draft, "👩‍💻one") {
		t.Fatal("backspace split accent")
	}
	s.Edit("backspace", 20)
	if !strings.HasPrefix(s.Draft, "one") {
		t.Fatal("backspace split emoji")
	}
	for _, width := range []int{1, 5, 20, 80} {
		for _, line := range editorBlock(s, width) {
			if ansi.StringWidth(line) > width && !strings.Contains(line, "more text") {
				t.Fatalf("width %d: %q", width, line)
			}
		}
	}
}

func TestVerticalMovementAtSoftWrapBoundary(t *testing.T) {
	s := State{}
	s.Insert("abcdefghij")
	s.Edit("up", 6)
	if s.Cursor >= 5 {
		t.Fatalf("up stayed on second wrapped row: %d", s.Cursor)
	}
	s.Edit("down", 6)
	if s.Cursor < 5 {
		t.Fatalf("down stayed on first wrapped row: %d", s.Cursor)
	}
}
