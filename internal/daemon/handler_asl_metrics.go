package daemon

// The Handler's ASL observability wrappers. The counters themselves live with
// the domain in internal/daemon/asl; what belongs here is the locking, and the
// state root the reconciliation block is reported against.

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// recordASLScanFailure counts a sweep that could not run at all.
func (h *Handler) recordASLScanFailure(_ time.Time, _ error) {
	h.stateMu.Lock()
	h.aslMetrics.RecordSweepFailure()
	h.stateMu.Unlock()
}

// ASLReconcileMetrics returns the current reconciliation snapshot.
func (h *Handler) ASLReconcileMetrics() protocol.ASLReconcileSnapshot {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.aslMetrics.Snapshot(h.aslStateRootValue, time.Now())
}

// SetASLStateRoot installs the canonical agent-session-loop state root. Pairing
// persists this so both sides agree on one location; an empty value keeps the
// XDG-derived default, which is only correct when neither side has been moved.
func (h *Handler) SetASLStateRoot(root string) {
	h.stateMu.Lock()
	h.aslStateRootValue = root
	h.stateMu.Unlock()
}

func (h *Handler) aslStateRoot() string {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.aslStateRootValue
}
