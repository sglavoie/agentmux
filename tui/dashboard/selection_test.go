package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// selectionFixtureRows are two rows in the SAME urgency bucket, so the list
// draws one heading and no group borders. That keeps every painted session line
// exactly the row — nothing prefixed outside the selection span — which is what
// lets the assertions below compare a selected line against an unselected one
// character for character.
//
// Both rows carry attention emphasis on purpose: the selection claim is about a
// row whose cells are already styled, and a neutral row would satisfy it by
// having nothing to strip.
func selectionFixtureRows() []session.RunningSession {
	return []session.RunningSession{
		{TabID: 1, WindowID: 1, Base: "api", Account: "work", State: "key"},
		{TabID: 2, WindowID: 2, Base: "web", Account: "work", State: "question"},
	}
}

func selectionFixtureInput(cursor int, blurred bool) ListInput {
	return ListInput{
		Cfg:     &config.Config{},
		Width:   60,
		Now:     time.Now(),
		Items:   BuildItems(selectionFixtureRows()),
		Cursor:  cursor,
		Blurred: blurred,
	}
}

// lineContaining returns the single rendered line carrying token, failing if the
// render has no such line or more than one.
func lineContaining(t *testing.T, rendered, token string) string {
	t.Helper()
	var found string
	n := 0
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(stripAnsi(line), token) {
			found = line
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one line containing %q, got %d:\n%s", token, n, stripAnsi(rendered))
	}
	return found
}

// TestSelectedQueueRowIsOneSelectionSpanOverPlainText pins the restraint half of
// the redesign's selection rule: being selected changes how a row is PAINTED and
// never what it SAYS, and the paint is one unbroken span rather than a reverse
// wrapper with the row's own amber status still lit inside it.
//
// The retired row carried its selection as a leading marker, so this could be
// asserted on the text. It cannot be any more — the text is now identical in
// both states, which is the point — so the assertion is made against
// renderSelectedRow applied to the unselected line. An emphasis sequence that
// survived under the selection would make the two differ.
func TestSelectedQueueRowIsOneSelectionSpanOverPlainText(t *testing.T) {
	selected := lineContaining(t, RenderList(selectionFixtureInput(0, false)), "api")
	unselected := lineContaining(t, RenderList(selectionFixtureInput(1, false)), "api")

	// Without this the test would pass against a row that had no styling to
	// normalize away, which is the one shape the normalization cannot fail on.
	if !strings.Contains(unselected, "\x1b[") {
		t.Fatalf("setup: the unselected attention row carries no styling to strip: %q", unselected)
	}
	if got, want := stripAnsi(selected), stripAnsi(unselected); got != want {
		t.Errorf("selection changed the row's text:\n got %q\nwant %q", got, want)
	}
	if want := renderSelectedRow(unselected, false); selected != want {
		t.Errorf("selected row is not one normalized span:\n got %q\nwant %q", selected, want)
	}
}

// TestBlurredSelectionIsDistinctFromTheFocusedOne is the other half: the row the
// keyboard is driving and the row a second pane left behind must not look the
// same. They still say the same thing — a blurred selection is a weaker claim
// about focus, not a different row — so the difference is asserted in the paint
// and the sameness in the text.
func TestBlurredSelectionIsDistinctFromTheFocusedOne(t *testing.T) {
	focused := lineContaining(t, RenderList(selectionFixtureInput(0, false)), "api")
	blurred := lineContaining(t, RenderList(selectionFixtureInput(0, true)), "api")

	if got, want := stripAnsi(blurred), stripAnsi(focused); got != want {
		t.Errorf("blurring changed the row's text:\n got %q\nwant %q", got, want)
	}
	if blurred == focused {
		t.Errorf("a blurred selection paints exactly like the focused one: %q", focused)
	}
	if !strings.Contains(blurred, "\x1b[") {
		t.Errorf("a blurred selection paints nothing at all: %q", blurred)
	}
}

// TestSelectionMovesWithTheCursorAndLeavesNothingBehind walks the cursor across
// the fixture and asserts the invariant a stale inverse cell would break: at
// every position exactly one session line is selected, and it is the one the
// cursor names.
func TestSelectionMovesWithTheCursorAndLeavesNothingBehind(t *testing.T) {
	names := []string{"api", "web"}
	for cursor, name := range names {
		rendered := RenderList(selectionFixtureInput(cursor, false))
		for i, other := range names {
			line := lineContaining(t, rendered, other)
			plain := lineContaining(t, RenderList(selectionFixtureInput(1-i, false)), other)
			isSelected := line == renderSelectedRow(plain, false)
			if want := i == cursor; isSelected != want {
				t.Errorf("cursor on %q: row %q selected = %v, want %v", name, other, isSelected, want)
			}
		}
	}
}

// headingAlignmentRows returns one row per requested state, optionally turning
// the first of them into an unread completion so the notice gutter is reserved.
//
// Note that unread is what puts that row in Needs you: a stop the operator has
// already seen falls to Idle, so a caller wanting ONE bucket beside the gutter
// has to give the remaining rows an attention state of their own.
func headingAlignmentRows(buckets []string, unread bool) []session.RunningSession {
	out := make([]session.RunningSession, len(buckets))
	for i, state := range buckets {
		out[i] = session.RunningSession{
			TabID:    i + 1,
			WindowID: i + 1,
			Base:     "row" + string(rune('a'+i)),
			Account:  "work",
			State:    state,
		}
	}
	if unread && len(out) > 0 {
		out[0].State = "stop"
		out[0].Unseen = true
	}
	return out
}

// TestUrgencyHeadingStartsOnTheSameCellAsItsRows pins the alignment the heading's
// indent parameter exists for. The heading is drawn above rows that may be
// prefixed by a notice gutter, a group border, both, or neither, and those
// prefixes are added by the row walk rather than by the heading — so an indent
// hard-coded to one of the four combinations would hang the heading to the left
// or the right of the column it labels in the other three.
//
// The claim is made on the rendered text: the cell the heading's first character
// lands on is the cell every row's name lands on.
func TestUrgencyHeadingStartsOnTheSameCellAsItsRows(t *testing.T) {
	for name, tc := range map[string]struct {
		buckets []string
		unread  bool
		// column is the cell the rows — and therefore the heading — must start
		// on. Spelled out per case rather than only compared against itself, so
		// the four combinations are pinned as four DIFFERENT indents and a
		// renderer that stopped drawing a prefix entirely could not satisfy them
		// all by agreeing with itself.
		column int
	}{
		"one bucket, no gutter":    {[]string{"bolt", "bolt"}, false, 0},
		"two buckets, no gutter":   {[]string{"key", "bolt"}, false, groupBorderWidth},
		"one bucket, with gutter":  {[]string{"stop", "key"}, true, noticeColumnWidth},
		"two buckets, with gutter": {[]string{"stop", "bolt"}, true, noticeColumnWidth + groupBorderWidth},
	} {
		t.Run(name, func(t *testing.T) {
			rows := headingAlignmentRows(tc.buckets, tc.unread)
			in := ListInput{Cfg: &config.Config{}, Width: 60, Now: time.Now(), Items: BuildItems(rows)}
			rendered := stripAnsi(RenderList(in))

			headings, names := 0, 0
			for _, line := range strings.Split(rendered, "\n") {
				trimmed := strings.TrimLeft(line, " ")
				if trimmed == "" {
					continue
				}
				at := len(line) - len(trimmed)
				switch {
				case strings.HasPrefix(trimmed, "Needs you (") ||
					strings.HasPrefix(trimmed, "Running (") ||
					strings.HasPrefix(trimmed, "Idle ("):
					headings++
					if want := nameColumnOf(t, rendered); at != want {
						t.Errorf("heading %q starts at cell %d, its rows start at %d", trimmed, at, want)
					}
					if at != tc.column {
						t.Errorf("heading %q starts at cell %d, want %d for this prefix combination", trimmed, at, tc.column)
					}
				case strings.Contains(trimmed, "row"):
					names++
				}
			}
			if headings != len(uniqueBuckets(rows)) || names != len(rows) {
				t.Fatalf("setup: rendered %d headings and %d rows, want %d and %d:\n%s",
					headings, names, len(uniqueBuckets(rows)), len(rows), rendered)
			}
		})
	}
}

// nameColumnOf reports the cell every session row's name begins on, read off the
// render rather than recomputed from the prefix widths the renderer used.
//
// The prefix is measured in CELLS and not in bytes, because the two disagree
// exactly where this test is pointed: a marked row is prefixed by the 3-byte
// 1-cell `•` where an unmarked one is prefixed by two spaces, so a byte index
// would report those two aligned rows as two different columns.
func nameColumnOf(t *testing.T, rendered string) int {
	t.Helper()
	col := -1
	for _, line := range strings.Split(rendered, "\n") {
		i := strings.Index(line, "row")
		if i < 0 {
			continue
		}
		at := layout.Width(line[:i])
		if col == -1 {
			col = at
			continue
		}
		if at != col {
			t.Fatalf("rows do not share a name column: %d and %d:\n%s", col, at, rendered)
		}
	}
	if col == -1 {
		t.Fatalf("no session row in the render:\n%s", rendered)
	}
	return col
}

func uniqueBuckets(rows []session.RunningSession) map[int]struct{} {
	seen := map[int]struct{}{}
	for _, row := range rows {
		seen[StateGroup(row, nil, 10*time.Minute)] = struct{}{}
	}
	return seen
}
