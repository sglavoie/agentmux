package actions

import (
	"context"
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestContinueReturnsLiveRuntimeWithoutLaunchingOrReadingSource(t *testing.T) {
	identity := conversation.Identity{Harness: "codex", SessionID: "source-1"}
	meta := continuityMeta(t, identity.Harness, identity.SessionID, "work")
	reader := &continuityReader{harness: identity.Harness}
	// An existing runtime session can be attached without a launcher.
	var shown []session.LaunchResult
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Backend:  runtimesession.BackendTmux,
		Readers:  map[string]transcript.Reader{identity.Harness: reader},
		Launched: func(result session.LaunchResult) { shown = append(shown, result) },
		Daemon: func(protocol.Request) (protocol.Response, error) {
			return protocol.Response{OK: true,
				States:       map[int]protocol.WindowStateSnapshot{123: {Conversation: &identity}},
				RuntimeSlots: map[int]protocol.RuntimeSlot{123: {RuntimeSessionID: "rs-live", Backend: "tmux"}},
			}, nil
		},
	}, Continue, Request{Conversation: &meta})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Data["continuation"] != DataContinuationAttach || result.Data["runtime_session_id"] != "rs-live" {
		t.Fatalf("continue = %+v, want the existing session to attach", result)
	}
	if reader.reads != 0 {
		t.Fatal("continuing a live runtime changed its source or launched an agent")
	}
	if len(shown) != 1 || shown[0].RuntimeSessionID != "rs-live" || shown[0].Backend != runtimesession.BackendTmux {
		t.Fatalf("workspace was not told to show the existing session: %+v", shown)
	}
}

func TestContinueRefusesDuplicateAndUnboundRuntimeOwners(t *testing.T) {
	identity := conversation.Identity{Harness: "codex", SessionID: "source-1"}
	for _, unbound := range []bool{false, true} {
		response := protocol.Response{OK: true,
			States: map[int]protocol.WindowStateSnapshot{123: {Conversation: &identity}, 10: {Conversation: &identity}},
			RuntimeSlots: map[int]protocol.RuntimeSlot{
				123: {RuntimeSessionID: "rs-one", Backend: "tmux"},
				10:  {RuntimeSessionID: "rs-two", Backend: "tmux"},
			},
		}
		if unbound {
			delete(response.RuntimeSlots, 10)
		}
		meta := continuityMeta(t, identity.Harness, identity.SessionID, "work")
		_, err := DefaultRegistry().Execute(context.Background(), Dependencies{
			Config: continuityConfig(identity.Harness),
			Daemon: func(protocol.Request) (protocol.Response, error) { return response, nil },
		}, Continue, Request{Conversation: &meta})
		var actionErr *Error
		want := "ambiguous_conversation"
		if unbound {
			want = "live_lookup_unavailable"
		}
		if !errors.As(err, &actionErr) || actionErr.Code != want {
			t.Fatalf("unbound=%v: error = %v, want %s", unbound, err, want)
		}
	}
}
