package commands

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// ConversationAction runs an indexed-conversation action away from the UI loop.
// Continue performs the exact-live lookup in the shared action registry; fork
// validates and launches only a native branch. Both preserve the session-search
// screen rather than overloading Enter's viewer.
func ConversationAction(deps actions.Dependencies, meta transcript.SessionMeta, actionID actions.ID) tea.Cmd {
	if actionID == actions.Continue {
		if identity, ok := conversation.New(meta.Harness, meta.ID); ok {
			if rs, live := LivePortableConversation(identity); live {
				return Attach(rs)
			}
		}
	}
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), deps, actionID, actions.Request{Conversation: &meta})
		return ActionDoneMsg{Err: err}
	}
}

// ConversationWorktreeFork forks a conversation into an isolated managed Git
// worktree. The confirmation gate is the caller's; by the time this runs the
// operator has already agreed to the mutation.
func ConversationWorktreeFork(deps actions.Dependencies, meta transcript.SessionMeta) tea.Cmd {
	return func() tea.Msg {
		_, err := Registry.Execute(context.Background(), deps, actions.ForkWorktree, actions.Request{Conversation: &meta, Confirmed: true})
		return ActionDoneMsg{Err: err}
	}
}

// ConversationHandoff builds a cross-harness handoff pack for a conversation
// and launches the target profile against it.
func ConversationHandoff(deps actions.Dependencies, meta transcript.SessionMeta, profile string, edit bool) tea.Cmd {
	return func() tea.Msg {
		parameters := map[string]string{"profile": profile}
		if edit {
			parameters["edit"] = "true"
		}
		_, err := Registry.Execute(context.Background(), deps, actions.Handoff, actions.Request{Conversation: &meta, Parameters: parameters})
		return ActionDoneMsg{Err: err}
	}
}
