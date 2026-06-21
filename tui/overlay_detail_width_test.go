package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestDetailHeaderPadsEmojiTitleIconColumn locks in that the detail
// overlay header routes the project title through displayRenderTitleIcon
// so a leading pictograph occupies the 2-cell title-icon column without
// gaining a phantom space.
func TestDetailHeaderPadsEmojiTitleIconColumn(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, WindowID: 10, Base: "🗣commscoach", Project: "🗣commscoach", Account: "work", State: "bolt"},
	})
	mm, _ := m.enterDetailMode(10, false)
	m = mm.(Model)
	m.detailLoading = false

	out := stripAnsi(detailHeader(m))
	if !strings.Contains(out, "🗣commscoach") {
		t.Errorf("expected title-icon column without phantom space, got:\n%s", out)
	}
}

// TestPadRightAtomicOnEmojiCluster verifies the detail-row padRight
// helper truncates without splitting a wide grapheme: a 2-cell emoji in
// the last visible slot must either fit whole or be replaced by the
// tail. A naive byte slice would have cut the emoji in half.
func TestPadRightAtomicOnEmojiCluster(t *testing.T) {
	// "Ab🐱" is 4 cells (A, b, 🐱=2). Width 3 must drop the cat and
	// land at "Ab…" — never "Ab" + half an emoji.
	got := padRight("Ab🐱", 3)
	if displayWidth(got) != 3 {
		t.Fatalf("padRight visible width = %d, want 3 (got %q)", displayWidth(got), got)
	}
	if got != "Ab…" {
		t.Errorf("padRight emoji-truncate = %q, want %q", got, "Ab…")
	}

	// Width 4 fits the cat exactly with no tail.
	full := padRight("Ab🐱", 4)
	if displayWidth(full) != 4 {
		t.Fatalf("padRight visible width = %d, want 4 (got %q)", displayWidth(full), full)
	}
	if full != "Ab🐱" {
		t.Errorf("padRight at exact width = %q, want %q", full, "Ab🐱")
	}
}

// TestSessionEndedSeparatorMeasuresLabel verifies the centered separator
// pads to exactly the requested width by measuring the label through
// displayWidth (not runewidth.StringWidth) — both report the same width
// for an ASCII label, so the regression value is keeping the result
// pinned to the requested column budget.
func TestSessionEndedSeparatorMeasuresLabel(t *testing.T) {
	out := stripAnsi(sessionEndedSeparator(40))
	if displayWidth(out) != 40 {
		t.Errorf("sessionEndedSeparator(40) width = %d, want 40 (got %q)", displayWidth(out), out)
	}
	if !strings.Contains(out, "session ended") {
		t.Errorf("expected label in separator, got %q", out)
	}
}

// TestTimelineGapDashesGraphemeAware confirms gap and separator rows are
// padded to the requested column width. A regression to byte-based math
// would produce too-short or too-long dash runs depending on how the
// fixed prefix/suffix is measured.
func TestTimelineGapDashesGraphemeAware(t *testing.T) {
	events := []daemon.SessionEvent{
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
		if w := displayWidth(plain); w != 60 {
			t.Errorf("gap line width = %d, want 60 (line=%q)", w, plain)
		}
	}
	if !gotGap {
		t.Fatalf("expected at least one gap line in: %v", lines)
	}
}

// TestTimelineSeparatorWidthGraphemeAware mirrors the gap test for the
// /compact separator branch.
func TestTimelineSeparatorWidthGraphemeAware(t *testing.T) {
	events := []daemon.SessionEvent{
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
		if w := displayWidth(plain); w != 60 {
			t.Errorf("separator line width = %d, want 60 (line=%q)", w, plain)
		}
	}
	if !gotSep {
		t.Fatalf("expected at least one /compact line in: %v", lines)
	}
}

// TestPopupWidthEmojiGraphemeAware locks in that popupWidth measures
// emoji popups via displayStyledWidth so wide glyphs count for their
// full cell width (2), not their byte length.
func TestPopupWidthEmojiGraphemeAware(t *testing.T) {
	popup := "🐱🐱\n🐱"
	// Two cats per line == 4 cells; tallest line == 4.
	if got := popupWidth(popup); got != 4 {
		t.Errorf("popupWidth(emoji) = %d, want 4", got)
	}
	// Styled emoji must still measure as 2 cells.
	styled := styleSelected.Render("⚡")
	if got := popupWidth(styled); got != 2 {
		t.Errorf("popupWidth(styled ⚡) = %d, want 2", got)
	}
}

// TestComposeOverlayPreservesEmojiAtBoundary verifies the overlay
// splicing path measures both popup and base lines through grapheme-
// aware width so a wide emoji at the popup boundary survives the slice
// without losing or duplicating cells.
func TestComposeOverlayPreservesEmojiAtBoundary(t *testing.T) {
	// 10-wide base, popup carries a single cat (2 cells) starting at col 4.
	base := makeBase(10, 3)
	popup := "🐱"
	got := composeOverlay(base, popup, 10, 3, 4, 1, false)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines; got %d (%q)", len(lines), got)
	}
	plain := stripAnsi(lines[1])
	if displayWidth(plain) != 10 {
		t.Errorf("row 1 width after emoji overlay = %d, want 10 (line=%q)", displayWidth(plain), plain)
	}
	if !strings.Contains(plain, "🐱") {
		t.Errorf("row 1 should contain emoji from popup, got %q", plain)
	}
}

// TestActionsHeaderPadsEmojiTitleIcon mirrors the detail-header test for
// the actions overlay so a leading pictograph uses the title-icon column
// without gaining a phantom space.
func TestActionsHeaderPadsEmojiTitleIcon(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, WindowID: 10, Base: "🗣commscoach", Project: "🗣commscoach", Account: "work", State: "bolt"},
	})
	m = openActionsOverlay(t, m)
	out := stripAnsi(renderActions(m, 80))
	if !strings.Contains(out, "Actions for 🗣commscoach") {
		t.Errorf("expected title-icon column without phantom space in actions header, got:\n%s", out)
	}
}

// TestConfirmHeaderPadsEmojiTitleIcon mirrors the detail test for the
// confirm overlay: the project title in the y/n header should route
// through displayRenderTitleIcon so leading pictographs use the title-icon
// width policy.
func TestConfirmHeaderPadsEmojiTitleIcon(t *testing.T) {
	m := Model{
		mode:               ModeConfirm,
		confirmAction:      "close",
		confirmConsequence: closeConsequence,
		targetSession: session.RunningSession{
			Base:    "🗣commscoach",
			Project: "🗣commscoach",
		},
	}
	out := stripAnsi(renderConfirm(m))
	if !strings.Contains(out, `Close "🗣commscoach"?`) {
		t.Errorf("expected title-icon column without phantom space in confirm header, got:\n%s", out)
	}
}

// TestDeleteChoiceHeaderPadsEmojiTitleIcon mirrors the confirm test for
// the delete-choice overlay header.
func TestDeleteChoiceHeaderPadsEmojiTitleIcon(t *testing.T) {
	target := transcript.SessionMeta{
		ID:      "abc123",
		Harness: "claude",
		Profile: "work",
		Project: "🗣commscoach",
		Path:    "/tmp/demo.jsonl",
	}
	m := Model{
		mode:         ModeDeleteChoice,
		width:        100,
		height:       40,
		deleteTarget: &target,
	}
	out := stripAnsi(renderDeleteChoice(m))
	if !strings.Contains(out, `Delete "🗣commscoach"?`) {
		t.Errorf("expected title-icon column without phantom space in delete-choice header, got:\n%s", out)
	}
}

// TestDetailRowEmojiToolTruncation locks in the detailRow flex column:
// when the tool-name column budget cannot fit the full glyph string,
// the truncation must keep grapheme clusters whole and append the tail.
func TestDetailRowEmojiToolTruncation(t *testing.T) {
	ev := daemon.SessionEvent{
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
