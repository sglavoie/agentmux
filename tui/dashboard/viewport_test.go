package dashboard

import (
	"fmt"
	"strings"
	"testing"
)

// numbered builds n lines named by their position, so an assertion about the
// window names WHERE the pane is looking rather than what happens to be there.
func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i+1)
	}
	return lines
}

func TestViewportFollowsTheTail(t *testing.T) {
	var v OutputViewport
	if v.Paused() {
		t.Fatalf("the zero viewport reported itself paused; it must follow")
	}
	got := v.Window(numbered(40), 5)
	want := []string{"line 36", "line 37", "line 38", "line 39", "line 40"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("following showed %v, want the last five %v", got, want)
	}

	// A shorter snapshot is shown whole rather than padded here: padding the
	// pane is the renderer's job, and a viewport that padded would make "how
	// many lines does this session have" unanswerable from its output.
	if got := v.Window(numbered(2), 5); len(got) != 2 {
		t.Errorf("content shorter than the pane returned %d lines, want 2", len(got))
	}
	if got := v.Window(nil, 5); got != nil {
		t.Errorf("an empty snapshot returned %v, want no lines", got)
	}
	if got := v.Window(numbered(40), 0); got != nil {
		t.Errorf("a zero-height pane returned %v, want no lines", got)
	}
}

// TestViewportHoldsTheSnapshotItScrolledOff is the whole reason the type
// exists: the text under a scrolled-back pane must not move when the next
// refresh replaces the buffer.
func TestViewportHoldsTheSnapshotItScrolledOff(t *testing.T) {
	first := numbered(40)
	v := OutputViewport{}.ScrollUp(first, 5, 3)
	if !v.Paused() {
		t.Fatalf("scrolling up in overflowing content did not pause the pane")
	}
	want := []string{"line 33", "line 34", "line 35", "line 36", "line 37"}
	if got := v.Window(first, 5); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("held window showed %v, want %v", got, want)
	}

	// A later, longer snapshot arrives. The pane is holding, so it changes
	// nothing at all — not the text and not the position within it.
	later := numbered(80)
	if got := v.Window(later, 5); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("a refresh moved the held window to %v, want it unchanged at %v", got, want)
	}

	// Resume picks the newer snapshot's tail up, which is the only way back.
	v = v.Resume()
	if v.Paused() {
		t.Fatalf("Resume left the pane paused")
	}
	if got := v.Window(later, 2); strings.Join(got, "|") != "line 79|line 80" {
		t.Errorf("after Resume the pane showed %v, want the newest snapshot's tail", got)
	}
}

// TestViewportHoldIsIndependentOfTheCallersSlice pins the defensive copy. The
// caller builds its slice from a string it replaces every refresh interval, and
// a held window aliasing it would quietly stop being held.
func TestViewportHoldIsIndependentOfTheCallersSlice(t *testing.T) {
	live := numbered(10)
	v := OutputViewport{}.ScrollUp(live, 3, 2)
	before := strings.Join(v.Window(nil, 3), "|")
	for i := range live {
		live[i] = "clobbered"
	}
	if after := strings.Join(v.Window(nil, 3), "|"); after != before {
		t.Errorf("mutating the caller's slice changed the held window from %q to %q", before, after)
	}
}

func TestViewportScrollingClampsToTheSnapshot(t *testing.T) {
	lines := numbered(10)
	v := OutputViewport{}.ScrollUp(lines, 4, 100)
	if got := strings.Join(v.Window(lines, 4), "|"); got != "line 01|line 02|line 03|line 04" {
		t.Errorf("an oversized scroll up landed at %q, want the first four lines", got)
	}
	v = v.ScrollDown(4, 100)
	if got := strings.Join(v.Window(lines, 4), "|"); got != "line 07|line 08|line 09|line 10" {
		t.Errorf("an oversized scroll down landed at %q, want the last four lines", got)
	}
	// Reaching the bottom of a held snapshot is not resuming: the bottom of a
	// snapshot taken a minute ago is not the newest line.
	if !v.Paused() {
		t.Errorf("scrolling to the bottom of the held snapshot silently resumed following")
	}
}

// TestViewportDeclinesToPauseContentThatFits guards the trade nobody asked for:
// pausing a pane that has nothing above its first line would stop it following
// live output in exchange for a scroll position it never left.
func TestViewportDeclinesToPauseContentThatFits(t *testing.T) {
	fits := numbered(3)
	if v := (OutputViewport{}).ScrollUp(fits, 5, 1); v.Paused() {
		t.Errorf("ScrollUp paused a pane whose content already fits whole")
	}
	if v := (OutputViewport{}).Top(fits, 5); v.Paused() {
		t.Errorf("Top paused a pane whose first line is already on screen")
	}
	if v := (OutputViewport{}).ScrollUp(fits, 0, 1); v.Paused() {
		t.Errorf("ScrollUp paused a zero-height pane")
	}
	// ScrollDown while following is a no-op: following already shows the tail.
	if v := (OutputViewport{}).ScrollDown(5, 1); v.Paused() {
		t.Errorf("ScrollDown paused a following pane")
	}
}

func TestViewportTopHoldsAtTheFirstLine(t *testing.T) {
	lines := numbered(40)
	v := OutputViewport{}.Top(lines, 3)
	if !v.Paused() {
		t.Fatalf("Top did not pause overflowing content")
	}
	if got := strings.Join(v.Window(lines, 3), "|"); got != "line 01|line 02|line 03" {
		t.Errorf("Top showed %q, want the first three lines", got)
	}
	// Top on an already-held pane moves to line one without recapturing.
	v = v.ScrollDown(3, 10).Top(numbered(80), 3)
	if got := strings.Join(v.Window(nil, 3), "|"); got != "line 01|line 02|line 03" {
		t.Errorf("Top on a held pane showed %q, want the held snapshot's first three lines", got)
	}
}

func TestSplitOutputCountsLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		// An empty capture is zero lines, not the one empty line strings.Split
		// reports. This is what lets a legitimately empty buffer be told apart
		// from a buffer holding a blank line.
		{"empty", "", nil},
		{"only newlines", "\n\n", nil},
		{"trailing newline is chrome", "a\nb\n", []string{"a", "b"}},
		{"interior blank line survives", "a\n\nb", []string{"a", "", "b"}},
		// A stray \r inside a rendered row is a cursor movement the terminal
		// would obey, redrawing the line on top of itself.
		{"carriage returns dropped", "a\r\nb\rc", []string{"a", "bc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitOutput(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("SplitOutput(%q) = %q (%d lines), want %q (%d)", tc.in, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("SplitOutput(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}
