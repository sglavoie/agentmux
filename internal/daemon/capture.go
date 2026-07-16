package daemon

import (
	"context"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const captureMaintenanceInterval = time.Minute

func (h *Handler) setCaptureStore(store *diagnostics.CaptureStore) {
	if h != nil {
		h.captureStore = store
	}
}

func (h *Handler) captureHookPayload(request Request) {
	if h == nil || h.captureStore == nil || request.PayloadCaptured || request.PayloadJSON == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = h.captureStore.Record(ctx, diagnostics.CapturePayloadRequest{
		WindowID: request.WindowID, Harness: request.ProducerHarness, Event: request.Event,
		TraceID: request.TraceID, RequestID: request.RequestID, Payload: []byte(request.PayloadJSON),
	})
}

func (h *Handler) maintainCaptureStore(now time.Time) {
	if h == nil || h.captureStore == nil {
		return
	}
	h.captureMaintenanceMu.Lock()
	if !h.lastCaptureMaintenanceAt.IsZero() && !now.Before(h.lastCaptureMaintenanceAt) && now.Sub(h.lastCaptureMaintenanceAt) < captureMaintenanceInterval {
		h.captureMaintenanceMu.Unlock()
		return
	}
	h.lastCaptureMaintenanceAt = now
	h.captureMaintenanceMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, _ = h.captureStore.Maintain(ctx)
}
