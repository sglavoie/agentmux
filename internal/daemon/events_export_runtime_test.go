package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// An events export restricted to a runtime session returns that session's
// slot and nothing else, on a tmux session (no Kitty window to name) and on a
// Kitty one alike, and says whose slot it is.
func TestEventsExportSelectsByRuntimeSession(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	onKitty, onTmux, other := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(onKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	directory.put(other, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%8")
	for runtime, session := range map[runtimesession.ID]string{
		onKitty: "session-kitty", onTmux: "session-tmux", other: "session-other",
	} {
		requireAccepted(t, h.Handle(runtimeHook(t, runtime, "UserPromptSubmit", hookevent.HookPayload{SessionID: session})))
	}

	for _, tc := range []struct {
		runtime runtimesession.ID
		backend string
	}{{onKitty, "kitty"}, {onTmux, "tmux"}} {
		slot := slotOf(t, h, tc.runtime)
		resp := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, RuntimeSessionID: string(tc.runtime)})
		if !resp.OK {
			t.Fatalf("%s export refused: %+v", tc.backend, resp)
		}
		if len(resp.EventsByWindow) != 1 || len(resp.EventsByWindow[slot]) == 0 {
			t.Fatalf("%s export = slots %v; want only slot %d with events", tc.backend, keysOf(resp.EventsByWindow), slot)
		}
		for _, event := range resp.EventsByWindow[slot] {
			if event.Origin == nil || event.Origin.RuntimeSessionID != string(tc.runtime) {
				t.Fatalf("%s export holds an event of another session: %+v", tc.backend, event.Origin)
			}
		}
		bound, ok := resp.RuntimeSlots[slot]
		if len(resp.RuntimeSlots) != 1 || !ok || bound.RuntimeSessionID != string(tc.runtime) || bound.Backend != tc.backend {
			t.Fatalf("%s export names slots %+v; want only %d = %s", tc.backend, resp.RuntimeSlots, slot, tc.runtime)
		}
		// The same selection through the slot itself is the same export.
		bySlot := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: slot})
		if len(bySlot.EventsByWindow[slot]) != len(resp.EventsByWindow[slot]) {
			t.Fatalf("%s export by slot has %d events; by runtime session %d", tc.backend, len(bySlot.EventsByWindow[slot]), len(resp.EventsByWindow[slot]))
		}
	}

	// The unrestricted export names every bound slot.
	all := h.Handle(protocol.Request{Type: protocol.TypeEventsExport})
	if len(all.RuntimeSlots) != 3 || len(all.EventsByWindow) != 3 {
		t.Fatalf("full export: %d slots named, %d exported; want 3 and 3", len(all.RuntimeSlots), len(all.EventsByWindow))
	}
}

// A session the daemon holds no slot for is refused: answering with every
// window would hand the caller other sessions' history under its own name.
func TestEventsExportRefusesAnUnknownRuntimeSession(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	known, unknown := newRuntimeID(t), newRuntimeID(t)
	directory.put(known, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	requireAccepted(t, h.Handle(runtimeHook(t, known, "UserPromptSubmit", hookevent.HookPayload{SessionID: "session"})))

	resp := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, RuntimeSessionID: string(unknown)})
	if resp.OK || resp.Error != protocol.OriginReasonRuntimeSessionUnknown || len(resp.EventsByWindow) != 0 {
		t.Fatalf("unknown runtime session export = %+v; want refused as %s with no events", resp, protocol.OriginReasonRuntimeSessionUnknown)
	}
	// Naming a window as well is refused too: the two could disagree.
	resp = h.Handle(protocol.Request{Type: protocol.TypeEventsExport, RuntimeSessionID: string(known), WindowID: 7})
	if resp.OK || len(resp.EventsByWindow) != 0 {
		t.Fatalf("export naming a session and a window = %+v; want refused", resp)
	}
}

func keysOf[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
