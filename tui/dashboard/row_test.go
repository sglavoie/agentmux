package dashboard

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// TestFormatRowLockIconPresentWhenLocked verifies that a locked session
// renders the 🔒 glyph in the new lock column and an unlocked session does
// not.
func TestFormatRowLockIconPresentWhenLocked(t *testing.T) {
	s := session.RunningSession{
		WindowID:  1,
		Account:   "work",
		Project:   "api",
		Title:     "api",
		Base:      "api",
		Directory: "/tmp",
	}
	acctColor := map[string]string{"work": "blue"}

	unlocked := stripAnsi(FormatRow(RowInput{Session: s, Band: BandSpacious, Width: 120, AcctColor: acctColor}))
	if strings.Contains(unlocked, palette.LockIcon()) {
		t.Fatalf("unlocked row should not contain %q; got %q", palette.LockIcon(), unlocked)
	}

	s.Locked = true
	locked := stripAnsi(FormatRow(RowInput{Session: s, Band: BandSpacious, Width: 120, AcctColor: acctColor}))
	if !strings.Contains(locked, palette.LockIcon()) {
		t.Fatalf("locked row should contain %q; got %q", palette.LockIcon(), locked)
	}
}

// TestFormatRowMuteIconPresentWhenMuted verifies that a muted session renders
// the 🔇 glyph in the mute column and an unmuted session does not.
func TestFormatRowMuteIconPresentWhenMuted(t *testing.T) {
	s := session.RunningSession{
		WindowID:  1,
		Account:   "work",
		Project:   "api",
		Title:     "api",
		Base:      "api",
		Directory: "/tmp",
	}
	acctColor := map[string]string{"work": "blue"}

	unmuted := stripAnsi(FormatRow(RowInput{Session: s, Band: BandSpacious, Width: 120, AcctColor: acctColor}))
	if strings.Contains(unmuted, palette.MuteIcon()) {
		t.Fatalf("unmuted row should not contain %q; got %q", palette.MuteIcon(), unmuted)
	}

	s.Muted = true
	muted := stripAnsi(FormatRow(RowInput{Session: s, Band: BandSpacious, Width: 120, AcctColor: acctColor}))
	if !strings.Contains(muted, palette.MuteIcon()) {
		t.Fatalf("muted row should contain %q; got %q", palette.MuteIcon(), muted)
	}
}

// TestFormatRowWidthArithmeticMatchesTheComposedRow is the audit the prefix
// enumeration in FormatRow cannot perform on itself. That expression is a
// hand-maintained sum of the cells the composition concatenates ahead of the
// allocator's fields, and nothing in the compiler relates the two: a cell added
// to the row without a term in the sum overflows the viewport, and a term
// without a cell silently steals directory width. This drives the real function
// with every optional column reserved at once, across every band, and asserts
// the composed row lands exactly one cell under the budget — the single cell of
// deliberate slack rowSafetySlack documents.
//
// The fixture is chosen so the last field in each band is always the binding
// constraint: a long directory for the bands that draw one, and a long title for
// compact, which draws nothing after it. Without that the row would be narrower
// than its budget for an uninteresting reason and the test would assert nothing.
func TestFormatRowWidthArithmeticMatchesTheComposedRow(t *testing.T) {
	s := session.RunningSession{
		Account: "work",
		Base:    "api",
		// Wider than any band's title column, so compact — which draws nothing
		// after the title — still fills its budget to the last cell.
		Project: "an api gateway service with a deliberately long project title for band tests",
		// Long enough that the directory is the binding constraint at every
		// width below that draws one, which is what makes the row's total width
		// a statement about the arithmetic rather than about this path.
		Directory: "/home/user/projects/agent-mux/worktrees/api-feature-x/internal/daemon/state/reducer/deep/deeper/deepest/and/deeper/still/for/the/widest/band",
	}
	cells := []struct {
		name  string
		cells []string
	}{
		{"no optional columns", []string{"1: ", "", "", "", ""}},
		{"every optional column", []string{
			"12: ",
			renderActiveLoopCell(true),
			renderReasonCell(session.RunningSession{Reason: "command_approval"}, 0, 5),
			"0:42 ",
			renderBranchCell(session.RunningSession{Branch: "amx/feat-x", Dirty: true}, 12),
		}},
	}
	for _, tc := range cells {
		for _, width := range []int{36, 79, 80, 119, 120, 160} {
			band := BandFor(width)
			row := FormatRow(RowInput{
				Session:        s,
				Band:           band,
				Width:          width,
				TabNumberCell:  tc.cells[0],
				ActiveLoopCell: tc.cells[1],
				ReasonCell:     tc.cells[2],
				ActivityCell:   tc.cells[3],
				BranchCell:     tc.cells[4],
			})
			got := layout.StyledWidth(row)
			if got != width-1 {
				t.Errorf("%s at width %d (%s): composed row is %d cells, want %d",
					tc.name, width, band, got, width-1)
			}
		}
	}
}

func TestTabNumberColumnAlignsSingleAndMultiDigitNumbers(t *testing.T) {
	sessions := []session.RunningSession{
		{TabNumber: 2},
		{TabNumber: 12},
		{},
	}
	width := tabNumberColumnWidth(sessions)
	if width != 4 {
		t.Fatalf("tab number column width = %d, want 4", width)
	}
	if got := renderTabNumberCell(2, width); got != " 2: " {
		t.Errorf("single-digit cell = %q, want %q", got, " 2: ")
	}
	if got := renderTabNumberCell(12, width); got != "12: " {
		t.Errorf("multi-digit cell = %q, want %q", got, "12: ")
	}
	if got := renderTabNumberCell(0, width); got != "    " {
		t.Errorf("unknown cell = %q, want four spaces", got)
	}
}
