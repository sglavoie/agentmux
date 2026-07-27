package daemon

// ASL lease reconciliation. Lifecycle hook events are the fast path for
// per-run ASL facts, but events can be lost (daemon restart, crashed loop,
// dropped hook) and a legacy finish without run_id cannot name which lease it
// releases. Reconciliation closes the gap: it compares every window's live ASL
// leases against agent-session-loop's on-disk run state (the ground truth the
// loop itself persists) and converges the facts.
//
//   - A discovered run with no live lease is seeded onto its managed owner
//     window with the phase the state file reports, so a restart or a lost
//     start/turn event still produces 🔁 (and ⚡ for an executing turn).
//   - A live lease whose run is missing from discovery is converted to a
//     finished fact — the same state a received finish event would have
//     produced — but only after two consecutive successful discoveries agree
//     it is gone, so one racy scan can never revoke a healthy lease.
//   - A discovery failure changes nothing: missing evidence preserves leases.
//
// It runs once at startup (the first maintenance tick) and every 30 seconds
// afterwards, plus immediately after a finish event that carried no run_id.

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const (
	// aslReconcileInterval is the periodic reconciliation cadence.
	aslReconcileInterval = 30 * time.Second
	// aslMissingRunThreshold is how many consecutive successful discoveries
	// must miss a run before its lease is released.
	aslMissingRunThreshold = 2
)

// aslLeaseRef identifies one window's ASL lease fact for miss-streak tracking.
type aslLeaseRef struct {
	windowID int
	key      string
}

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
	candidates, err := daemonLoopDiscover(agentloop.DiscoverOptions{IncludePaused: true})
	if err != nil {
		slog.Debug("asl reconcile skipped: discovery failed; preserving leases", "err", err)
		return
	}
	h.reconcileASLCandidates(now, windows, candidates)
}

// aslSeed is one discovered run attributed to its managed owner window.
type aslSeed struct {
	windowID  int
	key       string
	candidate agentloop.Candidate
}

// reconcileASLCandidates folds a successful discovery into the per-window
// reducers.
func (h *Handler) reconcileASLCandidates(now time.Time, windows []kitty.OSWindow, candidates []agentloop.Candidate) {
	// Attribute each candidate to the managed owner window(s) of its
	// workspace, reusing the exact matching policy lifecycle fan-out uses so
	// reconciliation and live events can never disagree about ownership.
	liveRunIDs := make(map[string]bool, len(candidates))
	windowsWithLoops := make(map[int]bool)
	var seeds []aslSeed
	for _, candidate := range candidates {
		if candidate.RunID != "" {
			liveRunIDs[candidate.RunID] = true
		}
		targets, _ := matchManagedLoopTargets(windows, []agentloop.Candidate{candidate})
		for _, target := range targets {
			windowsWithLoops[target.windowID] = true
			key := candidate.RunID
			if key == "" {
				key = windowKey(target.windowID)
			}
			seeds = append(seeds, aslSeed{windowID: target.windowID, key: key, candidate: candidate})
		}
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	changedWindows := make(map[int]bool)

	for _, seed := range seeds {
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

	// Two-miss removal: a live lease whose run two consecutive successful
	// discoveries could not see converges to the finished state its lost
	// finish event would have produced.
	liveRefs := make(map[aslLeaseRef]bool)
	for windowID, st := range h.store {
		if st.Reducer == nil {
			continue
		}
		for _, fact := range st.Reducer.Facts() {
			if fact.Kind != SourceASL || !fact.LeaseLive {
				continue
			}
			ref := aslLeaseRef{windowID: windowID, key: fact.Key}
			present := false
			if fact.RunID != "" {
				present = liveRunIDs[fact.RunID]
			} else {
				// A window-keyed lease predates run identity; it is corroborated
				// by any discovered run owned by the same window.
				present = windowsWithLoops[windowID]
			}
			if present {
				delete(h.aslMissStreak, ref)
				continue
			}
			h.aslMissStreak[ref]++
			if h.aslMissStreak[ref] < aslMissingRunThreshold {
				liveRefs[ref] = true
				continue
			}
			delete(h.aslMissStreak, ref)
			delete(h.aslSupervisorTargets, ref)
			h.clearASLSupervisorQuestionLocked(
				windowID,
				fact.RunID,
				fact.Seq+1,
				now,
				"ASL run missing from consecutive discoveries; supervisor question projection retired",
			)
			st.ShadowSeq++
			st.Reducer.Apply(ReducerEvent{
				Source:         SourceID{Kind: SourceASL, Key: fact.Key},
				Seq:            st.ShadowSeq,
				At:             now.Unix(),
				OriginWindowID: fact.OriginWindowID,
				RunID:          fact.RunID,
				Phase:          ASLPhaseFinished,
			})
			changedWindows[windowID] = true
			h.appendWatchdogEventLocked(windowID, fact.OriginWindowID, now, eventASLReconciled, "lease_released",
				string(SourceASL)+":"+fact.Key,
				"run "+fact.RunID+" missing from consecutive discoveries; lease released")
			slog.Debug("asl reconcile released missing run lease",
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

// seedASLLeaseLocked asserts one discovered run's lease on its owner window.
// Callers hold stateMu. Reports whether the window's facts changed.
//
//   - A run with no fact is seeded at sequence 0 with the state file's phase,
//     so the run's own future events (any positive sequence) always supersede
//     the seed.
//   - A live lease is left alone — live events own it.
//   - A released window-keyed lease is re-asserted with a fresh receipt
//     sequence: a legacy finish could not name which run it ended, and
//     discovery says one is still running (the pre-identity compatibility
//     contract). A released run-keyed lease is NOT resurrected — a finish that
//     named this exact run outranks a discovery scan that may have raced it.
//   - Paused runs seed a bare lease (phase only, no attention action): 🔁
//     survives a restart, but a pause prompt the user may have already seen is
//     not re-alerted from disk.
//   - A waiting run does seed ❓, because unlike a pause its marker is only on
//     disk while the loop process is alive and blocked on the reply — discovery
//     drops dead runs, so the question is provably still open right now.
func (h *Handler) seedASLLeaseLocked(now time.Time, seed aslSeed) bool {
	st := h.ensureWindowStateLocked(seed.windowID)
	if st.Reducer == nil {
		st.Reducer = NewReducer(h.reducerCfg)
	}
	exists, live := st.Reducer.LeaseState(seed.key)
	if live {
		return false
	}
	if exists && seed.candidate.RunID != "" {
		return false
	}
	var seq uint64
	if exists {
		st.ShadowSeq++
		seq = st.ShadowSeq
	}
	phase := aslPhaseForDiscovered(seed.candidate.Phase)
	originWindowID := seed.candidate.WindowID
	if originWindowID <= 0 {
		originWindowID = seed.windowID
	}
	decision := st.Reducer.Apply(ReducerEvent{
		Source:         SourceID{Kind: SourceASL, Key: seed.key},
		Seq:            seq,
		At:             now.Unix(),
		OriginWindowID: originWindowID,
		RunID:          seed.candidate.RunID,
		Phase:          phase,
	})
	if decision != DecisionApplied {
		return false
	}
	if !exists {
		// A first-time seed stamps the fact at sequence 0 (so the run's own future
		// events supersede it), which leaves the receipt generation untouched
		// above. It is still a real reducer mutation, so advance the window-wide
		// receipt generation here — decoupled from the fact's own wire sequence —
		// so an authoritative idle observation captured before the seed is
		// discarded rather than allowed to retire the freshly seeded lease.
		st.ShadowSeq++
	}
	h.appendWatchdogEventLocked(seed.windowID, originWindowID, now, eventASLReconciled, "lease_seeded",
		string(SourceASL)+":"+seed.key,
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
		return ASLPhaseTurn
	case agentloop.PhaseVerification:
		return ASLPhaseVerification
	case agentloop.PhaseWaiting:
		return ASLPhaseWaiting
	case agentloop.PhasePaused:
		return ASLPhasePaused
	default:
		return ASLPhaseBetween
	}
}
