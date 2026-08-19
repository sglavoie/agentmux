package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
)

// modelBackgroundCommands contains the long-running environment-facing
// effects started by the model. Production uses the normal Kitty, daemon,
// hook, and active-loop implementations; the in-process UI harness replaces
// every entry with a deterministic fixture command.
type modelBackgroundCommands struct {
	initialFetch   func(Model, int) tea.Cmd
	activeLoops    func(int) tea.Cmd
	health         func(Model, bool, bool) tea.Cmd
	detail         func(int) tea.Cmd
	worktrees      func(Model, int) tea.Cmd
	worktreeAction func(Model, actions.ID, actions.Request) tea.Cmd
}

func defaultModelBackgroundCommands() modelBackgroundCommands {
	return modelBackgroundCommands{
		initialFetch: func(m Model, sourceWindowID int) tea.Cmd {
			return initialFetchCmd(m.client, m.cfg, m.sortMode, sourceWindowID)
		},
		activeLoops: activeLoopRefreshCmd,
		health: func(m Model, checkDaemon, checkHooks bool) tea.Cmd {
			return healthCmd(m.cfg, m.amxBin, checkDaemon, checkHooks)
		},
		detail: sessionDetailCmd,
		worktrees: func(m Model, seq int) tea.Cmd {
			return worktreeListCmd(m, seq)
		},
		worktreeAction: func(m Model, id actions.ID, request actions.Request) tea.Cmd {
			return worktreeLifecycleActionCmd(m, id, request)
		},
	}
}

func (m Model) runWorktreeListCmd(seq int) tea.Cmd {
	if m.backgroundCommands.worktrees != nil {
		return m.backgroundCommands.worktrees(m, seq)
	}
	return defaultModelBackgroundCommands().worktrees(m, seq)
}

func (m Model) runWorktreeActionCmd(id actions.ID, request actions.Request) tea.Cmd {
	if m.backgroundCommands.worktreeAction != nil {
		return m.backgroundCommands.worktreeAction(m, id, request)
	}
	return defaultModelBackgroundCommands().worktreeAction(m, id, request)
}

func (m Model) runInitialFetchCmd(sourceWindowID int) tea.Cmd {
	if m.backgroundCommands.initialFetch != nil {
		return m.backgroundCommands.initialFetch(m, sourceWindowID)
	}
	return defaultModelBackgroundCommands().initialFetch(m, sourceWindowID)
}

func (m Model) runActiveLoopRefreshCmd(seq int) tea.Cmd {
	if m.backgroundCommands.activeLoops != nil {
		return m.backgroundCommands.activeLoops(seq)
	}
	return defaultModelBackgroundCommands().activeLoops(seq)
}

func (m Model) runHealthCmd(checkDaemon, checkHooks bool) tea.Cmd {
	if m.backgroundCommands.health != nil {
		return m.backgroundCommands.health(m, checkDaemon, checkHooks)
	}
	return defaultModelBackgroundCommands().health(m, checkDaemon, checkHooks)
}

func (m Model) runSessionDetailCmd(windowID int) tea.Cmd {
	if m.backgroundCommands.detail != nil {
		return m.backgroundCommands.detail(windowID)
	}
	return defaultModelBackgroundCommands().detail(windowID)
}
