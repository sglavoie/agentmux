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
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// maybeReconcileASLLeases runs reconciliation when the interval has elapsed
// (or has never run). Called from the maintenance tick with the fresh
// snapshot.
func (h *Handler) maybeReconcileASLLeases(now time.Time, windows []kitty.OSWindow) {
	h.stateMu.Lock()
	due := h.lastASLReconcileAt.IsZero() || now.Sub(h.lastASLReconcileAt) >= asl.ReconcileInterval
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
	// Paused runs have no loop process. Historical projections must not seed
	// leases onto new Kitty windows whose IDs happen to match an old run.
	result, err := daemonLoopDiscoverPlans(agentloop.DiscoverOptions{
		StateRoot: h.aslStateRoot(),
		Cache:     h.aslProjectionCache,
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
	sweep := asl.NewSweep(result)

	// Attribute each present candidate to the managed owner window(s) of its
	// workspace, reusing the exact matching policy lifecycle fan-out uses so
	// reconciliation and live events can never disagree about ownership.
	var seeds []aslSeed
	for _, candidate := range result.Candidates(false) {
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
	// Leases, supervisor targets, and question projections all move below; the
	// signal never blocks, and the hub's copy waits for this section to end.
	h.companionDirty()
	h.aslMetrics.RecordSweep(now, result)
	changedWindows := make(map[int]bool)

	for _, seed := range seeds {
		// Reject a scan that describes an older position for this run than one
		// already folded in. Two independent counters make that decidable: the
		// projection generation advances on every durable transition, and the
		// lifecycle source sequence advances on every notification. A read that
		// went backwards on either is a stale view — a reordered filesystem
		// read, or a scan that raced a write — and acting on it would resurrect
		// a position the run has already left.
		if !h.aslRunPosition.Accept(seed.candidate.RunID, seed.candidate.Generation, seed.candidate.SourceSequence) {
			h.aslMetrics.RecordStaleObservation()
			continue
		}
		h.aslSupervisors.Register(asl.SupervisorTarget{
			WindowID:       seed.windowID,
			OriginWindowID: seed.candidate.WindowID,
			Candidate:      seed.candidate,
		})
		if h.seedASLLeaseLocked(now, seed) {
			changedWindows[seed.windowID] = true
		}
		h.aslMissStreak.Clear(asl.LeaseRef{WindowID: seed.windowID, Key: seed.key})
	}

	// Two-miss removal: a live lease that two consecutive sweeps positively
	// confirmed gone converges to the finished state its lost finish event
	// would have produced.
	liveRefs := make(map[asl.LeaseRef]bool)
	for windowID, st := range h.store {
		if st.Reducer == nil {
			continue
		}
		for _, fact := range st.Reducer.Facts() {
			if fact.Kind != protocol.SourceASL || !fact.LeaseLive {
				continue
			}
			ref := asl.LeaseRef{WindowID: windowID, Key: fact.Key}
			switch sweep.Evidence(h.aslSupervisors.StateDir(ref), fact) {
			case asl.EvidencePresent:
				h.aslMissStreak.Clear(ref)
				continue
			case asl.EvidenceIndeterminate:
				// Hold the streak where it is. Resetting it would let a plan
				// that alternates unreadable and gone never retire; advancing
				// it would let an unreadable plan retire a healthy run.
				h.aslMetrics.RecordIndeterminateLease()
				if h.aslMissStreak.Streak(ref) > 0 {
					liveRefs[ref] = true
				}
				continue
			}

			h.aslMetrics.RecordConfirmedMiss()
			if h.aslMissStreak.Advance(ref) < asl.MissingRunThreshold {
				liveRefs[ref] = true
				continue
			}
			h.aslMissStreak.Clear(ref)
			h.aslSupervisors.Retire(ref)
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
			h.aslMetrics.RecordRetiredLease()
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
	h.aslMissStreak.Retain(func(ref asl.LeaseRef) bool { return liveRefs[ref] })

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

// seedASLLeaseLocked asserts one discovered run's lease on its owner window.
// Callers hold stateMu. Reports whether the window's facts changed.
//
//   - A run with no fact is seeded at sequence 0 with the projection's phase,
//     so the run's own future events (any positive sequence) always supersede
//     the seed.
//   - A live lease is left alone — live events own it.
//   - A released lease is NOT resurrected: a finish that named this exact run
//     outranks a discovery scan that may have raced it.
//   - Paused runs are never seeded: their loop process has ended.
//   - A waiting run also seeds a bare lease. Discovery proves the worker is
//     still blocked, but only a current supervisor projection correlated with
//     an interactive handoff may re-assert the human-actionable question icon.
func (h *Handler) seedASLLeaseLocked(now time.Time, seed aslSeed) bool {
	if seed.candidate.Paused || seed.candidate.Phase == agentloop.PhasePaused {
		return false
	}
	st := h.ensureWindowStateLocked(seed.windowID)
	if st.Reducer == nil {
		st.Reducer = state.NewReducer(h.reducerCfg)
	}
	exists, live := st.Reducer.LeaseState(seed.key)
	if live || exists {
		return false
	}
	phase := asl.PhaseForDiscovered(seed.candidate.Phase)
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
