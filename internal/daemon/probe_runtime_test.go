package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// TestDiagnosticProbeNamesARuntimeSession: a probe that names a runtime
// session says where that session's hooks would go, or why they would be
// refused, without doing what a hook does: it claims no slot for a session
// that has not reported and releases none for one that ended. It answers by
// session and never hands back a slot as a window ID.
func TestDiagnosticProbeNamesARuntimeSession(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	live, ended, pending := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(live, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	directory.put(ended, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%5")
	directory.put(pending, runtimesession.StatePending, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%6")
	probe := func(id string) protocol.Response {
		return h.Handle(protocol.Request{Type: protocol.TypeDiagnosticProbe, RuntimeSessionID: id, WindowID: 77, TraceID: "trace", RequestID: "request-" + id})
	}
	requireAck := func(resp protocol.Response, status, reason string) *protocol.DeliveryAcknowledgement {
		t.Helper()
		if resp.Delivery == nil || resp.Delivery.Status != status || resp.Delivery.Reason != reason || resp.OK != (status == protocol.DeliveryAcknowledgementAccepted) {
			t.Fatalf("probe = %+v delivery %+v; want %s/%s", resp, resp.Delivery, status, reason)
		}
		if resp.Delivery.SourceWindowID != 0 || resp.Delivery.TargetWindowID != 0 {
			t.Fatalf("probe of a tmux session named window %d/%d", resp.Delivery.SourceWindowID, resp.Delivery.TargetWindowID)
		}
		return resp.Delivery
	}

	ack := requireAck(probe(string(live)), protocol.DeliveryAcknowledgementAccepted, "routing_discovered")
	if ack.RuntimeSessionID != string(live) || ack.WindowFound {
		t.Fatalf("ack = %+v; want the session named and no state held yet", ack)
	}
	if slot := h.runtimeSessionSlot(string(live)); slot != 0 {
		t.Fatalf("a probe claimed slot %d for a session that has not reported", slot)
	}

	requireAccepted(t, h.Handle(runtimeHook(t, live, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	requireAccepted(t, h.Handle(runtimeHook(t, ended, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-2"})))
	if ack := requireAck(probe(string(live)), protocol.DeliveryAcknowledgementAccepted, "routing_discovered"); !ack.WindowFound {
		t.Fatalf("ack = %+v; want the session's state found once it reported", ack)
	}
	_, icon, _ := slotState(h, h.runtimeSessionSlot(string(live)))
	if icon != "key" {
		t.Fatalf("icon after a probe = %q; a probe changes no state", icon)
	}

	endedSlot := h.runtimeSessionSlot(string(ended))
	directory.put(ended, runtimesession.StateStale, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%5")
	requireAck(probe(string(ended)), protocol.DeliveryAcknowledgementRejected, protocol.OriginReasonRuntimeSessionEnded)
	if got := h.runtimeSessionSlot(string(ended)); got != endedSlot || endedSlot == 0 {
		t.Fatalf("a probe released the ended session's slot: %d -> %d", endedSlot, got)
	}

	// The same session registered at another pane is a conflict a hook would
	// be refused for.
	directory.put(live, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-2", "%4")
	requireAck(probe(string(live)), protocol.DeliveryAcknowledgementRejected, protocol.OriginReasonTargetConflict)

	requireAck(probe(string(pending)), protocol.DeliveryAcknowledgementRejected, protocol.OriginReasonRuntimeSessionPending)
	requireAck(probe(string(newRuntimeID(t))), protocol.DeliveryAcknowledgementRejected, protocol.OriginReasonRuntimeSessionUnknown)
	requireAck(probe("7"), protocol.DeliveryAcknowledgementRejected, protocol.OriginReasonInvalidRuntimeSession)
}
