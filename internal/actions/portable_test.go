package actions

import (
	"context"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// The daemon keys a tmux session's state by a slot only it can name, so an
// operator's icon action names the runtime session and never a window ID.
func TestOperatorIconActionsAddressASessionByItsBackend(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/kitty-a")
	kittyRow := session.RunningSession{WindowID: 42, Base: "api"}
	tmuxRow := session.RunningSession{
		WindowID: 1 << 41, Base: "web", Backend: runtimesession.BackendTmux,
		Pane: backend.PaneID("%0"), RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef",
	}
	for action, id := range map[string]ID{"user_clear": ClearStatus, "user_active": MarkActive} {
		var sent []protocol.Request
		deps := Dependencies{Daemon: func(req protocol.Request) (protocol.Response, error) {
			sent = append(sent, req)
			return protocol.Response{OK: true}, nil
		}}
		for _, row := range []session.RunningSession{kittyRow, tmuxRow} {
			if _, err := DefaultRegistry().Execute(context.Background(), deps, id, Request{Session: &row}); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
		}
		if len(sent) != 2 {
			t.Fatalf("%s sent %d requests, want 2", id, len(sent))
		}
		if sent[0].Action != action || sent[0].WindowID != 42 || sent[0].KittyListenOn != "unix:/kitty-a" || sent[0].RuntimeSessionID != "" {
			t.Fatalf("%s for a Kitty row = %+v, want it addressed by window", id, sent[0])
		}
		if sent[1].Action != action || sent[1].WindowID != 0 || sent[1].RuntimeSessionID != string(tmuxRow.RuntimeSessionID) {
			t.Fatalf("%s for a tmux row = %+v, want it addressed by runtime session", id, sent[1])
		}
	}
}

func TestOperatorIconActionsRefuseUnidentifiedKittyWindow(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "")
	row := session.RunningSession{WindowID: 42, Base: "api"}
	for _, id := range []ID{ClearStatus, MarkActive} {
		sent := false
		deps := Dependencies{Daemon: func(protocol.Request) (protocol.Response, error) {
			sent = true
			return protocol.Response{OK: true}, nil
		}}
		if _, err := DefaultRegistry().Execute(context.Background(), deps, id, Request{Session: &row}); err == nil {
			t.Fatalf("%s accepted a Kitty window without its socket", id)
		}
		if sent {
			t.Fatalf("%s sent an unidentified window update", id)
		}
	}
}

// A live session on a backend with no view of its own cannot be focused; a
// worktree reopen reports it for the caller to attach instead of failing.
func TestFocusingALiveTmuxWorktreeSessionReportsAttach(t *testing.T) {
	t.Parallel()

	running := session.RunningSession{
		TabID: 1 << 41, WindowID: 1 << 41, Backend: runtimesession.BackendTmux,
		Pane: backend.PaneID("%0"), RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Project: "api",
	}
	result, err := focusWorktreeSession(nil, running)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Data["continuation"] != DataContinuationAttach || result.Data["runtime_session_id"] != string(running.RuntimeSessionID) {
		t.Fatalf("result = %+v, want an attach continuation naming the runtime session", result)
	}
	if live, ok := result.Data["session"].(WorktreeLiveSession); !ok || live.TabID != running.TabID {
		t.Fatalf("session = %#v, want the live session view", result.Data["session"])
	}
	if _, err := focusWorktreeSession(nil, session.RunningSession{TabID: 7}); err == nil {
		t.Fatal("focusing a Kitty session with no Kitty client must fail")
	}
}
