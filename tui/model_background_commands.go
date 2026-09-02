package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/commands"
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
	aslSnapshot    func(Model, int) tea.Cmd
	// aslWorkbench takes the row, plan directory, and focus rather than a link,
	// because the tokenized URL is minted and consumed inside the command.
	aslWorkbench func(Model, string, string, string) tea.Cmd
	// aslQuestion fetches exactly one question body.
	aslQuestion func(Model, int, string, string) tea.Cmd
	// aslAction submits one explicit mutation. The request carries the operator's
	// answer, so no layer between here and the protocol writer may format it.
	aslAction func(Model, int, bool, string, aslbridge.ActionRequest) tea.Cmd
}

func defaultModelBackgroundCommands() modelBackgroundCommands {
	return modelBackgroundCommands{
		initialFetch: func(m Model, sourceWindowID int) tea.Cmd {
			return commands.InitialFetch(m.client, m.cfg, m.sortMode, sourceWindowID)
		},
		activeLoops: commands.ActiveLoopRefresh,
		health: func(m Model, checkDaemon, checkHooks bool) tea.Cmd {
			return commands.Health(m.cfg, m.amxBin, checkDaemon, checkHooks)
		},
		detail: sessionDetailCmd,
		worktrees: func(m Model, seq int) tea.Cmd {
			return worktreeListCmd(m, seq)
		},
		worktreeAction: func(m Model, id actions.ID, request actions.Request) tea.Cmd {
			return worktreeLifecycleActionCmd(m, id, request)
		},
		aslSnapshot: func(_ Model, seq int) tea.Cmd {
			return aslSnapshotCmd(seq)
		},
		aslWorkbench: func(_ Model, rowID, planDir, focus string) tea.Cmd {
			return aslWorkbenchCmd(rowID, planDir, focus)
		},
		aslQuestion: func(_ Model, seq int, planDir, questionID string) tea.Cmd {
			return aslQuestionCmd(seq, planDir, questionID)
		},
		aslAction: func(_ Model, seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
			return aslActionCmd(seq, control, rowID, req)
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
