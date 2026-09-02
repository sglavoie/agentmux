package tui

// This file is the model-bound half of the effect layer. The tea.Cmd builders
// themselves live in tui/commands, which knows nothing about the root model;
// what remains here is the wiring that charges the model's injected seams — the
// daemon transport, the Kitty client, the transcript readers, the handoff and
// worktree runners — against those builders.

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
)

func (m Model) clearStatusCmd(s session.RunningSession) tea.Cmd {
	return commands.ClearStatusWithDaemon(m.actionDaemon(), s)
}

// conversationDependencies collects the injected seams every
// indexed-conversation action needs. A nil handoff or worktree runner tells the
// registry to use its real workflow.
func (m Model) conversationDependencies(readers map[string]transcript.Reader) actions.Dependencies {
	return actions.Dependencies{
		Kitty:    m.client,
		Config:   m.cfg,
		Readers:  readers,
		Daemon:   m.actionDaemon(),
		Handoff:  m.handoffRunner,
		Worktree: m.worktreeRunner,
	}
}

func (m Model) conversationActionCmd(readers map[string]transcript.Reader, meta transcript.SessionMeta, actionID actions.ID) tea.Cmd {
	return commands.ConversationAction(m.conversationDependencies(readers), meta, actionID)
}

func (m Model) conversationWorktreeForkCmd(meta transcript.SessionMeta) tea.Cmd {
	return commands.ConversationWorktreeFork(m.conversationDependencies(m.transcriptReaders), meta)
}

func (m Model) conversationHandoffCmd(meta transcript.SessionMeta, profile string, edit bool) tea.Cmd {
	return commands.ConversationHandoff(m.conversationDependencies(m.transcriptReaders), meta, profile, edit)
}

func (m Model) actionDaemon() func(protocol.Request) (protocol.Response, error) {
	if m.daemonCommand != nil {
		return m.daemonCommand
	}
	return commands.DaemonRequest
}
