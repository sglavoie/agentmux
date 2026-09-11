package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// ---- State group sorting tests ----

func TestStateGroupSorting(t *testing.T) {
	m := testModel()
	sessions := []session.RunningSession{
		{TabID: 1, Base: "idle1", Account: "work", State: ""},
		{TabID: 2, Base: "active1", Account: "work", State: "bolt"},
		{TabID: 3, Base: "attention1", Account: "personal", State: "key"},
		{TabID: 4, Base: "attention2", Account: "personal", State: "stop", Unseen: true},
		{TabID: 5, Base: "attention3", Account: "work", State: "error"},
		{TabID: 6, Base: "readstop", Account: "work", State: "stop"},
	}
	m = loadSessions(m, sessions)

	// Attention comes first in severity order (error, key/question, unseen
	// stop); then Active with an explicit work icon (bolt) before empty-state
	// Active sessions (per the no-record→Active design). The seen stop sorts
	// below both: it is Idle, and it is the only row here whose bucket its state
	// name alone would have got wrong.
	expected := []string{"attention3", "attention1", "attention2", "active1", "idle1", "readstop"}
	if len(m.filtered) != len(expected) {
		t.Fatalf("expected %d filtered sessions, got %d", len(expected), len(m.filtered))
	}
	for i, want := range expected {
		if m.filtered[i].Base != want {
			t.Errorf("filtered[%d]: expected %q, got %q", i, want, m.filtered[i].Base)
		}
	}
}

func TestRenderListGroupDividers(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 40
	sessions := []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work", State: "key"},
		{TabID: 2, WindowID: 12, Base: "backend", Account: "work", State: "bolt"},
		{TabID: 3, WindowID: 13, Base: "docs", Account: "work", State: ""},
	}
	m = loadSessions(m, sessions)
	// Mark docs as stale so it lands in Idle rather than the default Active
	// no-record bucket.
	m.idleThreshold = time.Second
	m.lastActivity = map[int]time.Time{13: time.Now().Add(-time.Hour)}
	output := stripAnsi(renderList(m))
	if !strings.Contains(output, "Needs you (1)") {
		t.Errorf("expected attention divider, got:\n%s", output)
	}
	if !strings.Contains(output, "Running (1)") {
		t.Errorf("expected active divider, got:\n%s", output)
	}
	if !strings.Contains(output, "Idle (1)") {
		t.Errorf("expected idle divider, got:\n%s", output)
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for _, label := range []string{"Running (1)", "Idle (1)"} {
		idx := indexLineContaining(lines, label)
		if idx < 1 {
			t.Fatalf("expected %q after a separator line; lines=%q", label, lines)
		}
		if lines[idx-1] != "" {
			t.Errorf("expected blank separator before %q, got previous line %q", label, lines[idx-1])
		}
	}
}

func indexLineContaining(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func TestSingleGroupNoDivider(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 40
	// Both empty-state with no activity record → both go to the Running bucket,
	// so the renderer should omit the group divider entirely. The heading above
	// them stays: it counts the bucket, which is a fact whether or not a second
	// bucket exists to be separated from.
	sessions := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: ""},
		{TabID: 2, Base: "backend", Account: "work", State: ""},
	}
	m = loadSessions(m, sessions)
	output := renderList(m)
	if strings.Contains(output, "──") {
		t.Errorf("expected no dividers when all sessions are in one group, got:\n%s", output)
	}
	if !strings.Contains(stripAnsi(output), "Running (2)") {
		t.Errorf("expected the single bucket to still be counted, got:\n%s", output)
	}
}

// ---- Picker count tests ----

func TestPickerSelectedCountInHeader(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l") // → ModePicker

	// Nothing selected: header should not contain count.
	output := renderPicker(m)
	if strings.Contains(output, "selected]") {
		t.Errorf("expected no selected count when none selected, got:\n%s", output)
	}

	// Select one.
	m = pressKey(m, " ")
	output = renderPicker(m)
	if !strings.Contains(output, "[1 selected]") {
		t.Errorf("expected [1 selected] after selecting one, got:\n%s", output)
	}

	// Select a second.
	m = pressKey(m, "j")
	m = pressKey(m, " ")
	output = renderPicker(m)
	if !strings.Contains(output, "[2 selected]") {
		t.Errorf("expected [2 selected] after selecting two, got:\n%s", output)
	}
}

// ---- Attention row smoke test ----

func TestRenderListAttentionRowDoesNotPanic(t *testing.T) {
	m := testModel()
	m.width, m.height = 120, 40
	sessions := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "key"},
		{TabID: 2, Base: "backend", Account: "work", State: "bolt"},
	}
	m = loadSessions(m, sessions)
	_ = renderList(m) // should not panic; attention row gets orange style
}
