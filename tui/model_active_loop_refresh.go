package tui

import (
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func (m Model) activeLoopsForSession(s session.RunningSession) []agentloop.Candidate {
	matches, err := agentloop.MatchingWorkspace(
		m.activeLoopCandidates,
		agentloop.WorkspaceTarget{CWD: s.Directory, Harness: s.Harness, WindowID: s.WindowID},
		commands.ActiveLoopWorkspaceTargets(m.sessions),
	)
	if err != nil {
		return nil
	}
	return matches
}
