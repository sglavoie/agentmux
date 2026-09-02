package history

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestToolSummaryEmpty verifies empty string when no PreToolUse events exist.
func TestToolSummaryEmpty(t *testing.T) {
	p := timelinePhase{events: []protocol.SessionEvent{
		se(1000, "active", "SessionStart", ""),
	}}
	if got := p.toolSummary(); got != "" {
		t.Errorf("expected empty toolSummary, got %q", got)
	}
}

// TestToolSummaryCountsAndFormat verifies correct counts, ordering, and format string.
func TestToolSummaryCountsAndFormat(t *testing.T) {
	p := timelinePhase{events: []protocol.SessionEvent{
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
	events := make([]protocol.SessionEvent, 0, 5)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		events = append(events, protocol.SessionEvent{Event: "PreToolUse", Action: "active", ToolName: name})
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
