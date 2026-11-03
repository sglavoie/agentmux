package actions

import (
	"context"
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestContinueReturnsLiveRuntimeWithoutLaunchingOrReadingSource(t *testing.T) {
	identity := conversation.Identity{Harness: "codex", SessionID: "source-1"}
	meta := continuityMeta(t, identity.Harness, identity.SessionID, "work")
	reader := &continuityReader{harness: identity.Harness}
	// Neither the configured launch backend nor Kitty needs to be available
	// when the conversation already has a live runtime session.
	client := &continuityKitty{listErr: errors.New("Kitty is unavailable")}
	var shown []session.LaunchResult
	result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
		Kitty: client, Backend: runtimesession.BackendTmux,
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
	if reader.reads != 0 || len(client.launched) != 0 || len(client.focuses) != 0 {
		t.Fatal("continuing a live runtime changed its source or launched an agent")
	}
	if len(shown) != 1 || shown[0].RuntimeSessionID != "rs-live" || shown[0].Backend != runtimesession.BackendTmux {
		t.Fatalf("workspace was not told to show the existing session: %+v", shown)
	}
}

func TestContinueResolvesKittySlotsOnlyWithinTheirInstance(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/kitty-local")
	identity := conversation.Identity{Harness: "codex", SessionID: "source-1"}
	for _, tc := range []struct {
		name, socket string
		slot         int
	}{
		{"local alias", "unix:/kitty-local", 123},
		{"foreign alias", "unix:/kitty-foreign", 123},
		{"foreign colliding window ID", "unix:/kitty-foreign", 10},
		{"unattributed instance", "", 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &continuityKitty{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{
				ID: 100, Windows: []kitty.Window{{ID: 10, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: identity.Harness}}},
			}}}}}
			meta := continuityMeta(t, identity.Harness, identity.SessionID, "work")
			result, err := DefaultRegistry().Execute(context.Background(), Dependencies{
				Kitty: client, Config: continuityConfig(identity.Harness),
				Daemon: func(protocol.Request) (protocol.Response, error) {
					return protocol.Response{OK: true,
						States:     map[int]protocol.WindowStateSnapshot{tc.slot: {Conversation: &identity}},
						KittySlots: map[int]protocol.KittySlot{tc.slot: {Socket: tc.socket, WindowID: 10}},
					}, nil
				},
			}, Continue, Request{Conversation: &meta})
			if tc.socket == "unix:/kitty-local" {
				if err != nil || result.Data["continuation"] != "focused" || len(client.focuses) != 1 {
					t.Fatalf("local aliased window did not focus: %+v, %v", result, err)
				}
			} else {
				var actionErr *Error
				if !errors.As(err, &actionErr) || actionErr.Code != "live_lookup_unavailable" || len(client.focuses) != 0 {
					t.Fatalf("foreign conversation was not refused: %+v, %v", result, err)
				}
			}
			if len(client.launched) != 0 {
				t.Fatal("live Kitty conversation was resumed a second time")
			}
		})
	}
}

func TestContinueRefusesDuplicateRuntimeAndMixedBackendOwners(t *testing.T) {
	identity := conversation.Identity{Harness: "codex", SessionID: "source-1"}
	for _, mixed := range []bool{false, true} {
		client := &continuityKitty{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{
			ID: 100, Windows: []kitty.Window{{ID: 10, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: identity.Harness}}},
		}}}}}
		response := protocol.Response{OK: true,
			States: map[int]protocol.WindowStateSnapshot{123: {Conversation: &identity}, 10: {Conversation: &identity}},
			RuntimeSlots: map[int]protocol.RuntimeSlot{
				123: {RuntimeSessionID: "rs-one", Backend: "tmux"},
				10:  {RuntimeSessionID: "rs-two", Backend: "tmux"},
			},
		}
		if mixed {
			delete(response.RuntimeSlots, 10)
		}
		meta := continuityMeta(t, identity.Harness, identity.SessionID, "work")
		_, err := DefaultRegistry().Execute(context.Background(), Dependencies{
			Kitty: client, Config: continuityConfig(identity.Harness),
			Daemon: func(protocol.Request) (protocol.Response, error) { return response, nil },
		}, Continue, Request{Conversation: &meta})
		var actionErr *Error
		if !errors.As(err, &actionErr) || actionErr.Code != "ambiguous_conversation" {
			t.Fatalf("mixed=%v: error = %v, want ambiguous_conversation", mixed, err)
		}
		if len(client.focuses) != 0 || len(client.launched) != 0 {
			t.Fatal("ambiguous conversation changed a session")
		}
	}
}
