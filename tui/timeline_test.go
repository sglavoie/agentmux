package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
)

// se builds a minimal SessionEvent for use in timeline tests.
func se(at int64, action, event, tool string) daemon.SessionEvent {
	return daemon.SessionEvent{At: at, Action: action, Event: event, ToolName: tool}
}

func seFlags(at int64, action string, contextLimit, resetFlow, suppressed bool) daemon.SessionEvent {
	return daemon.SessionEvent{
		At:           at,
		Action:       action,
		Event:        action,
		ContextLimit: contextLimit,
		ResetFlow:    resetFlow,
		Suppressed:   suppressed,
	}
}

// TestGroupIntoPhasesEmpty verifies that an empty event slice yields a single preamble.
func TestGroupIntoPhasesEmpty(t *testing.T) {
	phases := groupIntoPhases(nil)
	if len(phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(phases))
	}
	if phases[0].kind != "preamble" {
		t.Errorf("expected kind=preamble, got %q", phases[0].kind)
	}
	if len(phases[0].events) != 0 {
		t.Errorf("expected no events, got %d", len(phases[0].events))
	}
}

func TestGroupIntoPhasesEmptySlice(t *testing.T) {
	phases := groupIntoPhases([]daemon.SessionEvent{})
	if len(phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(phases))
	}
	if phases[0].kind != "preamble" {
		t.Errorf("expected kind=preamble, got %q", phases[0].kind)
	}
}

// TestGroupIntoPhasesOngoingActive verifies a single active phase with no terminal event stays ongoing.
func TestGroupIntoPhasesOngoingActive(t *testing.T) {
	events := []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1010, "active", "PreToolUse", "bash"),
	}
	phases := groupIntoPhases(events)
	if len(phases) != 1 {
		t.Fatalf("expected 1 phase, got %d: %+v", len(phases), phases)
	}
	p := phases[0]
	if p.kind != "active" {
		t.Errorf("expected kind=active, got %q", p.kind)
	}
	if !p.endAt.IsZero() {
		t.Errorf("expected zero endAt (ongoing), got %v", p.endAt)
	}
	if p.termEvent != nil {
		t.Errorf("expected nil termEvent for ongoing phase")
	}
}

// TestGroupIntoPhasesActiveThenStop verifies two complete phases with no gap.
func TestGroupIntoPhasesTwoActiveStopCycles(t *testing.T) {
	// timestamps close together (<60s gap) so no gap phases are inserted
	events := []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1010, "active", "PreToolUse", "bash"),
		se(1020, "stop", "Stop", ""),
		se(1030, "active", "SessionStart", ""),
		se(1040, "active", "PreToolUse", "write_file"),
		se(1050, "stop", "Stop", ""),
	}
	phases := groupIntoPhases(events)
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %+v", len(phases), phases)
	}
	if phases[0].kind != "active" || phases[1].kind != "active" {
		t.Errorf("expected both phases to be active, got %q and %q", phases[0].kind, phases[1].kind)
	}
	if phases[0].termEvent == nil || phases[0].termEvent.Action != "stop" {
		t.Errorf("phase 0: expected termEvent with action=stop")
	}
	if phases[1].termEvent == nil || phases[1].termEvent.Action != "stop" {
		t.Errorf("phase 1: expected termEvent with action=stop")
	}
	if phases[0].endAt.IsZero() {
		t.Error("phase 0: expected non-zero endAt (terminated by stop)")
	}
	if phases[1].endAt.IsZero() {
		t.Error("phase 1: expected non-zero endAt (terminated by stop)")
	}
}

// TestGroupIntoPhasesGapInserted verifies a gap phase is inserted when phases are >60s apart.
func TestGroupIntoPhasesGapInserted(t *testing.T) {
	events := []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1005, "stop", "Stop", ""),
		// gap: 1005 → 1075 = 70s ≥ 60s
		se(1075, "active", "SessionStart", ""),
		se(1080, "stop", "Stop", ""),
	}
	phases := groupIntoPhases(events)
	// expected: active, active, gap between them — but gap is inserted between phases
	// order: active(ended@1005), gap(1005→1075), active(ended@1080)
	if len(phases) != 3 {
		t.Fatalf("expected 3 phases (active, gap, active), got %d: %+v", len(phases), phases)
	}
	if phases[0].kind != "active" {
		t.Errorf("phase[0]: expected active, got %q", phases[0].kind)
	}
	if phases[1].kind != "gap" {
		t.Errorf("phase[1]: expected gap, got %q", phases[1].kind)
	}
	if phases[2].kind != "active" {
		t.Errorf("phase[2]: expected active, got %q", phases[2].kind)
	}
	gapDur := phases[1].endAt.Sub(phases[1].startAt)
	if gapDur != 70*time.Second {
		t.Errorf("gap duration: expected 70s, got %v", gapDur)
	}
}

// TestGroupIntoPhasesNoGapBelowThreshold verifies no gap phase when gap < 60s.
func TestGroupIntoPhasesNoGapBelowThreshold(t *testing.T) {
	events := []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1005, "stop", "Stop", ""),
		// gap: 1005 → 1030 = 25s < 60s
		se(1030, "active", "SessionStart", ""),
		se(1035, "stop", "Stop", ""),
	}
	phases := groupIntoPhases(events)
	for _, p := range phases {
		if p.kind == "gap" {
			t.Errorf("expected no gap phase, got one: %+v", p)
		}
	}
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %+v", len(phases), phases)
	}
}

// TestGroupIntoPhasesSuppressedEventsIncluded verifies suppressed events are kept in phase.events.
func TestGroupIntoPhasesSuppressedEventsIncluded(t *testing.T) {
	events := []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		{At: 1010, Action: "active", Event: "PreToolUse", ToolName: "bash", Suppressed: true},
	}
	phases := groupIntoPhases(events)
	if len(phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(phases))
	}
	found := false
	for _, ev := range phases[0].events {
		if ev.Suppressed {
			found = true
		}
	}
	if !found {
		t.Error("expected suppressed event to be present in phase.events")
	}
}

// TestGroupIntoPhasesContextLimit verifies contextLimit flag propagates from terminal event.
func TestGroupIntoPhasesContextLimit(t *testing.T) {
	ev := seFlags(1010, "stop_failure", true, false, false)
	events := []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		ev,
	}
	phases := groupIntoPhases(events)
	if len(phases) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(phases))
	}
	if !phases[0].contextLimit {
		t.Error("expected contextLimit=true on phase terminated by context-limit event")
	}
}

// TestGroupIntoPhasesResetFlow verifies a separator phase is inserted when ResetFlow is true.
func TestGroupIntoPhasesResetFlow(t *testing.T) {
	resetEv := daemon.SessionEvent{At: 1020, Action: "active", Event: "SessionStart", ResetFlow: true}
	events := []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1005, "stop", "Stop", ""),
		resetEv,
		se(1025, "active", "PreToolUse", "bash"),
	}
	phases := groupIntoPhases(events)

	var kinds []string
	for _, p := range phases {
		kinds = append(kinds, p.kind)
	}
	foundSeparator := false
	for _, k := range kinds {
		if k == "separator" {
			foundSeparator = true
		}
	}
	if !foundSeparator {
		t.Errorf("expected a separator phase, got kinds: %v", kinds)
	}
}

// TestToolSummaryEmpty verifies empty string when no PreToolUse events exist.
func TestToolSummaryEmpty(t *testing.T) {
	p := timelinePhase{events: []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
	}}
	if got := p.toolSummary(); got != "" {
		t.Errorf("expected empty toolSummary, got %q", got)
	}
}

// TestToolSummaryCountsAndFormat verifies correct counts, ordering, and format string.
func TestToolSummaryCountsAndFormat(t *testing.T) {
	p := timelinePhase{events: []daemon.SessionEvent{
		{At: 1, Event: "PreToolUse", Action: "active", ToolName: "bash"},
		{At: 2, Event: "PreToolUse", Action: "active", ToolName: "bash"},
		{At: 3, Event: "PreToolUse", Action: "active", ToolName: "bash"},
		{At: 4, Event: "PreToolUse", Action: "active", ToolName: "write_file"},
		{At: 5, Event: "PreToolUse", Action: "active", ToolName: "write_file"},
		{At: 6, Event: "PreToolUse", Action: "active", ToolName: "read_file"},
		{At: 7, Event: "PostToolUse", Action: "active", ToolName: "bash"}, // not PreToolUse; excluded
	}}
	got := p.toolSummary()
	// bash×3 should appear before write_file×2 before read_file×1
	if !strings.Contains(got, "bash") {
		t.Errorf("expected bash in toolSummary, got %q", got)
	}
	if !strings.Contains(got, "write_file") {
		t.Errorf("expected write_file in toolSummary, got %q", got)
	}
	bashIdx := strings.Index(got, "bash")
	writeIdx := strings.Index(got, "write_file")
	readIdx := strings.Index(got, "read_file")
	if bashIdx > writeIdx {
		t.Errorf("expected bash before write_file in %q", got)
	}
	if writeIdx > readIdx {
		t.Errorf("expected write_file before read_file in %q", got)
	}
	if !strings.Contains(got, "×3") {
		t.Errorf("expected ×3 for bash, got %q", got)
	}
	if !strings.Contains(got, "×2") {
		t.Errorf("expected ×2 for write_file, got %q", got)
	}
}

// TestToolSummaryTop4Cap verifies only the top 4 tools appear.
func TestToolSummaryTop4Cap(t *testing.T) {
	events := make([]daemon.SessionEvent, 0, 5)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		events = append(events, daemon.SessionEvent{Event: "PreToolUse", Action: "active", ToolName: name})
	}
	p := timelinePhase{events: events}
	got := p.toolSummary()
	// Count separators (entries separated by double space)
	parts := strings.Split(got, "  ")
	if len(parts) > 4 {
		t.Errorf("expected at most 4 tool entries, got %d in %q", len(parts), got)
	}
}

// TestPhaseDuration tests all duration formatting cases.
func TestPhaseDuration(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	cases := []struct {
		name  string
		start time.Time
		end   time.Time // zero = ongoing
		want  string
	}{
		{"ongoing", base, time.Time{}, "ongoing"},
		{"0s", base, base, "0s"},
		{"30s", base, base.Add(30 * time.Second), "30s"},
		{"59s", base, base.Add(59 * time.Second), "59s"},
		{"1m00s", base, base.Add(60 * time.Second), "1m00s"},
		{"3m08s", base, base.Add(3*time.Minute + 8*time.Second), "3m08s"},
		{"59m59s", base, base.Add(59*time.Minute + 59*time.Second), "59m59s"},
		{"1h00m", base, base.Add(time.Hour), "1h00m"},
		{"1h22m", base, base.Add(time.Hour + 22*time.Minute), "1h22m"},
		{"2h05m", base, base.Add(2*time.Hour + 5*time.Minute), "2h05m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := timelinePhase{startAt: tc.start, endAt: tc.end}
			got := p.phaseDuration()
			if got != tc.want {
				t.Errorf("phaseDuration: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGroupIntoPhasesPreamblesBeforeFirstActive ensures events before any active
// action land in a preamble phase.
func TestGroupIntoPhasesPreamblesBeforeFirstActive(t *testing.T) {
	events := []daemon.SessionEvent{
		se(1000, "idle", "SessionStart", ""),
		se(1005, "active", "PreToolUse", "bash"),
	}
	phases := groupIntoPhases(events)
	if len(phases) < 1 {
		t.Fatalf("expected at least 1 phase, got 0")
	}
	if phases[0].kind != "preamble" {
		t.Errorf("expected first phase to be preamble, got %q", phases[0].kind)
	}
}

// TestGroupIntoPhasesSubagentAndCompact verifies subagent and compact open their own phases.
func TestGroupIntoPhasesSubagentAndCompact(t *testing.T) {
	events := []daemon.SessionEvent{
		se(1000, "active", "SessionStart", ""),
		se(1005, "subagent", "SubagentStart", ""),
		se(1010, "compact", "PreCompact", ""),
	}
	phases := groupIntoPhases(events)
	kinds := make([]string, len(phases))
	for i, p := range phases {
		kinds[i] = p.kind
	}
	wantContains := []string{"active", "subagent", "compact"}
	for _, want := range wantContains {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected phase kind %q in %v", want, kinds)
		}
	}
}

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
		events := make([]daemon.SessionEvent, tc.eventCount)
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
	p := timelinePhase{kind: "active", events: make([]daemon.SessionEvent, 3)}
	if !isPhaseExpanded(p, 0, nil) {
		t.Error("expected expanded for ≤5 events with nil fold")
	}
}

// TestIsPhaseExpandedDefaultLarge verifies >5 events → collapsed with nil fold.
func TestIsPhaseExpandedDefaultLarge(t *testing.T) {
	p := timelinePhase{kind: "active", events: make([]daemon.SessionEvent, 6)}
	if isPhaseExpanded(p, 0, nil) {
		t.Error("expected collapsed for >5 events with nil fold")
	}
}

// TestIsPhaseExpandedFoldOverride verifies the fold map takes precedence over the default.
func TestIsPhaseExpandedFoldOverride(t *testing.T) {
	p := timelinePhase{kind: "active", events: make([]daemon.SessionEvent, 6)} // >5 → default collapsed
	if !isPhaseExpanded(p, 0, map[int]bool{0: true}) {
		t.Error("fold map true should override default collapsed")
	}

	p2 := timelinePhase{kind: "active", events: make([]daemon.SessionEvent, 2)} // ≤5 → default expanded
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
	events := []daemon.SessionEvent{
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
	events := []daemon.SessionEvent{
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
	events := []daemon.SessionEvent{
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

// ── Rendering ────────────────────────────────────────────────────────────────

// TestRenderTimelineLinesCollapsedVsExpanded verifies line count increases when a phase is expanded.
func TestRenderTimelineLinesCollapsedVsExpanded(t *testing.T) {
	events := []daemon.SessionEvent{
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
	events := []daemon.SessionEvent{
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
	events := []daemon.SessionEvent{
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
	events := []daemon.SessionEvent{
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
	events := []daemon.SessionEvent{
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

// TestClampTimelineScroll verifies scroll is pinned to [0, max(0, totalLines-visibleH)].
func TestClampTimelineScroll(t *testing.T) {
	events := []daemon.SessionEvent{
		se(1000, "active", "PreToolUse", "bash"),
	}
	m := Model{
		detailEvents:         events,
		detailTimelineScroll: 9999,
		width:                80,
		height:               40,
	}
	phases := groupIntoPhases(m.detailEvents)
	clampTimelineScroll(&m, phases)
	if m.detailTimelineScroll < 0 {
		t.Errorf("scroll must not be negative, got %d", m.detailTimelineScroll)
	}
	// With a single small phase and a 40-row viewport, totalLines < visibleH → maxOffset=0.
	if m.detailTimelineScroll != 0 {
		t.Errorf("expected scroll clamped to 0 (few lines, large viewport), got %d", m.detailTimelineScroll)
	}
}
