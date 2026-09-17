package commands

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/layout"
)

// menuModel is the smallest host model the menu can be projected onto. The menu
// is generic over it precisely so this file does not need a Bubble Tea model to
// test what the widget does.
type menuModel struct {
	idle bool
}

func testAction(id ID, band, label, key string) Action[menuModel] {
	return Action[menuModel]{
		ID:          id,
		Band:        band,
		KeyDisplay:  key,
		KeyMatch:    key,
		Label:       label,
		Description: "What " + label + " does when it runs.",
	}
}

func testMenu(width, height int) ActionsInput[menuModel] {
	return ActionsInput[menuModel]{
		Actions: []Action[menuModel]{
			testAction("focus", "Session", "Open session", MenuExecuteKey),
			testAction("peek", "Session", "Peek scrollback", "p"),
			testAction("shell", "Session", "Open shell", "d"),
			testAction("clear", "State", "Clear status", "x"),
			testAction("lock", "State", "Lock/Unlock", "l"),
			testAction("restart", "Lifecycle", "Restart", "r"),
			testAction("close", "Lifecycle", "Close", "X"),
		},
		Title:      "alpha",
		Context:    "codex · claude · main · ~/projects/alpha",
		Width:      width,
		BodyHeight: height,
	}
}

// TestActionImmediateKeyHidesTheExecuteKey is the Enter/mnemonic collision the
// selectable menu introduces: the entry keeps its declared mnemonic, but the
// menu must not print ⏎ against one row when enter runs whatever is selected.
func TestActionImmediateKeyHidesTheExecuteKey(t *testing.T) {
	enter := Action[menuModel]{KeyDisplay: "⏎", KeyMatch: MenuExecuteKey}
	if got := enter.ImmediateKey(); got != "" {
		t.Errorf("ImmediateKey() = %q for the execute key, want it hidden", got)
	}
	ordinary := Action[menuModel]{KeyDisplay: "p", KeyMatch: "p"}
	if got := ordinary.ImmediateKey(); got != "p" {
		t.Errorf("ImmediateKey() = %q, want %q", got, "p")
	}
}

// TestActionsRowsOmitTheRuleAboveTheFirstBand pins the row the menu buys its
// 80x24 fit with: a rule separates, and there is nothing above the first entry
// to separate it from.
func TestActionsRowsOmitTheRuleAboveTheFirstBand(t *testing.T) {
	rows := testMenu(120, 40).Rows(60)
	if len(rows) == 0 {
		t.Fatal("expected rows")
	}
	if rows[0].BandRule {
		t.Errorf("first row is a band rule: %q", rows[0].Text)
	}
	var rules int
	for _, row := range rows {
		if row.BandRule {
			rules++
		}
	}
	// Three bands, two boundaries.
	if rules != 2 {
		t.Errorf("drew %d band rules, want 2 for three bands", rules)
	}
	if len(rows) != 7+2 {
		t.Errorf("drew %d rows, want 7 entries plus 2 rules", len(rows))
	}
}

// TestActionsRowsSpanExactContentWidth keeps every row — entry and rule alike —
// inside the column budget, which is what stops the box from clipping them.
func TestActionsRowsSpanExactContentWidth(t *testing.T) {
	for _, width := range []int{20, 40, 68} {
		for _, row := range testMenu(120, 40).Rows(width) {
			if got := layout.Width(row.Text); got != width {
				t.Errorf("row %q spans %d cells at width %d", row.Text, got, width)
			}
		}
	}
}

// TestActionsCursorMarksExactlyOneRow guards against the marker following a
// duplicate: it is placed from the cursor's position, not by comparing IDs.
func TestActionsCursorMarksExactlyOneRow(t *testing.T) {
	in := testMenu(120, 40)
	in.Cursor = 4
	var marked []string
	for _, row := range in.Rows(60) {
		if !row.BandRule && strings.HasPrefix(row.Text, actionsSelectedMarker) {
			marked = append(marked, row.Text)
		}
	}
	if len(marked) != 1 {
		t.Fatalf("marked %d rows, want exactly 1: %q", len(marked), marked)
	}
	if !strings.Contains(marked[0], "Lock/Unlock") {
		t.Errorf("marked row is %q, want the entry at cursor 4", marked[0])
	}
}

// TestActionsMoveCursorDoesNotWrap pins the deliberate absence of wrapping: an
// operator holding j at the bottom should stay on the last entry rather than
// land back on the first and run it.
func TestActionsMoveCursorDoesNotWrap(t *testing.T) {
	in := testMenu(120, 40)
	in.Cursor = len(in.Actions) - 1
	if got := in.MoveCursor(1); got != len(in.Actions)-1 {
		t.Errorf("MoveCursor(1) at the bottom = %d, want %d", got, len(in.Actions)-1)
	}
	in.Cursor = 0
	if got := in.MoveCursor(-1); got != 0 {
		t.Errorf("MoveCursor(-1) at the top = %d, want 0", got)
	}
	if got := in.MoveCursor(100); got != len(in.Actions)-1 {
		t.Errorf("MoveCursor(100) = %d, want the last entry", got)
	}
}

// TestActionsEmptyMenuSelectsNothing keeps the degenerate case from reporting a
// selection that does not exist.
func TestActionsEmptyMenuSelectsNothing(t *testing.T) {
	in := ActionsInput[menuModel]{Width: 80, BodyHeight: 20}
	if _, ok := in.SelectedAction(); ok {
		t.Error("SelectedAction reported a selection on an empty menu")
	}
	if got := in.SelectedID(); got != "" {
		t.Errorf("SelectedID() = %q on an empty menu, want empty", got)
	}
	if got := in.MoveCursor(3); got != 0 {
		t.Errorf("MoveCursor on an empty menu = %d, want 0", got)
	}
	if !strings.Contains(RenderActions(in), "No actions are available") {
		t.Error("an empty menu does not say so")
	}
}

// TestActionsDetailHeightIsIndependentOfSelection is the no-jitter rule: the
// explanation block must be the same height under every entry, so moving the
// cursor never moves the rows under it.
func TestActionsDetailHeightIsIndependentOfSelection(t *testing.T) {
	base := testMenu(100, 24)
	base.Actions[2].Description = strings.Repeat("a much longer explanation ", 6)
	want := -1
	for i := range base.Actions {
		in := base
		in.Cursor = i
		got := strings.Count(RenderActions(in), "\n")
		if want < 0 {
			want = got
		}
		if got != want {
			t.Fatalf("menu is %d rows with entry %d selected, %d with entry 0", got+1, i, want+1)
		}
	}
}

// TestActionsDetailHeightIsIndependentOfAvailability is the other half: a
// verdict that flips while the menu sits open (an idle threshold elapsing) must
// not resize the block either.
func TestActionsDetailHeightIsIndependentOfAvailability(t *testing.T) {
	in := testMenu(100, 24)
	for i := range in.Actions {
		in.Actions[i].Availability = func(m menuModel) Availability {
			if m.idle {
				return Unavailable("the session has been idle past the configured threshold")
			}
			return Available()
		}
	}
	available := strings.Count(RenderActions(in), "\n")
	in.Model = menuModel{idle: true}
	unavailable := strings.Count(RenderActions(in), "\n")
	if available != unavailable {
		t.Errorf("menu is %d rows when available and %d when not", available+1, unavailable+1)
	}
}

// TestActionsReasonPrecedesDescription is the clipped-reason review focus: a
// detail block short enough to lose its tail must lose the description, not the
// explanation of why the selected entry cannot run.
func TestActionsReasonPrecedesDescription(t *testing.T) {
	in := testMenu(100, 24)
	in.Actions[0].Availability = func(menuModel) Availability {
		return Unavailable("this session is already focused")
	}
	out := RenderActions(in)
	reason := strings.Index(out, "Unavailable: this session is already focused")
	if reason < 0 {
		t.Fatalf("menu does not state the reason:\n%s", out)
	}
	description := strings.Index(out, "What Open session does")
	if description >= 0 && description < reason {
		t.Error("the description is drawn above the unavailability reason")
	}
}

// TestActionsUnavailableEntryStaysSelectable is the requirement that a disabled
// entry explains itself rather than vanishing: it is still a row, it can still
// be reached, and the menu still says why it will not run.
func TestActionsUnavailableEntryStaysSelectable(t *testing.T) {
	in := testMenu(100, 24)
	in.Actions[3].Availability = func(menuModel) Availability {
		return Unavailable("this row carries no status icon")
	}
	in.Cursor = 3
	action, ok := in.SelectedAction()
	if !ok || action.ID != "clear" {
		t.Fatalf("SelectedAction() = %v, %v; want the unavailable entry", action.ID, ok)
	}
	if action.Available(in.Model) {
		t.Error("the unavailable entry reports itself available")
	}
	out := RenderActions(in)
	if !strings.Contains(out, "Clear status") {
		t.Error("the unavailable entry is not drawn")
	}
	if !strings.Contains(out, "this row carries no status icon") {
		t.Error("the unavailable entry does not explain itself")
	}
}

// TestActionsScrollToCursorKeepsTheSelectionVisible is the disappearing-selection
// review focus. Whatever the viewport, the offset the widget hands back must
// leave the cursor's row inside the window.
func TestActionsScrollToCursorKeepsTheSelectionVisible(t *testing.T) {
	for _, height := range []int{6, 8, 12, 24, 45} {
		in := testMenu(100, height)
		for cursor := 0; cursor < len(in.Actions); cursor++ {
			in.Cursor = cursor
			in.Offset = 0
			in.Offset = in.ScrollToCursor()
			rows, visible := in.ScrollBounds()
			if visible <= 0 {
				continue
			}
			index := -1
			for i, row := range rows {
				if row.ActionIndex == cursor {
					index = i
					break
				}
			}
			if index < in.Offset || index >= in.Offset+visible {
				t.Errorf("height %d cursor %d: row %d outside window [%d,%d)", height, cursor, index, in.Offset, in.Offset+visible)
			}
		}
	}
}

// TestActionsRenderNeverExceedsTheBox is the positive layout check the menu is
// required to pass at every target size, replacing the assertion that it
// overflowed at the narrowest one.
func TestActionsRenderNeverExceedsTheBox(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {100, 24}, {120, 30}, {160, 45}, {40, 12}, {20, 6}}
	for _, size := range sizes {
		in := testMenu(size.w, size.h)
		for cursor := 0; cursor < len(in.Actions); cursor++ {
			in.Cursor = cursor
			in.Offset = in.ScrollToCursor()
			content := in.ContentWidth()
			for _, line := range strings.Split(RenderActions(in), "\n") {
				if got := layout.StyledWidth(line); got > content {
					t.Errorf("%dx%d cursor %d: line spans %d cells, content width is %d: %q",
						size.w, size.h, cursor, got, content, line)
				}
			}
			if got := layout.StyledWidth(strings.Split(RenderActionsBox(in), "\n")[0]); got > size.w {
				t.Errorf("%dx%d: box spans %d cells", size.w, size.h, got)
			}
		}
	}
}

// TestActionsChromeShedsBeforeTheListDoes pins the shed order at the height
// where it matters: a viewport too short for everything gives up decoration
// before it gives up the entries, and the header — the only row naming the
// session — goes last.
func TestActionsChromeShedsBeforeTheListDoes(t *testing.T) {
	in := testMenu(100, 8)
	_, visible := in.ScrollBounds()
	if visible < 1 {
		t.Fatalf("a short viewport shows %d rows, want at least one entry", visible)
	}
	if !strings.Contains(RenderActions(in), "Actions for") {
		t.Error("a short viewport dropped the header while other chrome remained")
	}
}
