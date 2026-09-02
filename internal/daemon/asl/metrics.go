package asl

// Pairing observability, in both directions.
//
// Outbound, presence is derived from evidence that can be missing, and missing
// evidence is deliberately silent: it preserves leases and changes nothing.
// That is the safe behavior, but it is also invisible — a permanently
// unreadable plan and a perfectly healthy one look identical from the outside.
//
// Inbound, lifecycle hooks arrive on stdin from processes the daemon never
// spawned, and the only correct response to one it cannot place is to
// quarantine it and answer OK. That silence is deliberate too — a hook producer
// must never be blocked on the daemon's opinion — but it means a pairing that
// is quietly refusing every event looks exactly like an idle one.
//
// These counters are what make both differences legible, so an operator can
// tell "nothing is happening because nothing needs to" from "nothing is
// happening because AgentMux cannot read the peer". Both are CALLER-LOCKED and
// zero-value ready, like the trackers they accompany.

import (
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// ReconcileMetrics accumulates reconciliation outcomes.
type ReconcileMetrics struct {
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

// RecordSweep folds one successful sweep's classification counts into the
// metrics.
func (m *ReconcileMetrics) RecordSweep(now time.Time, result agentloop.DiscoveryResult) {
	if !m.lastSweepAt.IsZero() {
		if gap := now.Sub(m.lastSweepAt); gap > m.maxSweepGap {
			m.maxSweepGap = gap
		}
	}
	m.lastSweepAt = now
	m.sweeps++

	present, absent, indeterminate := result.Counts()
	m.lastPresent = present
	m.lastAbsent = absent
	m.lastIndeterminate = indeterminate

	for _, observation := range result.Observations {
		if observation.ContractSetID != "" {
			m.lastContractSetID = observation.ContractSetID
		}
		switch observation.Reason {
		case agentloop.ReasonInvalidProjection, agentloop.ReasonMisplacedState:
			m.invalidProjections++
		case agentloop.ReasonUnsupportedContract:
			m.unsupportedContracts++
		case agentloop.ReasonUnreadableState:
			m.unreadableStates++
		case agentloop.ReasonMissingProjection:
			m.missingProjections++
		case agentloop.ReasonProcessUnverified, agentloop.ReasonProcessReplaced:
			m.processUnverified++
		}
	}
}

// RecordSweepFailure counts a sweep that could not run at all. The state root
// itself was unreadable, which says nothing about any individual plan, so no
// per-plan counter moves and no lease is touched.
func (m *ReconcileMetrics) RecordSweepFailure() {
	m.sweepErrors++
}

// RecordStaleObservation counts one discovered position older than one already
// folded in for the same run.
func (m *ReconcileMetrics) RecordStaleObservation() {
	m.staleObservations++
}

// RecordIndeterminateLease counts one live lease this sweep could not decide.
func (m *ReconcileMetrics) RecordIndeterminateLease() {
	m.indeterminateLeases++
}

// RecordConfirmedMiss counts one live lease this sweep positively confirmed
// gone. It takes more than one to retire the lease; see MissingRunThreshold.
func (m *ReconcileMetrics) RecordConfirmedMiss() {
	m.confirmedMisses++
}

// RecordRetiredLease counts one lease released by consecutive confirmed
// absences.
func (m *ReconcileMetrics) RecordRetiredLease() {
	m.retiredLeases++
}

// Snapshot renders the reconciliation block published in health. Ages are
// computed at read time: a daemon that stopped sweeping also stopped writing,
// so a stored lag would freeze at the last thing that worked. stateRoot falls
// back to the XDG-derived default, which is the root a daemon that was never
// paired is in fact reading.
func (m *ReconcileMetrics) Snapshot(stateRoot string, now time.Time) protocol.ASLReconcileSnapshot {
	snapshot := protocol.ASLReconcileSnapshot{
		StateRoot:            stateRoot,
		MaxGapSeconds:        m.maxSweepGap.Seconds(),
		IntervalSeconds:      ReconcileInterval.Seconds(),
		Sweeps:               m.sweeps,
		SweepErrors:          m.sweepErrors,
		PlansPresent:         m.lastPresent,
		PlansAbsent:          m.lastAbsent,
		PlansIndeterminate:   m.lastIndeterminate,
		InvalidProjections:   m.invalidProjections,
		UnsupportedContracts: m.unsupportedContracts,
		UnreadableStates:     m.unreadableStates,
		MissingProjections:   m.missingProjections,
		ProcessUnverified:    m.processUnverified,
		ConfirmedMisses:      m.confirmedMisses,
		IndeterminateLeases:  m.indeterminateLeases,
		RetiredLeases:        m.retiredLeases,
		StaleObservations:    m.staleObservations,
		ContractSetID:        m.lastContractSetID,
	}
	if snapshot.StateRoot == "" {
		snapshot.StateRoot = agentloop.DefaultStateRoot()
	}
	if !m.lastSweepAt.IsZero() {
		snapshot.LagSeconds = now.Sub(m.lastSweepAt).Seconds()
	}
	return snapshot
}

// HookDeliveryMetrics accumulates lifecycle-hook ingestion outcomes.
//
// These are delivery facts only: how many lifecycle payloads were believed, how
// many were refused and under which daemon-authored reason, and when each last
// happened. Nothing a payload chose is recorded — not its offered version, not
// its contract identity, not a plan path — because a quarantined payload is
// precisely one whose self-description the daemon has declined to believe.
type HookDeliveryMetrics struct {
	accepted       uint64
	lastAcceptedAt time.Time

	quarantined       uint64
	byReason          map[string]uint64
	lastQuarantinedAt time.Time
	// lastReason is the most recent refusal reason. It is a daemon constant
	// from the events.IsQuarantineReason set, never producer-supplied text.
	lastReason string
}

// RecordAccepted counts one lifecycle payload the daemon believed and acted on.
func (m *HookDeliveryMetrics) RecordAccepted(now time.Time) {
	m.accepted++
	m.lastAcceptedAt = now
}

// RecordQuarantined counts one lifecycle payload refused as unusable. reason
// must be a daemon-authored constant; anything else is dropped rather than
// keyed, so the reason map cannot be grown by a producer.
func (m *HookDeliveryMetrics) RecordQuarantined(now time.Time, reason string) {
	if !events.IsQuarantineReason(reason) {
		return
	}
	m.quarantined++
	m.lastQuarantinedAt = now
	m.lastReason = reason
	if m.byReason == nil {
		m.byReason = make(map[string]uint64, 3)
	}
	m.byReason[reason]++
}

// Snapshot renders the ingestion block published in health. An empty by-reason
// breakdown is omitted rather than published: "no refusals" and "refusals this
// build does not classify" are different facts.
func (m *HookDeliveryMetrics) Snapshot(now time.Time) protocol.ASLHookDeliverySnapshot {
	snapshot := protocol.ASLHookDeliverySnapshot{
		Accepted:             m.accepted,
		Quarantined:          m.quarantined,
		LastQuarantineReason: m.lastReason,
	}
	if len(m.byReason) > 0 {
		snapshot.QuarantinedByReason = make(map[string]uint64, len(m.byReason))
		for reason, count := range m.byReason {
			snapshot.QuarantinedByReason[reason] = count
		}
	}
	if !m.lastAcceptedAt.IsZero() {
		age := now.Sub(m.lastAcceptedAt).Seconds()
		snapshot.AcceptedAgeSeconds = &age
	}
	if !m.lastQuarantinedAt.IsZero() {
		age := now.Sub(m.lastQuarantinedAt).Seconds()
		snapshot.QuarantinedAgeSeconds = &age
	}
	return snapshot
}
