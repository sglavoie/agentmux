package tui

import (
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/session"

	tea "charm.land/bubbletea/v2"
)

var activeLoopDiscover = agentloop.Discover

func activeLoopRefreshCmd(seq int) tea.Cmd {
	return func() tea.Msg {
		candidates, err := activeLoopDiscover(agentloop.DiscoverOptions{})
		if err != nil {
			return activeLoopsMsg{seq: seq, err: err}
		}
		return activeLoopsMsg{seq: seq, candidates: candidates}
	}
}

func (m Model) activeLoopsForSession(s session.RunningSession) []agentloop.Candidate {
	matches, err := agentloop.MatchingWorkspace(
		m.activeLoopCandidates,
		agentloop.WorkspaceTarget{CWD: s.Directory, Harness: s.Harness, WindowID: s.WindowID},
		activeLoopWorkspaceTargets(m.sessions),
	)
	if err != nil {
		return nil
	}
	return matches
}

func activeLoopWorkspaceTargets(sessions []session.RunningSession) []agentloop.WorkspaceTarget {
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
