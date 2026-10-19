package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestDiagnosticProbeDiscoversRouteWithoutMutatingState(t *testing.T) {
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: unmanagedRouteWindows("api", harness.Codex, true)})

	resp := h.Handle(protocol.Request{
		Type: protocol.TypeDiagnosticProbe, WindowID: 43, Harness: harness.Claude,
		TraceID: "trace-1", RequestID: "request-1", Attempt: 1,
	})
	if !resp.OK || resp.Delivery == nil {
		t.Fatalf("probe response = %+v", resp)
	}
	ack := resp.Delivery
	if ack.Status != protocol.DeliveryAcknowledgementAccepted || ack.Reason != "routing_discovered" ||
		ack.TraceID != "trace-1" || ack.RequestID != "request-1" || ack.Attempt != 1 ||
		ack.ProducerHarness != harness.Claude || ack.TargetHarness != harness.Codex ||
		ack.Profile != "managed" || ack.SourceWindowID != 43 || ack.TargetWindowID != 42 ||
		ack.RouteDecision != events.RouteDecisionOwner || !ack.SnapshotReady || !ack.WindowFound {
		t.Fatalf("probe acknowledgement = %+v", ack)
	}

	h.stateMu.Lock()
	storeLen := len(h.store)
	metadataLen := len(h.windowMetadata)
	h.stateMu.Unlock()
	if storeLen != 0 || metadataLen != 0 {
		t.Fatalf("probe mutated state: store=%d metadata=%d", storeLen, metadataLen)
	}
}

func TestDiagnosticProbeDoesNotReserveManagedKittyOwner(t *testing.T) {
	h := newTestHandler()
	windows := unmanagedRouteWindows("api", harness.Codex, true)
	h.SetKittyClient(&fakeKittyClient{windows: windows})
	h.snapshots.Publish(probes.Publication{Windows: windows, Socket: kittyB, CapturedAt: time.Now()})
	h.stateMu.Lock()
	// Any existing route makes the mapping path inspect managed owners. The
	// owner in Kitty B has not claimed its own slot yet.
	h.kittySlotLocked(kittyA, 7)
	before := len(h.runtimeRoutes.kittyByKey)
	h.stateMu.Unlock()
	resp := h.Handle(protocol.Request{Type: protocol.TypeDiagnosticProbe, WindowID: 43, KittyListenOn: kittyB, Harness: harness.Claude})
	if !resp.OK || resp.Delivery == nil || !resp.Delivery.WindowFound {
		t.Fatalf("probe response = %+v", resp)
	}
	h.stateMu.Lock()
	after := len(h.runtimeRoutes.kittyByKey)
	h.stateMu.Unlock()
	if after != before {
		t.Fatalf("probe reserved Kitty slots: before=%d after=%d", before, after)
	}
}

func TestHookDeliveryAcknowledgesUnknownEvidenceWithoutState(t *testing.T) {
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: unmanagedRouteWindows("api", harness.Codex, true)})

	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 43, Event: "FutureEvent", Harness: harness.Claude,
		ProducerHarness: harness.Claude, PayloadJSON: `{}`, TraceID: "trace-2",
		RequestID: "request-2", SourceEventID: "source-2", Attempt: 2,
	})
	if !resp.OK || resp.Delivery == nil || resp.Delivery.Status != protocol.DeliveryAcknowledgementRejected ||
		resp.Delivery.Reason != suppressionReasonUnknownEvent {
		t.Fatalf("unknown response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 1 || !detail.Events[0].Suppressed ||
		detail.Events[0].SuppressionReason != suppressionReasonUnknownEvent {
		t.Fatalf("unknown evidence = %+v", detail.Events)
	}
}

func TestHookDeliveryRejectsMissingWindow(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 0, Event: "Stop", Harness: harness.Claude,
		PayloadJSON: `{}`, TraceID: "trace-missing-window", RequestID: "request-missing-window",
	})
	if !resp.OK || resp.Delivery == nil || resp.Delivery.Status != protocol.DeliveryAcknowledgementRejected ||
		resp.Delivery.Reason != "invalid_window" {
		t.Fatalf("missing-window response = %+v", resp)
	}
	if resp.Delivery.SourceWindowID != 0 || resp.Delivery.TargetWindowID != 0 {
		t.Fatalf("missing-window acknowledgement = %+v", resp.Delivery)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if len(h.store) != 0 || len(h.windowMetadata) != 0 {
		t.Fatalf("missing-window delivery mutated state: store=%d metadata=%d", len(h.store), len(h.windowMetadata))
	}
}
