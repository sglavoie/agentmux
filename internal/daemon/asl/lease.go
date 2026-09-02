package asl

// Lease reconciliation evidence.
//
// Lifecycle hook events are the fast path for per-run ASL facts, but events can
// be lost (daemon restart, crashed loop, dropped hook) and hints are fail-open
// by design. Reconciliation closes the gap by comparing a window's live ASL
// leases against the atomic `agent_mux` projection agent-session-loop publishes
// in its durable state. What one sweep established about one lease is decided
// here; what the daemon then does about it is a reducer mutation and stays with
// the reducer.

import (
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

const (
	// ReconcileInterval is the periodic reconciliation cadence. Two consecutive
	// confirmations are required before a lease is retired, so this also sets
	// the retirement floor: two intervals plus one scan, comfortably inside the
	// thirty seconds the contract promises.
	ReconcileInterval = 10 * time.Second
	// MissingRunThreshold is how many consecutive *confirmed* absences must
	// accumulate before a lease is released.
	MissingRunThreshold = 2
)

// LeaseRef identifies one window's ASL lease fact for miss-streak tracking and
// for the adjacent supervision bookkeeping keyed by the same run.
type LeaseRef struct {
	WindowID int
	Key      string
}

// Evidence is what one sweep established about one existing lease.
type Evidence int

const (
	// EvidenceIndeterminate is the default so that any path that forgets to
	// decide preserves the lease rather than retiring it.
	EvidenceIndeterminate Evidence = iota
	EvidencePresent
	EvidenceAbsent
)

// Sweep is one discovery result indexed for lease evidence. Building it once
// per sweep keeps the two lookup maps out of the reconciliation loop's
// signature and out of this package's exported surface.
type Sweep struct {
	result     agentloop.DiscoveryResult
	byRunID    map[string]agentloop.PlanObservation
	byStateDir map[string]agentloop.PlanObservation
}

// NewSweep indexes one discovery result by run ID and by plan state directory.
func NewSweep(result agentloop.DiscoveryResult) Sweep {
	byStateDir := make(map[string]agentloop.PlanObservation, len(result.Observations))
	for _, observation := range result.Observations {
		if observation.StateDir != "" {
			byStateDir[observation.StateDir] = observation
		}
	}
	return Sweep{
		result:     result,
		byRunID:    result.PresenceByRunID(),
		byStateDir: byStateDir,
	}
}

// Evidence decides what this sweep says about one live lease.
//
// The lease's own run ID is the primary key. When the sweep has nothing under
// that run ID, the question becomes "what happened to the plan this lease came
// from", and leaseStateDir — the state directory of the supervision target
// registered for this lease, empty when none is — answers it: that is what
// keeps a single corrupt plan from either retiring its own lease or, worse,
// holding every other plan's lease hostage to its unreadability.
func (s Sweep) Evidence(leaseStateDir string, fact protocol.SourceFactSnapshot) Evidence {
	if fact.RunID != "" {
		if observation, ok := s.byRunID[fact.RunID]; ok {
			switch observation.Presence {
			case agentloop.PresencePresent:
				return EvidencePresent
			case agentloop.PresenceConfirmedAbsent:
				return EvidenceAbsent
			default:
				return EvidenceIndeterminate
			}
		}
	}

	// The run is not in this sweep at all. Locate the plan it belonged to.
	if leaseStateDir != "" {
		observation, ok := s.byStateDir[leaseStateDir]
		if !ok {
			// The plan's state directory is gone. ASL removes it when the run's
			// record is torn down, so this is a real absence.
			return EvidenceAbsent
		}
		if observation.Presence == agentloop.PresenceIndeterminate {
			return EvidenceIndeterminate
		}
		// The plan is readable and does not describe this run: either it
		// finished, or the plan has since restarted under a new run ID. The
		// lease's run is over either way.
		return EvidenceAbsent
	}

	// No plan association for this lease — it was established by live events
	// before any sweep saw the plan. There is nothing to correlate against, so
	// only a sweep that positively enumerated the state root and read every plan
	// in it may be read as absence. A state root that does not exist is a
	// pairing or configuration problem, and reading it as "no runs anywhere"
	// would retire every lease on the machine at once.
	if !s.result.RootPresent || s.result.HasIndeterminate() {
		return EvidenceIndeterminate
	}
	return EvidenceAbsent
}

// MissStreaks counts, per lease, how many consecutive sweeps positively
// confirmed the run gone. Only a streak that reaches MissingRunThreshold
// releases a lease; indeterminate evidence neither advances nor resets one, so
// an unreadable plan can never retire a healthy run.
//
// Like the activity trackers this is deliberately CALLER-LOCKED and zero-value
// ready: the daemon advances a streak in the same stateMu critical section that
// mutates the reducer state the streak is about to settle, so an internal mutex
// would hide an atomicity requirement rather than satisfy it.
type MissStreaks struct {
	streaks map[LeaseRef]int
}

// Advance records one confirmed absence and reports the new streak length.
func (m *MissStreaks) Advance(ref LeaseRef) int {
	if m.streaks == nil {
		m.streaks = make(map[LeaseRef]int)
	}
	m.streaks[ref]++
	return m.streaks[ref]
}

// Streak reports how many consecutive absences this lease has accumulated. A
// recorded streak is always at least one, so zero and absent are the same
// answer.
func (m *MissStreaks) Streak(ref LeaseRef) int {
	return m.streaks[ref]
}

// Clear forgets one lease's streak, which is what present evidence and a fresh
// seed both do.
func (m *MissStreaks) Clear(ref LeaseRef) {
	delete(m.streaks, ref)
}

// Retain drops every streak whose lease keep reports as no longer live. Streaks
// only make sense for leases that still exist, so a closed window or an already
// released lease cannot leave the map growing.
func (m *MissStreaks) Retain(keep func(LeaseRef) bool) {
	for ref := range m.streaks {
		if !keep(ref) {
			delete(m.streaks, ref)
		}
	}
}

// Len reports how many leases currently carry a streak.
func (m *MissStreaks) Len() int {
	return len(m.streaks)
}
