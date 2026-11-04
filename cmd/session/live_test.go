package session

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestLiveConversationsUseUniqueTmuxBindings(t *testing.T) {
	first := conversation.Identity{Harness: "codex", SessionID: "first"}
	duplicate := conversation.Identity{Harness: "claude", SessionID: "duplicate"}
	legacy := conversation.Identity{Harness: "claude", SessionID: "legacy"}
	respond := func(protocol.Request) (protocol.Response, error) {
		return protocol.Response{OK: true,
			States: map[int]protocol.WindowStateSnapshot{
				1: {Conversation: &first}, 2: {Conversation: &duplicate},
				3: {Conversation: &duplicate}, 4: {Conversation: &legacy},
			},
			RuntimeSlots: map[int]protocol.RuntimeSlot{
				1: {RuntimeSessionID: "rs-first", Backend: "tmux"},
				2: {RuntimeSessionID: "rs-second", Backend: "tmux"},
				3: {RuntimeSessionID: "rs-third", Backend: "tmux"},
			},
		}, nil
	}
	live, known := liveConversations(respond)
	if !known || len(live) != 1 || live["codex:first"].RuntimeSessionID != "rs-first" {
		t.Fatalf("live = %+v, known = %t; want only the unique tmux conversation", live, known)
	}
}

func TestLiveConversationsRequireDaemonAnswer(t *testing.T) {
	down := func(protocol.Request) (protocol.Response, error) { return protocol.Response{}, errors.New("offline") }
	if live, known := liveConversations(down); known || live != nil {
		t.Fatalf("offline daemon = %+v, %t", live, known)
	}
	rejected := func(protocol.Request) (protocol.Response, error) { return protocol.Response{OK: false}, nil }
	if _, known := liveConversations(rejected); known {
		t.Fatal("rejected state query reported known live sessions")
	}
}

func TestSessionTableMarksLiveConversations(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	views := []sessionView{
		{Harness: "claude", Profile: "p", Project: "x", LastAt: now, ID: "aaaa1111", Snippet: "open one"},
		{Harness: "codex", Profile: "p", Project: "x", LastAt: now, ID: "bbbb2222", Snippet: "closed one"},
	}
	previous := currentRuntimeSession
	currentRuntimeSession = func() string { return "rs-current" }
	t.Cleanup(func() { currentRuntimeSession = previous })
	markLive(views, map[string]liveConversation{"claude:aaaa1111": {RuntimeSessionID: "rs-current"}})
	if views[0].Live == nil || !views[0].Live.Here || views[1].Live != nil {
		t.Fatalf("live views = %+v, %+v", views[0].Live, views[1].Live)
	}
	var out bytes.Buffer
	if err := writeSessionTable(&out, views, nil, now, 0, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), " LIVE ") || !strings.Contains(out.String(), "here") {
		t.Fatalf("table = %q", out.String())
	}
	out.Reset()
	if err := writeSessionTable(&out, views, nil, now, 0, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "LIVE") {
		t.Fatalf("unknown live state shown: %q", out.String())
	}
}
