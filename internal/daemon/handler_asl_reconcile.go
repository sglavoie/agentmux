package daemon

// ASL lease reconciliation. Lifecycle hook events are the fast path for
// per-run ASL facts, but events can be lost (daemon restart, crashed loop,
// dropped hook) and hints are fail-open by design. Reconciliation closes the
// gap: it compares every window's live ASL leases against the atomic `agent_mux`
// projection agent-session-loop publishes in its durable state, and converges
// the facts.
//
//   - A discovered run with no live lease is seeded onto its managed owner
//     window with the phase the projection reports, so a restart or a lost
//     start/turn event still produces 🔁 (and ⚡ for an executing turn).
//   - A live lease whose run is *confirmed absent* is converted to a finished
//     fact — the same state a received finish event would have produced — but
//     only after two consecutive confirmations, so one racy scan can never
//     revoke a healthy lease.
//   - Indeterminate evidence changes nothing at all. It neither advances a miss
//     streak nor resets one, because "I could not read this plan" is not
//     evidence in either direction, and treating it as absence is exactly how a
//     corrupt file or an unpaired ASL would retire a healthy run's lease.
//
// It runs immediately at daemon startup and every ten seconds afterwards, plus
// immediately after a finish event that carried no run ID.

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const (
	// aslReconcileInterval is the periodic reconciliation cadence. Two
	// consecutive confirmations are required before a lease is retired, so this
	// also sets the retirement floor: two intervals plus one scan, comfortably
	// inside the thirty seconds the contract promises.
	aslReconcileInterval = 10 * time.Second
	// aslMissingRunThreshold is how many consecutive *confirmed* absences must
	// accumulate before a lease is released.
	aslMissingRunThreshold = 2
)

// aslLeaseRef identifies one window's ASL lease fact for miss-streak tracking.
type aslLeaseRef struct {
	windowID int
	key      string
}

// aslEvidence is what one sweep established about one existing lease.
type aslEvidence int

const (
	// aslEvidenceIndeterminate is the default so that any path that forgets to
	// decide preserves the lease rather than retiring it.
	aslEvidenceIndeterminate aslEvidence = iota
	aslEvidencePresent
	aslEvidenceAbsent
)

// maybeReconcileASLLeases runs reconciliation when the interval has elapsed
// (or has never run). Called from the maintenance tick with the fresh
// snapshot.
func (h *Handler) maybeReconcileASLLeases(now time.Time, windows []kitty.OSWindow) {
	h.stateMu.Lock()
	due := h.lastASLReconcileAt.IsZero() || now.Sub(h.lastASLReconcileAt) >= aslReconcileInterval
	h.stateMu.Unlock()
	if due {
		h.ReconcileASLLeases(now, windows)
	}
}

// ReconcileASLLeases reconciles immediately, bypassing the interval gate.
// Discovery runs outside stateMu so a slow filesystem scan cannot block state
// handlers. An empty snapshot skips reconciliation entirely: without windows,
// candidates cannot be attributed to owners, and missing evidence must
// preserve leases rather than revoke them.
func (h *Handler) ReconcileASLLeases(now time.Time, windows []kitty.OSWindow) {
	h.stateMu.Lock()
	h.lastASLReconcileAt = now
	h.stateMu.Unlock()

	if len(windows) == 0 {
		return
	}
	// Paused runs are included: a pause finalizes the loop process, so the
	// default liveness checks would hide a run that is deliberately holding an
	// attention icon + 🔁 while it waits for a human.
	result, err := daemonLoopDiscoverPlans(agentloop.DiscoverOptions{
		StateRoot:     h.aslStateRoot(),
		IncludePaused: true,
		Cache:         h.aslProjectionCache,
	})
	if err != nil {
		h.recordASLScanFailure(now, err)
		slog.Debug("asl reconcile skipped: discovery failed; preserving leases", "err", err)
		return
	}
	h.reconcileASLDiscovery(now, windows, result)
}

// aslSeed is one discovered run attributed to its managed owner window.
type aslSeed struct {
	windowID  int
	key       string
	candidate agentloop.Candidate
}

// reconcileASLDiscovery folds one successful sweep into the per-window
// reducers.
func (h *Handler) reconcileASLDiscovery(now time.Time, windows []kitty.OSWindow, result agentloop.DiscoveryResult) {
	byRunID := result.PresenceByRunID()
	byStateDir := make(map[string]agentloop.PlanObservation, len(result.Observations))
	for _, observation := range result.Observations {
		if observation.StateDir != "" {
			byStateDir[observation.StateDir] = observation
		}
	}

	// Attribute each present candidate to the managed owner window(s) of its
	// workspace, reusing the exact matching policy lifecycle fan-out uses so
	// reconciliation and live events can never disagree about ownership.
	var seeds []aslSeed
	for _, candidate := range result.Candidates(true) {
		// Leases are keyed by run identity only. The pre-identity window-keyed
		// key is gone with the legacy parser: the projection always carries a
		// run ID, and a window-keyed lease cannot be matched against evidence,
		// so seeding one would create a fact nothing could ever retire.
		if candidate.RunID == "" {
			continue
		}
		targets, _ := matchManagedLoopTargets(windows, []agentloop.Candidate{candidate})
		for _, target := range targets {
			seeds = append(seeds, aslSeed{windowID: target.windowID, key: candidate.RunID, candidate: candidate})
		}
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	h.recordASLScanLocked(now, result)
	changedWindows := make(map[int]bool)

	for _, seed := range seeds {
		// Reject a scan that describes an older position for this run than one
		// already folded in. Two independent counters make that decidable: the
		// projection generation advances on every durable transition, and the
		// lifecycle source sequence advances on every notification. A read that
		// went backwards on either is a stale view — a reordered filesystem
		// read, or a scan that raced a write — and acting on it would resurrect
		// a position the run has already left.
		if !h.acceptASLObservationLocked(seed.candidate.RunID, seed.candidate.Generation, seed.candidate.SourceSequence) {
			h.aslMetrics.staleObservations++
			continue
		}
		h.registerASLSupervisorTargetLocked(aslSupervisorTarget{
			windowID:       seed.windowID,
			originWindowID: seed.candidate.WindowID,
			candidate:      seed.candidate,
		})
		if h.seedASLLeaseLocked(now, seed) {
			changedWindows[seed.windowID] = true
		}
		delete(h.aslMissStreak, aslLeaseRef{windowID: seed.windowID, key: seed.key})
	}

	// Two-miss removal: a live lease that two consecutive sweeps positively
	// confirmed gone converges to the finished state its lost finish event
	// would have produced.
	liveRefs := make(map[aslLeaseRef]bool)
	for windowID, st := range h.store {
		if st.Reducer == nil {
			continue
		}
		for _, fact := range st.Reducer.Facts() {
			if fact.Kind != protocol.SourceASL || !fact.LeaseLive {
				continue
			}
			ref := aslLeaseRef{windowID: windowID, key: fact.Key}
			switch h.aslLeaseEvidenceLocked(result, byRunID, byStateDir, ref, fact) {
			case aslEvidencePresent:
				delete(h.aslMissStreak, ref)
				continue
			case aslEvidenceIndeterminate:
				// Hold the streak where it is. Resetting it would let a plan
				// that alternates unreadable and gone never retire; advancing
				// it would let an unreadable plan retire a healthy run.
				h.aslMetrics.indeterminateLeases++
				if h.aslMissStreak[ref] > 0 {
					liveRefs[ref] = true
				}
				continue
			}

			h.aslMetrics.confirmedMisses++
			h.aslMissStreak[ref]++
			if h.aslMissStreak[ref] < aslMissingRunThreshold {
				liveRefs[ref] = true
				continue
			}
			delete(h.aslMissStreak, ref)
			delete(h.aslSupervisorTargets, ref)
			delete(h.aslSupervisorClosureCutoffs, ref)
			h.clearASLSupervisorQuestionLocked(
				windowID,
				fact.RunID,
				fact.Seq+1,
				now,
				"ASL run missing from consecutive discoveries; supervisor question projection retired",
			)
			st.ShadowSeq++
			st.Reducer.Apply(protocol.ReducerEvent{
				Source:         protocol.SourceID{Kind: protocol.SourceASL, Key: fact.Key},
				Seq:            st.ShadowSeq,
				At:             now.Unix(),
				OriginWindowID: fact.OriginWindowID,
				RunID:          fact.RunID,
				Phase:          state.ASLPhaseFinished,
			})
			changedWindows[windowID] = true
			h.aslMetrics.retiredLeases++
			h.appendWatchdogEventLocked(windowID, fact.OriginWindowID, now, eventASLReconciled, "lease_released",
				string(protocol.SourceASL)+":"+fact.Key,
				"run "+fact.RunID+" confirmed absent by consecutive discoveries; lease released")
			slog.Debug("asl reconcile released confirmed-absent run lease",
				"window_id", windowID,
				"run_id", fact.RunID,
				"key", fact.Key,
			)
		}
	}
	// Miss streaks only make sense for leases that still exist; drop stale
	// entries so a closed window or an already-released lease cannot leave the
	// map growing.
	for ref := range h.aslMissStreak {
		if !liveRefs[ref] {
			delete(h.aslMissStreak, ref)
		}
	}

	for windowID := range changedWindows {
		st := h.store[windowID]
		if st == nil || st.Reducer == nil {
			continue
		}
		h.publishEffectiveLocked(windowID, st, st.Reducer.Reduce(now.Unix()))
		h.syncRendererMetadataLocked(windowID)
		h.markWindowDirtyLocked(windowID)
	}
}

// aslLeaseEvidenceLocked decides what one sweep says about one live lease.
//
// The lease's own run ID is the primary key. When the sweep has nothing under
// that run ID, the question becomes "what happened to the plan this lease came
// from", and the registered supervisor target answers it: that is what keeps a
// single corrupt plan from either retiring its own lease or, worse, holding
// every other plan's lease hostage to its unreadability.
func (h *Handler) aslLeaseEvidenceLocked(
	result agentloop.DiscoveryResult,
	byRunID map[string]agentloop.PlanObservation,
	byStateDir map[string]agentloop.PlanObservation,
	ref aslLeaseRef,
	fact protocol.SourceFactSnapshot,
) aslEvidence {
	if fact.RunID != "" {
		if observation, ok := byRunID[fact.RunID]; ok {
			switch observation.Presence {
			case agentloop.PresencePresent:
				return aslEvidencePresent
			case agentloop.PresenceConfirmedAbsent:
				return aslEvidenceAbsent
			default:
				return aslEvidenceIndeterminate
			}
		}
	}

	// The run is not in this sweep at all. Locate the plan it belonged to.
	if stateDir := h.aslSupervisorTargets[ref].candidate.StateDir; stateDir != "" {
		observation, ok := byStateDir[stateDir]
		if !ok {
			// The plan's state directory is gone. ASL removes it when the run's
			// record is torn down, so this is a real absence.
			return aslEvidenceAbsent
		}
		if observation.Presence == agentloop.PresenceIndeterminate {
			return aslEvidenceIndeterminate
		}
		// The plan is readable and does not describe this run: either it
		// finished, or the plan has since restarted under a new run ID. The
		// lease's run is over either way.
		return aslEvidenceAbsent
	}

	// No plan association for this lease — it was established by live events
	// before any sweep saw the plan. There is nothing to correlate against, so
	// only a sweep that positively enumerated the state root and read every plan
	// in it may be read as absence. A state root that does not exist is a
	// pairing or configuration problem, and reading it as "no runs anywhere"
	// would retire every lease on the machine at once.
	if !result.RootPresent || result.HasIndeterminate() {
		return aslEvidenceIndeterminate
	}
	return aslEvidenceAbsent
}

// acceptASLObservationLocked enforces per-run monotonicity across sweeps and
// records the accepted position. Both counters are monotonic at the source, so
// a decrease in either can only mean this read is older than one already
// applied.
func (h *Handler) acceptASLObservationLocked(runID string, generation, sequence uint64) bool {
	if runID == "" {
		return false
	}
	seen, ok := h.aslRunPosition[runID]
	if ok && (generation < seen.generation || (generation == seen.generation && sequence < seen.sequence)) {
		return false
	}
	h.aslRunPosition[runID] = aslRunPosition{generation: generation, sequence: sequence}
	return true
}

// seedASLLeaseLocked asserts one discovered run's lease on its owner window.
// Callers hold stateMu. Reports whether the window's facts changed.
//
//   - A run with no fact is seeded at sequence 0 with the projection's phase,
//     so the run's own future events (any positive sequence) always supersede
//     the seed.
//   - A live lease is left alone — live events own it.
//   - A released lease is NOT resurrected: a finish that named this exact run
//     outranks a discovery scan that may have raced it.
//   - Paused runs seed a bare lease (phase only, no attention action): 🔁
//     survives a restart, but a pause prompt the user may have already seen is
//     not re-alerted from disk.
//   - A waiting run also seeds a bare lease. Discovery proves the worker is
//     still blocked, but only a current supervisor projection correlated with
//     an interactive handoff may re-assert the human-actionable question icon.
func (h *Handler) seedASLLeaseLocked(now time.Time, seed aslSeed) bool {
	st := h.ensureWindowStateLocked(seed.windowID)
	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}
	exists, live := st.Reducer.LeaseState(seed.key)
	if live || exists {
		return false
	}
	phase := aslPhaseForDiscovered(seed.candidate.Phase)
	originWindowID := seed.candidate.WindowID
	if originWindowID <= 0 {
		originWindowID = seed.windowID
	}
	decision := st.Reducer.Apply(protocol.ReducerEvent{
		Source:         protocol.SourceID{Kind: protocol.SourceASL, Key: seed.key},
		Seq:            0,
		At:             now.Unix(),
		OriginWindowID: originWindowID,
		RunID:          seed.candidate.RunID,
		Phase:          phase,
	})
	if decision != protocol.DecisionApplied {
		return false
	}
	// A first-time seed stamps the fact at sequence 0 (so the run's own future
	// events supersede it), which leaves the receipt generation untouched
	// above. It is still a real reducer mutation, so advance the window-wide
	// receipt generation here — decoupled from the fact's own wire sequence —
	// so an authoritative idle observation captured before the seed is
	// discarded rather than allowed to retire the freshly seeded lease.
	st.ShadowSeq++
	h.appendWatchdogEventLocked(seed.windowID, originWindowID, now, eventASLReconciled, "lease_seeded",
		string(protocol.SourceASL)+":"+seed.key,
		"discovery seeded run "+seed.candidate.RunID+" (phase "+phase+")")
	slog.Debug("asl reconcile seeded run lease",
		"window_id", seed.windowID,
		"run_id", seed.candidate.RunID,
		"phase", phase,
	)
	return true
}

// aslPhaseForDiscovered maps a discovered candidate's phase onto the reducer's
// phase vocabulary. The string values already match; the mapping exists to
// fail closed — an unknown phase from a newer loop version degrades to a bare
// between-turns lease instead of an active or attention state.
func aslPhaseForDiscovered(phase string) string {
	switch phase {
	case agentloop.PhaseTurn:
		return state.ASLPhaseTurn
	case agentloop.PhaseVerification:
		return state.ASLPhaseVerification
	case agentloop.PhaseWaiting:
		return state.ASLPhaseWaiting
	case agentloop.PhasePaused:
		return state.ASLPhasePaused
	default:
		return state.ASLPhaseBetween
	}
}
