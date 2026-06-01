package tui

import (
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
