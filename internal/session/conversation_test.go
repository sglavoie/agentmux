package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestResolveLiveConversationRequiresExactCurrentOwner(t *testing.T) {
	t.Parallel()

	target := conversation.Identity{Harness: "claude", SessionID: "session-1"}
	owner := kitty.Window{ID: 10, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", Project: "api"}}
	windows := []kitty.OSWindow{{Tabs: []kitty.Tab{
		{ID: 99, Title: "shell", Windows: []kitty.Window{{ID: 9, IsActive: true}}},
		{ID: 100, Title: "api", Windows: []kitty.Window{owner}},
	}}}
	states := map[int]protocol.WindowStateSnapshot{10: {Conversation: &target}}

	got, ok := session.ResolveLiveConversation(target, states, windows)
	if !ok {
		t.Fatal("exact live owner should resolve")
	}
	if got.TabID != 100 || got.TabNumber != 2 || got.WindowID != 10 || got.Harness != "claude" || got.Project != "api" {
		t.Fatalf("resolved session = %+v", got)
	}
}

func TestResolveLiveConversationRejectsStaleSiblingAndOwnerMismatch(t *testing.T) {
	t.Parallel()

	target := conversation.Identity{Harness: "claude", SessionID: "session-1"}
	other := conversation.Identity{Harness: "claude", SessionID: "session-2"}
	owner := kitty.Window{ID: 10, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: "claude"}}
	sibling := kitty.Window{ID: 11, UserVars: kitty.UserVars{Profile: "work", Harness: "claude"}}
	split := []kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 100, Title: "api", Windows: []kitty.Window{owner, sibling}}}}}

	tests := []struct {
		name    string
		states  map[int]protocol.WindowStateSnapshot
		windows []kitty.OSWindow
	}{
		{
			name:    "closed window snapshot",
			states:  map[int]protocol.WindowStateSnapshot{10: {Conversation: &target}},
			windows: nil,
		},
		{
			name:    "split pane sibling",
			states:  map[int]protocol.WindowStateSnapshot{11: {Conversation: &target}},
			windows: split,
		},
		{
			name:   "owner harness changed",
			states: map[int]protocol.WindowStateSnapshot{10: {Conversation: &target}},
			windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 100, Windows: []kitty.Window{{
				ID: 10, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
			}}}}}},
		},
		{
			name:    "different parent session",
			states:  map[int]protocol.WindowStateSnapshot{10: {Conversation: &other}},
			windows: split,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := session.ResolveLiveConversation(target, tc.states, tc.windows); ok {
				t.Fatalf("unexpected resolution: %+v", got)
			}
		})
	}
}

func TestResolveLiveConversationRejectsAmbiguousDuplicateOwners(t *testing.T) {
	t.Parallel()

	target := conversation.Identity{Harness: "codex", SessionID: "thread-1"}
	states := map[int]protocol.WindowStateSnapshot{
		10: {Conversation: &target},
		20: {Conversation: &target},
	}
	windows := []kitty.OSWindow{{Tabs: []kitty.Tab{
		{ID: 100, Windows: []kitty.Window{{ID: 10, IsActive: true, UserVars: kitty.UserVars{Profile: "one", Harness: "codex"}}}},
		{ID: 200, Windows: []kitty.Window{{ID: 20, IsActive: true, UserVars: kitty.UserVars{Profile: "two", Harness: "codex"}}}},
	}}}

	if got, ok := session.ResolveLiveConversation(target, states, windows); ok {
		t.Fatalf("ambiguous live owners resolved to %+v", got)
	}
}
