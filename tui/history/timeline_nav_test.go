package history

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// ── Phase navigation helpers ──────────────────────────────────────────────────

// TestTimelinePhaseExpandedDefault verifies the default fold logic.
func TestTimelinePhaseExpandedDefault(t *testing.T) {
	cases := []struct {
		eventCount    int
		isLastContent bool
		wantExpanded  bool
	}{
		{3, false, true},
		{5, false, true},
		{6, false, false},
		{6, true, true}, // last content phase is always expanded by default
	}
	for _, tc := range cases {
		events := make([]protocol.SessionEvent, tc.eventCount)
		p := timelinePhase{kind: "active", events: events}
		lastIdx := -1
		if tc.isLastContent {
			lastIdx = 0
		}
		got := timelinePhaseExpanded(0, p, nil, lastIdx)
		if got != tc.wantExpanded {
			t.Errorf("eventCount=%d isLastContent=%v: got expanded=%v, want %v",
				tc.eventCount, tc.isLastContent, got, tc.wantExpanded)
		}
	}
}

// TestIsPhaseExpandedDefaultSmall verifies ≤5 events → expanded with nil fold.
func TestIsPhaseExpandedDefaultSmall(t *testing.T) {
	p := timelinePhase{kind: "active", events: make([]protocol.SessionEvent, 3)}
	if !isPhaseExpanded(p, 0, nil) {
		t.Error("expected expanded for ≤5 events with nil fold")
	}
}

// TestIsPhaseExpandedDefaultLarge verifies >5 events → collapsed with nil fold.
func TestIsPhaseExpandedDefaultLarge(t *testing.T) {
	p := timelinePhase{kind: "active", events: make([]protocol.SessionEvent, 6)}
	if isPhaseExpanded(p, 0, nil) {
		t.Error("expected collapsed for >5 events with nil fold")
	}
}

// TestIsPhaseExpandedFoldOverride verifies the fold map takes precedence over the default.
func TestIsPhaseExpandedFoldOverride(t *testing.T) {
	p := timelinePhase{kind: "active", events: make([]protocol.SessionEvent, 6)} // >5 → default collapsed
	if !isPhaseExpanded(p, 0, map[int]bool{0: true}) {
		t.Error("fold map true should override default collapsed")
	}

	p2 := timelinePhase{kind: "active", events: make([]protocol.SessionEvent, 2)} // ≤5 → default expanded
	if isPhaseExpanded(p2, 0, map[int]bool{0: false}) {
		t.Error("fold map false should override default expanded")
	}
}

// TestPhaseAtScroll verifies phaseAtScroll across header / between / past-last positions.
func TestPhaseAtScroll(t *testing.T) {
	positions := []int{0, 5, 10}
	cases := []struct {
		scroll int
		want   int
	}{
		{0, 0},
		{3, 0},
		{5, 1},
		{7, 1},
		{10, 2},
		{15, 2},
	}
	for _, tc := range cases {
		got := phaseAtScroll(positions, tc.scroll)
		if got != tc.want {
			t.Errorf("phaseAtScroll(%d): got %d, want %d", tc.scroll, got, tc.want)
		}
	}
}

// TestPhaseAtScrollEmpty verifies -1 for empty positions.
func TestPhaseAtScrollEmpty(t *testing.T) {
	if got := phaseAtScroll(nil, 5); got != -1 {
		t.Errorf("expected -1 for empty positions, got %d", got)
	}
}

// TestPhaseHeaderLinePositionsSinglePhase verifies positions for a single active phase.
func TestPhaseHeaderLinePositionsSinglePhase(t *testing.T) {
	events := []protocol.SessionEvent{
		se(1000, "active", "PreToolUse", "bash"),
	}
	phases := groupIntoPhases(events)
	positions := phaseHeaderLinePositions(phases, nil, false)
	if len(positions) != len(phases) {
		t.Fatalf("positions length %d != phases length %d", len(positions), len(phases))
	}
	if positions[0] != 0 {
		t.Errorf("expected first phase at line 0, got %d", positions[0])
	}
}

// TestPhaseHeaderLinePositionsFoldEffect verifies that expanding a phase pushes later phase headers down.
func TestPhaseHeaderLinePositionsFoldEffect(t *testing.T) {
	// Build a phase with >5 events (default collapsed), then a gap, then a second phase.
	events := []protocol.SessionEvent{
		se(1000, "active", "PreToolUse", "bash"),
		se(1001, "active", "PreToolUse", "bash"),
		se(1002, "active", "PreToolUse", "bash"),
		se(1003, "active", "PreToolUse", "bash"),
		se(1004, "active", "PreToolUse", "bash"),
		se(1005, "active", "PreToolUse", "bash"),
		se(1006, "stop", "Stop", ""),
		se(1076, "active", "PreToolUse", "write_file"), // 70s gap
	}
	phases := groupIntoPhases(events)
	if len(phases) < 2 {
		t.Fatalf("expected ≥2 phases, got %d", len(phases))
	}

	posCollapsed := phaseHeaderLinePositions(phases, nil, false)                  // first phase >5 events → collapsed by default
	posExpanded := phaseHeaderLinePositions(phases, map[int]bool{0: true}, false) // force-expand

	// The second-phase (or gap) header should shift down when first phase is expanded.
	if posExpanded[1] <= posCollapsed[1] {
		t.Errorf("expanded phase 0 should push phase 1 down: collapsed[1]=%d expanded[1]=%d",
			posCollapsed[1], posExpanded[1])
	}
}

// TestPhaseHeaderLinePositionsThreeKinds verifies preamble/gap/active ordering.
func TestPhaseHeaderLinePositionsThreeKinds(t *testing.T) {
	// preamble event at 1000, then 70s gap, then active phase at 1070
	events := []protocol.SessionEvent{
		se(1000, "idle", "SessionStart", ""), // idle → preamble (closed immediately)
		se(1070, "active", "PreToolUse", "bash"),
	}
	phases := groupIntoPhases(events)
	var kinds []string
	for _, p := range phases {
		kinds = append(kinds, p.kind)
	}

	positions := phaseHeaderLinePositions(phases, nil, false)
	if len(positions) != len(phases) {
		t.Fatalf("positions len %d != phases len %d (kinds=%v)", len(positions), len(phases), kinds)
	}
	// positions must be strictly non-decreasing
	for i := 1; i < len(positions); i++ {
		if positions[i] < positions[i-1] {
			t.Errorf("positions not monotonic at [%d]: %d < %d (kinds=%v)", i, positions[i], positions[i-1], kinds)
		}
	}
}

// TestClampTimelineScroll verifies scroll is pinned to [0, max(0, totalLines-visibleH)].
func TestClampTimelineScroll(t *testing.T) {
	events := []protocol.SessionEvent{
		se(1000, "active", "PreToolUse", "bash"),
	}
	in := DetailInput{
		Events:         events,
		TimelineScroll: 9999,
		Width:          80,
		Height:         40,
		BodyHeight:     40,
	}
	scroll := in.ClampTimelineScroll()
	if scroll < 0 {
		t.Errorf("scroll must not be negative, got %d", scroll)
	}
	// With a single small phase and a 40-row viewport, totalLines < visibleH → maxOffset=0.
	if scroll != 0 {
		t.Errorf("expected scroll clamped to 0 (few lines, large viewport), got %d", scroll)
	}
}
