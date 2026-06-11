package tui

import (
	"github.com/sglavoie/agentmux/internal/agentloop"

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

func (m Model) activeLoopsForDirectory(directory string) []agentloop.Candidate {
	matches, err := agentloop.MatchingCWD(m.activeLoopCandidates, directory)
	if err != nil {
		return nil
	}
	return matches
}
