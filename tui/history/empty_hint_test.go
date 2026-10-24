package history

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/layout"
)

// TestEmptyResultsNameTheWayOut pins that an empty list says what to undo:
// esc clears only the text query, so a chip left from an earlier search must
// be named on its own or the operator has no reason to look for it.
func TestEmptyResultsNameTheWayOut(t *testing.T) {
	cases := []struct {
		name string
		in   SearchInput
		want string
	}{
		{"nothing to undo", SearchInput{}, "(no past sessions found — each profile's home_dir in config.toml says where to look)"},
		{"query only", SearchInput{Query: "x"}, "(no sessions match — esc clears the search)"},
		{"chip only", SearchInput{ProjectChip: "api"}, "(no sessions match — h/p/r/m change the filters)"},
		{"both", SearchInput{Query: "x", HarnessChip: "codex"}, "(no sessions match — esc clears the search · h/p/r/m change the filters)"},
		{"load failed", SearchInput{RefreshErr: errors.New("permission denied"), RefreshErrLabel: "Refresh error"}, "(could not load past sessions — esc, then reopen Past Sessions to retry)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.IndexReady = true
			if got := tc.in.RenderList(10); !strings.Contains(got, tc.want) {
				t.Fatalf("RenderList = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// A long error wraps within the width, and the list gives up the rows it takes.
func TestLongErrorsWrapAndAreCountedAgainstTheList(t *testing.T) {
	long := errors.New(strings.Repeat("unreadable transcript directory ", 6))
	in := SearchInput{Width: 40, Height: 30, BodyHeight: 30, IndexReady: true, RefreshErr: long, RefreshErrLabel: "Refresh error"}
	lines := strings.Split(in.Render(), "\n")
	first, list := -1, -1
	for i, line := range lines {
		if w := layout.StyledWidth(line); w > in.Width {
			t.Fatalf("a row is %d cells wide in a %d-cell view: %q", w, in.Width, line)
		}
		if first < 0 && strings.Contains(line, "Refresh error") {
			first = i
		}
		if list < 0 && strings.Contains(line, "could not load past sessions") {
			list = i
		}
	}
	if first < 0 || list < 0 {
		t.Fatalf("the error or the empty list is missing:\n%s", strings.Join(lines, "\n"))
	}
	errorRows := list - first
	if errorRows < 2 {
		t.Fatalf("the error took %d rows; want it wrapped over several", errorRows)
	}
	if got, want := in.errorLines(), errorRows; got != want {
		t.Fatalf("errorLines = %d, but the error drew %d rows", got, want)
	}
}
