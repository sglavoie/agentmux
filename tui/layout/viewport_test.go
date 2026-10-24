package layout

import (
	"strings"
	"testing"
)

func TestQueryLineKeepsTheEndOfALongQuery(t *testing.T) {
	line := QueryLine("/", "a-very-long-query-ending-here█", 16)
	if got := StyledWidth(line); got != 16 {
		t.Fatalf("width = %d, want 16: %q", got, line)
	}
	if !strings.HasPrefix(line, "/…") || !strings.HasSuffix(line, "ending-here█") {
		t.Fatalf("line = %q, want the prompt, an ellipsis, and the end of the query", line)
	}
	if got := QueryLine("/", "short", 16); got != "/short" {
		t.Fatalf("short query = %q, want it whole", got)
	}
	if got := QueryLine("/", "anything", 0); got != "/anything" {
		t.Fatalf("no width yet = %q, want it whole", got)
	}
}

// TestListWindowForFillsTheBodyAfterTheListShrank pins the shrunken-list fix:
// an offset left past the end by a filter or a refresh draws the window that
// ends on the last row, rather than hiding rows above while the body has room.
func TestListWindowForFillsTheBodyAfterTheListShrank(t *testing.T) {
	cases := []struct {
		total, offset, height int
		want                  ListWindow
	}{
		{total: 2, offset: 13, height: 10, want: ListWindow{Start: 0, End: 2}},
		{total: 14, offset: 13, height: 10, want: ListWindow{Start: 5, End: 14, ShowTop: true}},
		{total: 14, offset: 5, height: 10, want: ListWindow{Start: 5, End: 14, ShowTop: true}},
		{total: 14, offset: 3, height: 10, want: ListWindow{Start: 3, End: 11, ShowTop: true, ShowBottom: true}},
		{total: 5, offset: 9, height: 1, want: ListWindow{Start: 4, End: 5}},
	}
	for _, tc := range cases {
		if got := ListWindowFor(tc.total, tc.offset, tc.height); got != tc.want {
			t.Errorf("ListWindowFor(%d, %d, %d) = %+v, want %+v", tc.total, tc.offset, tc.height, got, tc.want)
		}
		max := MaxListOffset(tc.total, tc.height)
		if w := ListWindowFor(tc.total, max, tc.height); w.End != tc.total {
			t.Errorf("MaxListOffset(%d, %d) = %d does not reach the last row: %+v", tc.total, tc.height, max, w)
		}
	}
}
