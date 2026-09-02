package daemon

// Reconciliation observability.
//
// Presence is now derived from evidence that can be missing, and missing
// evidence is deliberately silent: it preserves leases and changes nothing.
// That is the safe behavior, but it is also invisible — a permanently
// unreadable plan and a perfectly healthy one look identical from the outside.
// These counters are what make the difference legible, so an operator can tell
// "nothing is happening because nothing needs to" from "nothing is happening
// because AgentMux cannot read the peer".

import (
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// aslRunPosition is the furthest position AgentMux has folded in for one run.
type aslRunPosition struct {
	generation uint64
	sequence   uint64
}

// aslReconcileMetrics accumulates reconciliation outcomes. Guarded by stateMu.
type aslReconcileMetrics struct {
	// lastSweepAt is when the most recent successful sweep completed, and
	// maxSweepGap the widest interval ever observed between two of them.
	// Together they answer the only question that matters about cadence: is
	// reconciliation actually running as often as it claims to.
	lastSweepAt time.Time
	maxSweepGap time.Duration
	sweeps      uint64
	sweepErrors uint64

	// Per-plan classification counts from the most recent sweep, plus lifetime
	// totals for the failure modes that need operator action. An invalid
	// projection is a corrupt or partially written state file; an unsupported
	// contract is a peer speaking a contract set this build does not, which is
	// a pairing problem rather than damage.
	lastPresent       int
	lastAbsent        int
	lastIndeterminate int

	invalidProjections   uint64
	unsupportedContracts uint64
	unreadableStates     uint64
	missingProjections   uint64
	processUnverified    uint64

	// Lease-level outcomes.
	confirmedMisses     uint64
	indeterminateLeases uint64
	retiredLeases       uint64
	staleObservations   uint64

	// lastContractSetID is the contract set the peer most recently published,
	// reported verbatim so a mismatch can be diagnosed against ASL's own
	// identifier rather than guessed at.
	lastContractSetID string
}

// recordASLScanLocked folds one successful sweep's classification counts into
// the metrics. Callers hold stateMu.
func (h *Handler) recordASLScanLocked(now time.Time, result agentloop.DiscoveryResult) {
	if !h.aslMetrics.lastSweepAt.IsZero() {
		if gap := now.Sub(h.aslMetrics.lastSweepAt); gap > h.aslMetrics.maxSweepGap {
			h.aslMetrics.maxSweepGap = gap
		}
	}
	h.aslMetrics.lastSweepAt = now
	h.aslMetrics.sweeps++

	present, absent, indeterminate := result.Counts()
	h.aslMetrics.lastPresent = present
	h.aslMetrics.lastAbsent = absent
	h.aslMetrics.lastIndeterminate = indeterminate

	for _, observation := range result.Observations {
		if observation.ContractSetID != "" {
			h.aslMetrics.lastContractSetID = observation.ContractSetID
		}
		switch observation.Reason {
		case agentloop.ReasonInvalidProjection, agentloop.ReasonMisplacedState:
			h.aslMetrics.invalidProjections++
		case agentloop.ReasonUnsupportedContract:
			h.aslMetrics.unsupportedContracts++
		case agentloop.ReasonUnreadableState:
			h.aslMetrics.unreadableStates++
		case agentloop.ReasonMissingProjection:
			h.aslMetrics.missingProjections++
		case agentloop.ReasonProcessUnverified, agentloop.ReasonProcessReplaced:
			h.aslMetrics.processUnverified++
		}
	}
}

// recordASLScanFailure counts a sweep that could not run at all. The state root
// itself was unreadable, which says nothing about any individual plan, so no
// per-plan counter moves and no lease is touched.
func (h *Handler) recordASLScanFailure(_ time.Time, _ error) {
	h.stateMu.Lock()
	h.aslMetrics.sweepErrors++
	h.stateMu.Unlock()
}

// ASLReconcileMetrics returns the current reconciliation snapshot.
func (h *Handler) ASLReconcileMetrics() protocol.ASLReconcileSnapshot {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	snapshot := protocol.ASLReconcileSnapshot{
		StateRoot:            h.aslStateRootValue,
		MaxGapSeconds:        h.aslMetrics.maxSweepGap.Seconds(),
		IntervalSeconds:      aslReconcileInterval.Seconds(),
		Sweeps:               h.aslMetrics.sweeps,
		SweepErrors:          h.aslMetrics.sweepErrors,
		PlansPresent:         h.aslMetrics.lastPresent,
		PlansAbsent:          h.aslMetrics.lastAbsent,
		PlansIndeterminate:   h.aslMetrics.lastIndeterminate,
		InvalidProjections:   h.aslMetrics.invalidProjections,
		UnsupportedContracts: h.aslMetrics.unsupportedContracts,
		UnreadableStates:     h.aslMetrics.unreadableStates,
		MissingProjections:   h.aslMetrics.missingProjections,
		ProcessUnverified:    h.aslMetrics.processUnverified,
		ConfirmedMisses:      h.aslMetrics.confirmedMisses,
		IndeterminateLeases:  h.aslMetrics.indeterminateLeases,
		RetiredLeases:        h.aslMetrics.retiredLeases,
		StaleObservations:    h.aslMetrics.staleObservations,
		ContractSetID:        h.aslMetrics.lastContractSetID,
	}
	if snapshot.StateRoot == "" {
		snapshot.StateRoot = agentloop.DefaultStateRoot()
	}
	if !h.aslMetrics.lastSweepAt.IsZero() {
		snapshot.LagSeconds = time.Since(h.aslMetrics.lastSweepAt).Seconds()
	}
	return snapshot
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
