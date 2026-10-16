package commands

import (
	"context"
	"fmt"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"

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

// RecoverClosed reuses the continuity service's source validation and live-owner
// checks. A known conversation never silently falls back to a fresh launch.
func RecoverClosed(ctx context.Context, deps actions.Dependencies, project config.Session, identity conversation.Identity, backend runtimesession.BackendKind) error {
	deps.Backend = backend // A recorded empty backend still means Kitty.
	id, request := actions.Launch, actions.Request{Project: &project}
	if identity.Valid() {
		if deps.Readers == nil {
			deps.Readers = map[string]transcript.Reader{
				harness.Claude: transcript.NewClaudeReader(), harness.Codex: transcript.NewCodexReader(), harness.Copilot: transcript.NewCopilotReader(),
			}
		}
		index, err := transcript.LoadIndex()
		if err != nil {
			return fmt.Errorf("loading conversation for recovery: %w", err)
		}
		key := transcript.IndexKey(identity.Harness, identity.SessionID)
		meta, found := index.Get(key)
		if !found {
			if err := index.Refresh(deps.Config, deps.Readers); err != nil {
				return fmt.Errorf("finding closed conversation: %w", err)
			}
			meta, found = index.Get(key)
		}
		if !found {
			return fmt.Errorf("closed conversation is not indexed; open Past Sessions and refresh before resuming")
		}
		id, request = actions.Continue, actions.Request{Conversation: &meta}
	}
	_, err := Registry.Execute(ctx, deps, id, request)
	return err
}
