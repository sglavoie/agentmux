package history

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/tui/layout"
)

// TestPadRightAtomicOnEmojiCluster verifies the detail-row padRight
// helper truncates without splitting a wide grapheme: a 2-cell emoji in
// the last visible slot must either fit whole or be replaced by the
// tail. A naive byte slice would have cut the emoji in half.
func TestPadRightAtomicOnEmojiCluster(t *testing.T) {
	// "Ab🐱" is 4 cells (A, b, 🐱=2). Width 3 must drop the cat and
	// land at "Ab…" — never "Ab" + half an emoji.
	got := padRight("Ab🐱", 3)
	if layout.Width(got) != 3 {
		t.Fatalf("padRight visible width = %d, want 3 (got %q)", layout.Width(got), got)
	}
	if got != "Ab…" {
		t.Errorf("padRight emoji-truncate = %q, want %q", got, "Ab…")
	}

	// Width 4 fits the cat exactly with no tail.
	full := padRight("Ab🐱", 4)
	if layout.Width(full) != 4 {
		t.Fatalf("padRight visible width = %d, want 4 (got %q)", layout.Width(full), full)
	}
	if full != "Ab🐱" {
		t.Errorf("padRight at exact width = %q, want %q", full, "Ab🐱")
	}
}

// TestSessionEndedSeparatorMeasuresLabel verifies the centered separator
// pads to exactly the requested width by measuring the label through
// layout.Width (not runewidth.StringWidth) — both report the same width
// for an ASCII label, so the regression value is keeping the result
// pinned to the requested column budget.

// TestSessionEndedSeparatorMeasuresLabel verifies the centered separator
// pads to exactly the requested width by measuring the label through
// layout.Width (not runewidth.StringWidth) — both report the same width
// for an ASCII label, so the regression value is keeping the result
// pinned to the requested column budget.
func TestSessionEndedSeparatorMeasuresLabel(t *testing.T) {
	out := stripAnsi(sessionEndedSeparator(40))
	if layout.Width(out) != 40 {
		t.Errorf("sessionEndedSeparator(40) width = %d, want 40 (got %q)", layout.Width(out), out)
	}
	if !strings.Contains(out, "session ended") {
		t.Errorf("expected label in separator, got %q", out)
	}
}

// TestTimelineGapDashesGraphemeAware confirms gap and separator rows are
// padded to the requested column width. A regression to byte-based math
// would produce too-short or too-long dash runs depending on how the
// fixed prefix/suffix is measured.

// TestTimelineGapDashesGraphemeAware confirms gap and separator rows are
// padded to the requested column width. A regression to byte-based math
// would produce too-short or too-long dash runs depending on how the
// fixed prefix/suffix is measured.
func TestTimelineGapDashesGraphemeAware(t *testing.T) {
	events := []protocol.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1005, "stop", "Stop", ""),
		se(1100, "active", "SessionStart", ""), // 95s gap → emits a gap line
	}
	phases := groupIntoPhases(events)
	lines := renderTimelineLines(phases, 60, nil, false, false)
	gotGap := false
	for _, line := range lines {
		plain := stripAnsi(line)
		if !strings.Contains(plain, "gap") {
			continue
		}
		gotGap = true
		if w := layout.Width(plain); w != 60 {
			t.Errorf("gap line width = %d, want 60 (line=%q)", w, plain)
		}
	}
	if !gotGap {
		t.Fatalf("expected at least one gap line in: %v", lines)
	}
}

// TestTimelineSeparatorWidthGraphemeAware mirrors the gap test for the
// /compact separator branch.

// TestTimelineSeparatorWidthGraphemeAware mirrors the gap test for the
// /compact separator branch.
func TestTimelineSeparatorWidthGraphemeAware(t *testing.T) {
	events := []protocol.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1005, "stop", "Stop", ""),
		{At: 1010, Action: "active", Event: "SessionStart", ResetFlow: true},
	}
	phases := groupIntoPhases(events)
	lines := renderTimelineLines(phases, 60, nil, false, false)
	gotSep := false
	for _, line := range lines {
		plain := stripAnsi(line)
		if !strings.Contains(plain, "/compact") {
			continue
		}
		gotSep = true
		if w := layout.Width(plain); w != 60 {
			t.Errorf("separator line width = %d, want 60 (line=%q)", w, plain)
		}
	}
	if !gotSep {
		t.Fatalf("expected at least one /compact line in: %v", lines)
	}
}

// TestActionsHeaderPadsEmojiTitleIcon mirrors the detail-header test for
// the actions overlay so a leading pictograph uses the title-icon column
// without gaining a phantom space.

// TestDetailRowEmojiToolTruncation locks in the detailRow flex column:
// when the tool-name column budget cannot fit the full glyph string,
// the truncation must keep grapheme clusters whole and append the tail.
func TestDetailRowEmojiToolTruncation(t *testing.T) {
	ev := protocol.SessionEvent{
		At:       100,
		Event:    "PreToolUse",
		Action:   "active",
		ToolName: "🐱🐱🐱🐱cat-tool",
	}
	// Width tight enough that the tool column gets only a few cells.
	// Fixed chrome: detailColTime(8)+detailColEvent(20)+detailColArrow(1)+
	// detailColAction(12)+detailColMarker(1)=42; plus 5*detailColumnGap=5
	// → toolWidth = width-47. width=53 → toolWidth=6.
	row := stripAnsi(detailRow(ev, time.Unix(100, 0), 53))
	// The row contains the tool column; isolate by tail boundary.
	if strings.Contains(row, "🐱🐱🐱🐱cat") {
		t.Errorf("expected truncated tool column, got full string in row=%q", row)
	}
	// Ellipsis must appear when truncation kicks in.
	if !strings.Contains(row, "…") {
		t.Errorf("expected truncation ellipsis in row=%q", row)
	}
}
