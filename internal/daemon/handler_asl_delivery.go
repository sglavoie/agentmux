package daemon

// The Handler's lifecycle-hook delivery wrappers. The counters live with the
// domain in internal/daemon/asl; the locking is what belongs here.

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// recordASLHookAccepted counts one lifecycle payload the daemon believed and
// acted on.
func (h *Handler) recordASLHookAccepted(now time.Time) {
	h.stateMu.Lock()
	h.aslDelivery.RecordAccepted(now)
	h.stateMu.Unlock()
}

// recordASLHookQuarantined counts one lifecycle payload refused as unusable.
func (h *Handler) recordASLHookQuarantined(now time.Time, reason string) {
	h.stateMu.Lock()
	h.aslDelivery.RecordQuarantined(now, reason)
	h.stateMu.Unlock()
}

// ASLHookDeliveryMetrics returns the current ingestion snapshot.
func (h *Handler) ASLHookDeliveryMetrics() protocol.ASLHookDeliverySnapshot {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.aslDelivery.Snapshot(time.Now())
}
