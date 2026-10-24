package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/layout"
)

// TestLiveSessionCandidateNamesTmuxByBackendAndAligns pins the worktree
// live-session picker row: a tmux candidate is named by its backend and
// runtime session rather than the process-local tab it is listed under, and
// a wide project name does not push the later columns out of line.
func TestLiveSessionCandidateNamesTmuxByBackendAndAligns(t *testing.T) {
	kitty := actions.WorktreeLiveSession{ID: "12", TabID: 12, WindowID: 120, Project: "api", Profile: "work"}
	tmux := actions.WorktreeLiveSession{
		ID: "rs-1", TabID: 1 << 41, WindowID: 1 << 41, Backend: "tmux", RuntimeSessionID: "rs-1",
		Project: "🐱 café", Profile: "work",
	}
	kittyLine := formatLiveSessionCandidate(kitty, 200)
	tmuxLine := formatLiveSessionCandidate(tmux, 200)
	if !strings.Contains(kittyLine, "tab 12 / window 120") {
		t.Errorf("kitty row lacks its tab and window: %q", kittyLine)
	}
	if strings.Contains(tmuxLine, "tab ") || strings.Contains(tmuxLine, "2199023255552") || !strings.Contains(tmuxLine, "candidate rs-1") {
		t.Errorf("tmux row = %q, want its backend and runtime session, no portable tab", tmuxLine)
	}
	at := func(line string) int { return layout.Width(line[:strings.Index(line, "candidate")]) }
	if a, b := at(kittyLine), at(tmuxLine); a != b {
		t.Errorf("candidate column starts at %d and %d; want the same cell", a, b)
	}
}
