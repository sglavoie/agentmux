package actions

import (
	"context"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// The daemon keys a tmux session's state by its runtime session ID.
func TestOperatorIconActionsAddressRuntimeSession(t *testing.T) {
	row := session.RunningSession{Base: "web", Backend: runtimesession.BackendTmux,
		Pane: backend.PaneID("%0"), RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef"}
	for action, id := range map[string]ID{"user_clear": ClearStatus, "user_active": MarkActive} {
		var sent []protocol.Request
		deps := Dependencies{Daemon: func(req protocol.Request) (protocol.Response, error) {
			sent = append(sent, req)
			return protocol.Response{OK: true}, nil
		}}
		if _, err := DefaultRegistry().Execute(context.Background(), deps, id, Request{Session: &row}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(sent) != 1 || sent[0].Action != action || sent[0].WindowID != 0 || sent[0].RuntimeSessionID != string(row.RuntimeSessionID) {
			t.Fatalf("%s request = %+v, want the runtime session", id, sent)
		}
	}
}

func TestOperatorIconActionsRefuseMissingRuntimeSessionID(t *testing.T) {
	row := session.RunningSession{Base: "api", Backend: runtimesession.BackendTmux}
	for _, id := range []ID{ClearStatus, MarkActive} {
		sent := false
		deps := Dependencies{Daemon: func(protocol.Request) (protocol.Response, error) {
			sent = true
			return protocol.Response{OK: true}, nil
		}}
		if _, err := DefaultRegistry().Execute(context.Background(), deps, id, Request{Session: &row}); err == nil {
			t.Fatalf("%s accepted a session without a runtime ID", id)
		}
		if sent {
			t.Fatalf("%s sent an unidentified state update", id)
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
	result, err := focusWorktreeSession(running)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.Data["continuation"] != DataContinuationAttach || result.Data["runtime_session_id"] != string(running.RuntimeSessionID) {
		t.Fatalf("result = %+v, want an attach continuation naming the runtime session", result)
	}
	if live, ok := result.Data["session"].(WorktreeLiveSession); !ok || live.TabID != running.TabID {
		t.Fatalf("session = %#v, want the live session view", result.Data["session"])
	}
}
