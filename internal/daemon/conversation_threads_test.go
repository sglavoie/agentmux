package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func TestAcceptedInteractiveHooksRetainConversationAcrossExitAndDaemonRestart(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	store := conversation.ThreadStore{Path: filepath.Join(t.TempDir(), "links.json")}
	h.conversationThreads = &store
	id := newRuntimeID(t)
	directory.put(id, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	requireAccepted(t, h.Handle(runtimeHook(t, id, "UserPromptSubmit", hookevent.HookPayload{SessionID: "receiving"})))
	// The hook can arrive before the launcher's response. No app is running.
	source := conversation.Identity{Harness: "claude", SessionID: "source"}
	if err := store.Record(conversation.Link{ID: "handoff-one", Source: source, TargetKey: string(id), Kind: "handoff", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	links, err := store.Links()
	if err != nil || len(links) != 1 || links[0].Target == nil || links[0].Target.SessionID != "receiving" {
		t.Fatalf("accepted hook was not retained: %+v %v", links, err)
	}
	before, _ := os.Stat(store.Path)
	requireAccepted(t, h.Handle(runtimeHook(t, id, "PermissionRequest", hookevent.HookPayload{SessionID: "receiving"})))
	after, _ := os.Stat(store.Path)
	if !os.SameFile(before, after) {
		t.Fatal("unchanged hook rewrote durable history")
	}
	// Retiring the runtime and replacing the daemon do not remove the link.
	directory.put(id, runtimesession.StateEnded, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	h.persistConversationBindings()
	restarted := conversation.ThreadStore{Path: store.Path}
	links, err = restarted.Links()
	if err != nil || len(conversation.Thread(links, *links[0].Target)) != 1 {
		t.Fatalf("lost closed session relationship: %v", err)
	}
}

func TestRejectedRuntimeHookCannotCreateConversationBinding(t *testing.T) {
	h, _ := newRuntimeTestHandler(t)
	store := conversation.ThreadStore{Path: filepath.Join(t.TempDir(), "links.json")}
	h.conversationThreads = &store
	id := newRuntimeID(t)
	h.Handle(runtimeHook(t, id, "UserPromptSubmit", hookevent.HookPayload{SessionID: "untrusted"}))
	if _, err := os.Stat(store.Path); !os.IsNotExist(err) {
		t.Fatalf("rejected hook wrote history: %v", err)
	}
}

func TestPipeOnlyChildCannotClaimReceivingConversation(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	store := conversation.ThreadStore{Path: filepath.Join(t.TempDir(), "links.json")}
	h.conversationThreads = &store
	id := newRuntimeID(t)
	directory.put(id, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "generation", "%1")
	child := runtimeHook(t, id, "UserPromptSubmit", hookevent.HookPayload{SessionID: "child"})
	child.PipeMode = true
	requireAccepted(t, h.Handle(child))
	if _, err := os.Stat(store.Path); !os.IsNotExist(err) {
		t.Fatalf("pipe child claimed receiving identity: %v", err)
	}
	requireAccepted(t, h.Handle(runtimeHook(t, id, "UserPromptSubmit", hookevent.HookPayload{SessionID: "interactive"})))
	if err := store.Record(conversation.Link{ID: "handoff", Source: conversation.Identity{Harness: "codex", SessionID: "source"}, TargetKey: string(id), Kind: "handoff", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	links, err := store.Links()
	if err != nil || len(links) != 1 || links[0].Target == nil || links[0].Target.SessionID != "interactive" {
		t.Fatalf("wrong receiving conversation: %+v %v", links, err)
	}
}
