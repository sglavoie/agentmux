package commands

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/session"
)

// ActiveLoopDiscover is the seam the refresh command discovers through, so the
// in-process UI harness can serve deterministic candidates.
var ActiveLoopDiscover = agentloop.Discover

// ActiveLoopRefresh rediscovers active agent-session-loop runs. The sequence
// number rides along so a late reply for a superseded refresh can be dropped.
func ActiveLoopRefresh(seq int) tea.Cmd {
	return func() tea.Msg {
		candidates, err := ActiveLoopDiscover(agentloop.DiscoverOptions{})
		if err != nil {
			return ActiveLoopsMsg{Seq: seq, Err: err}
		}
		return ActiveLoopsMsg{Seq: seq, Candidates: candidates}
	}
}

// ActiveLoopWorkspaceTargets projects the live session list into the workspace
// identities agentloop matches a run against.
func ActiveLoopWorkspaceTargets(sessions []session.RunningSession) []agentloop.WorkspaceTarget {
	targets := make([]agentloop.WorkspaceTarget, 0, len(sessions))
	for _, s := range sessions {
		targets = append(targets, agentloop.WorkspaceTarget{
			CWD:      s.Directory,
			Harness:  s.Harness,
			WindowID: s.WindowID,
		})
	}
	return targets
}
