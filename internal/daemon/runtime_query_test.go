package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// A dashboard outside Kitty knows a tmux session only by its runtime session.
// The state query has to tell it which slot that is, and the two operator
// requests it sends back have to be addressable the same way.
func TestStateQueryNamesTheRuntimeSessionOfEachBoundSlot(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	tmuxRuntime := newRuntimeID(t)
	directory.put(tmuxRuntime, runtimesession.StateLive, runtimesession.BackendTmux, "/tmp/amx/tmux.sock", "gen-1", "%0")
	requireAccepted(t, h.Handle(runtimeHook(t, tmuxRuntime, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-a"})))
	slot := slotOf(t, h, tmuxRuntime)

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	if !resp.OK {
		t.Fatalf("state query refused: %s", resp.Error)
	}
	bound, ok := resp.RuntimeSlots[slot]
	if !ok {
		t.Fatalf("runtime_slots = %v, want an entry for slot %d", resp.RuntimeSlots, slot)
	}
	if bound.RuntimeSessionID != string(tmuxRuntime) || bound.Backend != string(runtimesession.BackendTmux) {
		t.Fatalf("slot %d is reported as %+v, want runtime %s on tmux", slot, bound, tmuxRuntime)
	}
	if got := resp.States[slot].IconState; got != "key" {
		t.Fatalf("slot %d icon = %q, want the permission icon", slot, got)
	}
}

func TestOperatorUpdatesReachARuntimeSessionByItsID(t *testing.T) {
	t.Parallel()

	h, directory := newRuntimeTestHandler(t)
	tmuxRuntime := newRuntimeID(t)
	directory.put(tmuxRuntime, runtimesession.StateLive, runtimesession.BackendTmux, "/tmp/amx/tmux.sock", "gen-1", "%0")
	requireAccepted(t, h.Handle(runtimeHook(t, tmuxRuntime, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-a"})))
	slot := slotOf(t, h, tmuxRuntime)

	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, Action: "user_clear", RuntimeSessionID: string(tmuxRuntime)})
	if !resp.OK {
		t.Fatalf("user_clear by runtime session refused: %s", resp.Error)
	}
	if got := slotIcon(h, slot); got != "" {
		t.Fatalf("icon after user_clear = %q, want it cleared", got)
	}
	resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, Action: "user_active", RuntimeSessionID: string(tmuxRuntime)})
	if !resp.OK {
		t.Fatalf("user_active by runtime session refused: %s", resp.Error)
	}
	if got := slotIcon(h, slot); got != "bolt" {
		t.Fatalf("icon after user_active = %q, want bolt", got)
	}

	// Only the two operator actions are addressable this way, and only for a
	// session the daemon has a slot for; naming the slot directly stays refused.
	for name, req := range map[string]protocol.Request{
		"hook action":     {Type: protocol.TypeStateUpdate, Action: "permission", RuntimeSessionID: string(tmuxRuntime)},
		"unknown session": {Type: protocol.TypeStateUpdate, Action: "user_clear", RuntimeSessionID: string(newRuntimeID(t))},
		"direct slot":     {Type: protocol.TypeStateUpdate, Action: "user_clear", WindowID: slot},
	} {
		if resp := h.Handle(req); resp.OK {
			t.Errorf("%s: accepted, want it refused", name)
		}
	}
	if got := slotIcon(h, slot); got != "bolt" {
		t.Fatalf("icon after refused updates = %q, want it unchanged", got)
	}

	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, RuntimeSessionID: string(tmuxRuntime)})
	if !detail.OK || len(detail.Events) == 0 {
		t.Fatalf("session detail by runtime session = ok %v with %d events, want the slot's event log", detail.OK, len(detail.Events))
	}
}
