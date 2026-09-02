package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestReducerCurrentConversationUsesNewestLiveParentHarnessFact(t *testing.T) {
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:parent-1"},
		Action:    "active",
		SessionID: "parent-1",
		Seq:       1,
		At:        100,
	})
	// A child can arrive later but only carries lifecycle attribution; it must
	// never replace the parent conversation identity.
	r.Apply(protocol.ReducerEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceSubagent, Key: "child-1"},
		Action:    "subagent",
		SessionID: "child-session",
		Seq:       2,
		At:        200,
	})

	if got, ok := r.CurrentConversation(200); !ok || got != (conversation.Identity{Harness: "claude", SessionID: "parent-1"}) {
		t.Fatalf("CurrentConversation() = %+v, %v; want claude/parent-1", got, ok)
	}

	r.Apply(protocol.ReducerEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:parent-2"},
		Action:    "active",
		SessionID: "parent-2",
		Seq:       3,
		At:        300,
	})
	if got, ok := r.CurrentConversation(300); !ok || got != (conversation.Identity{Harness: "codex", SessionID: "parent-2"}) {
		t.Fatalf("newer parent CurrentConversation() = %+v, %v; want codex/parent-2", got, ok)
	}

	// Acknowledged sources are stale and cannot leave a false live mapping.
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "user"}, Action: "user_clear", Seq: 4, At: 301})
	if got, ok := r.CurrentConversation(301); ok {
		t.Fatalf("acknowledged facts produced stale conversation %+v", got)
	}
}

func TestReducerCurrentConversationRejectsExpiredAndMalformedFacts(t *testing.T) {
	t.Run("expired terminal", func(t *testing.T) {
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{
			Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"},
			Action:    "stop",
			SessionID: "old",
			Seq:       1,
			At:        10,
		})
		if got, ok := r.CurrentConversation(10 + tombstoneRetentionSeconds); ok {
			t.Fatalf("expired terminal fact produced %+v", got)
		}
	})

	t.Run("source key must agree with fact", func(t *testing.T) {
		r := NewReducer(ReducerConfig{})
		r.Apply(protocol.ReducerEvent{
			Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:one"},
			Action:    "active",
			SessionID: "two",
			Seq:       1,
			At:        10,
		})
		if got, ok := r.CurrentConversation(10); ok {
			t.Fatalf("malformed fact produced %+v", got)
		}
	})
}

func TestConversationSurvivesDurableStateAndDaemonRestore(t *testing.T) {
	now := time.Now()
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:thread-7"},
		Action:    "active",
		SessionID: "thread-7",
		Seq:       1,
		At:        now.Unix(),
	})
	encoded, err := (WindowState{Reducer: r}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar: %v", err)
	}
	if !strings.Contains(encoded, `"c":{"harness":"codex","session_id":"thread-7"}`) {
		t.Fatalf("durable state omitted conversation identity: %s", encoded)
	}

	var restored WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar: %v", err)
	}
	if got, ok := restored.CurrentConversation(now.Unix()); !ok || got != (conversation.Identity{Harness: "codex", SessionID: "thread-7"}) {
		t.Fatalf("restored CurrentConversation() = %+v, %v", got, ok)
	}

	h := newTestHandler()
	h.restoreFromUserVars([]kitty.OSWindow{{Tabs: []kitty.Tab{{Windows: []kitty.Window{{
		ID:       42,
		UserVars: kitty.UserVars{Profile: "work", Harness: "codex", AMXState: encoded},
	}}}}}}, now)
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	got := resp.States[42].Conversation
	if got == nil || *got != (conversation.Identity{Harness: "codex", SessionID: "thread-7"}) {
		t.Fatalf("restored state-query conversation = %+v", got)
	}
}

func TestConversationProjectionNeverTrustsDurableCacheWithoutFacts(t *testing.T) {
	var state WindowState
	if err := state.UnmarshalUserVar(`{"v":2,"c":{"harness":"claude","session_id":"stale"}}`); err != nil {
		t.Fatalf("UnmarshalUserVar: %v", err)
	}
	if got, ok := state.CurrentConversation(time.Now().Unix()); ok {
		t.Fatalf("cache-only durable conversation was trusted: %+v", got)
	}

	h := newTestHandler()
	h.stateMu.Lock()
	h.store[8] = &state
	h.stateMu.Unlock()
	if got := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[8].Conversation; got != nil {
		t.Fatalf("state snapshot exposed cache-only conversation: %+v", got)
	}
}

func TestWindowStateSnapshotOmitsAbsentConversationForLegacyClients(t *testing.T) {
	data, err := json.Marshal(protocol.WindowStateSnapshot{IconState: "bolt", LastAction: "active"})
	if err != nil {
		t.Fatalf("Marshal snapshot: %v", err)
	}
	if strings.Contains(string(data), "conversation") {
		t.Fatalf("identity-less snapshot changed legacy response shape: %s", data)
	}
}
