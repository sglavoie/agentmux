package history

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// ── Rendering ────────────────────────────────────────────────────────────────

// TestRenderTimelineLinesCollapsedVsExpanded verifies line count increases when a phase is expanded.
func TestRenderTimelineLinesCollapsedVsExpanded(t *testing.T) {
	events := []protocol.SessionEvent{
		se(1000, "active", "PreToolUse", "bash"),
		se(1001, "active", "PreToolUse", "bash"),
		se(1002, "active", "PreToolUse", "bash"),
		se(1003, "active", "PreToolUse", "bash"),
		se(1004, "active", "PreToolUse", "bash"),
		se(1005, "active", "PreToolUse", "bash"),
		se(1006, "active", "PreToolUse", "bash"), // 7 events
	}
	phases := groupIntoPhases(events)
	// Use explicit fold entries so we're not affected by lastContentIdx defaults.
	linesCollapsed := renderTimelineLines(phases, 80, map[int]bool{0: false}, false, false)
	linesExpanded := renderTimelineLines(phases, 80, map[int]bool{0: true}, false, false)
	if len(linesExpanded) <= len(linesCollapsed) {
		t.Errorf("expanded (%d) should have more lines than collapsed (%d)", len(linesExpanded), len(linesCollapsed))
	}
}

// TestRenderTimelineLinesSuppressedToggle verifies suppressed events hide/show correctly.
// When showSuppressed=false the suppressed event row is replaced by a "N more hidden" summary
// line, so total line counts are equal; we only check text content.
func TestRenderTimelineLinesSuppressedToggle(t *testing.T) {
	events := []protocol.SessionEvent{
		{At: 1000, Action: "active", Event: "PreToolUse", ToolName: "bash"},
		{At: 1001, Action: "active", Event: "PreToolUse", ToolName: "write_file", Suppressed: true},
		{At: 1002, Action: "active", Event: "PreToolUse", ToolName: "read_file"},
	}
	phases := groupIntoPhases(events)
	fold := map[int]bool{0: true} // force-expand so event rows appear

	linesHidden := renderTimelineLines(phases, 80, fold, false, false)
	linesShown := renderTimelineLines(phases, 80, fold, true, false)

	hiddenJoined := strings.Join(linesHidden, "\n")
	shownJoined := strings.Join(linesShown, "\n")

	if strings.Contains(hiddenJoined, "write_file") {
		t.Error("suppressed write_file should not appear when showSuppressed=false")
	}
	if !strings.Contains(hiddenJoined, "hidden") {
		t.Error("expected 'hidden' summary line when showSuppressed=false")
	}
	if !strings.Contains(shownJoined, "write_file") {
		t.Error("suppressed write_file should appear when showSuppressed=true")
	}
	if strings.Contains(shownJoined, "hidden") {
		t.Error("'hidden' summary line should not appear when showSuppressed=true")
	}
}

// TestRenderTimelineLinesContainsSeparator verifies /compact separator appears in output.
func TestRenderTimelineLinesContainsSeparator(t *testing.T) {
	events := []protocol.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1005, "stop", "Stop", ""),
		{At: 1010, Action: "active", Event: "SessionStart", ResetFlow: true},
	}
	phases := groupIntoPhases(events)
	lines := renderTimelineLines(phases, 80, nil, false, false)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "/compact") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected /compact separator in rendered lines; got: %v", lines)
	}
}

// TestRenderTimelineLinesContainsGap verifies gap line appears for >60s idle periods.
func TestRenderTimelineLinesContainsGap(t *testing.T) {
	events := []protocol.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1005, "stop", "Stop", ""),
		se(1075, "active", "SessionStart", ""), // 70s gap
	}
	phases := groupIntoPhases(events)
	lines := renderTimelineLines(phases, 80, nil, false, false)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "gap") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected gap line in rendered output; got: %v", lines)
	}
}

// TestRenderTimelineLinesEmptyEvents verifies a single preamble line (or none) for empty input.
func TestRenderTimelineLinesEmptyEvents(t *testing.T) {
	phases := groupIntoPhases(nil)
	lines := renderTimelineLines(phases, 80, nil, false, false)
	// empty preamble → no lines emitted (preamble with 0 events is skipped)
	if len(lines) != 0 {
		t.Errorf("expected 0 lines for empty event list, got %d: %v", len(lines), lines)
	}
}

// TestRenderTimelineLinesOngoing verifies last phase with no termEvent renders correctly.
func TestRenderTimelineLinesOngoing(t *testing.T) {
	events := []protocol.SessionEvent{
		se(1000, "active", "PreToolUse", "bash"),
	}
	phases := groupIntoPhases(events)
	lines := renderTimelineLines(phases, 80, nil, false, false)
	if len(lines) == 0 {
		t.Fatal("expected at least one line for an ongoing active phase")
	}
	found := false
	for _, line := range lines {
		if strings.Contains(line, "ongoing") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'ongoing' in lines for phase with no termEvent; got: %v", lines)
	}
}
