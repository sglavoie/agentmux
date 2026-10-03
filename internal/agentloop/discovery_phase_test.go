package agentloop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The projection states the phase outright. Discovery no longer derives it from
// heartbeats, active-turn markers, or waiting flags, so every phase ASL can
// publish must survive the round trip verbatim.
func TestDiscoverCarriesEveryLivePhaseVerbatim(t *testing.T) {
	for _, phase := range []string{PhaseStarting, PhaseTurn, PhaseVerification, PhaseBetween, PhaseWaiting} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			planDir := filepath.Join(t.TempDir(), "plan")
			seedProjection(t, root, planDir, projectionSeed{Phase: phase, QuestionID: "q-9"})

			result, err := DiscoverPlans(DiscoverOptions{
				StateRoot:    root,
				PIDRunning:   livePIDs,
				ProcessBirth: matchingBirth(),
			})
			if err != nil {
				t.Fatalf("DiscoverPlans: %v", err)
			}
			observation := observationFor(t, result, root, planDir)
			if observation.Presence != PresencePresent || observation.Reason != ReasonLiveProcess {
				t.Fatalf("observation = %+v, want present/%s", observation, ReasonLiveProcess)
			}
			if observation.Candidate.Phase != phase || observation.Candidate.Paused {
				t.Fatalf("candidate phase = %q paused=%v, want %q unpaused", observation.Candidate.Phase, observation.Candidate.Paused, phase)
			}
		})
	}
}

// A pause finalizes the loop process, so a paused run has no PID to check and is
// still holding its position. Requiring liveness here would retire the lease of
// every run that is waiting on a human.
func TestDiscoverPausedRunIsPresentWithoutAProcess(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{Phase: PhasePaused, RunID: "run-paused"})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:  root,
		PIDRunning: deadPIDs,
		// A paused plan publishes no process, so nothing should ever be probed.
		ProcessBirth: func(pids []int) (map[int]string, bool) {
			if len(pids) != 0 {
				t.Errorf("probed %v for a paused plan, want no probe", pids)
			}
			return nil, true
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresencePresent || observation.Reason != ReasonPaused {
		t.Fatalf("observation = %+v, want present/%s", observation, ReasonPaused)
	}
	if !observation.Candidate.Paused || observation.Candidate.PID != 0 {
		t.Fatalf("candidate = %+v, want paused with no PID", observation.Candidate)
	}
}

// Reconciliation must see paused runs (their leases are still owed); routing and
// the loop markers must not (there is no process to route to).
func TestCandidatesFilterPausedRunsOnDemand(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	seedProjection(t, root, filepath.Join(base, "live"), projectionSeed{RunID: "run-live", PID: 11})
	seedProjection(t, root, filepath.Join(base, "held"), projectionSeed{RunID: "run-held", Phase: PhasePaused})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: matchingBirth(),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	if got := len(result.Candidates(true)); got != 2 {
		t.Fatalf("Candidates(true) = %d, want 2", got)
	}
	excluded := result.Candidates(false)
	if len(excluded) != 1 || excluded[0].RunID != "run-live" {
		t.Fatalf("Candidates(false) = %+v, want only run-live", excluded)
	}
}

// A finished run is the authoritative absence: it survives a daemon restart and
// a missed hook alike, and it does not depend on catching the process gone.
func TestDiscoverFinishedRunIsConfirmedAbsent(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{
		Phase:      PhaseFinished,
		RunID:      "run-done",
		FinishedAt: "2026-07-10T13:20:00+00:00",
	})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: matchingBirth(),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresenceConfirmedAbsent || observation.Reason != ReasonFinished {
		t.Fatalf("observation = %+v, want confirmed absent/%s", observation, ReasonFinished)
	}
	if len(result.Candidates(true)) != 0 {
		t.Fatalf("candidates = %+v, want none", result.Candidates(true))
	}
}

func TestDiscoverExitedProcessIsConfirmedAbsent(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{RunID: "run-gone", PID: 4242})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   deadPIDs,
		ProcessBirth: staticBirth(nil),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresenceConfirmedAbsent || observation.Reason != ReasonProcessExited {
		t.Fatalf("observation = %+v, want confirmed absent/%s", observation, ReasonProcessExited)
	}
}

// PID reuse is the case a liveness check alone gets exactly backwards: the PID
// is running, so a naive scan calls the run present and routes mutations into an
// unrelated process. The start token catches it, and the answer is
// indeterminate — a recycled PID says nothing about where the original run went.
func TestDiscoverRecycledPIDIsIndeterminate(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{RunID: "run-recycled", PID: 4242})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: staticBirth(map[int]string{4242: "Sat Jul 11 09:00:00 2026"}),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresenceIndeterminate || observation.Reason != ReasonProcessReplaced {
		t.Fatalf("observation = %+v, want indeterminate/%s", observation, ReasonProcessReplaced)
	}
	if len(result.Candidates(true)) != 0 {
		t.Fatalf("candidates = %+v, want none", result.Candidates(true))
	}
}

// ASL renders the start token with its own inherited locale and zone. The same
// process observed from a daemon in another zone renders as a whole-hour offset
// of the same instant, which is a rendering difference rather than PID reuse.
func TestDiscoverToleratesTimezoneRenderedStartTokens(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{
		RunID:      "run-zoned",
		PID:        4242,
		StartToken: "Fri Jul 10 13:05:00 2026",
	})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:  root,
		PIDRunning: livePIDs,
		// Same instant, rendered four hours away.
		ProcessBirth: staticBirth(map[int]string{4242: "Fri Jul 10 09:05:00 2026"}),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresencePresent || observation.Reason != ReasonLiveProcess {
		t.Fatalf("observation = %+v, want present/%s", observation, ReasonLiveProcess)
	}
}

// Whitespace padding is a `ps` formatting artifact, not an identity difference.
func TestDiscoverToleratesStartTokenWhitespace(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{PID: 4242, StartToken: "Fri Jul  4 09:05:00 2026"})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: staticBirth(map[int]string{4242: "Fri Jul 4 09:05:00 2026"}),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresencePresent {
		t.Fatalf("observation = %+v, want present", observation)
	}
}

// When the probe itself fails, a running PID is not enough to claim identity and
// not enough to claim absence.
func TestDiscoverUnprobedProcessIsIndeterminate(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{PID: 4242})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: func([]int) (map[int]string, bool) { return nil, false },
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresenceIndeterminate || observation.Reason != ReasonProcessUnverified {
		t.Fatalf("observation = %+v, want indeterminate/%s", observation, ReasonProcessUnverified)
	}
}

// ASL publishes a null process when it cannot establish its own birth, and the
// state migration tool writes the same shape. Neither attests liveness, and
// neither is a corrupt document.
func TestDiscoverLivePhaseWithoutAProcessIsIndeterminate(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{Phase: PhaseTurn, NoProcess: true})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: matchingBirth(),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation := observationFor(t, result, root, planDir)
	if observation.Presence != PresenceIndeterminate || observation.Reason != ReasonProcessUnverified {
		t.Fatalf("observation = %+v, want indeterminate/%s", observation, ReasonProcessUnverified)
	}
}

// The whole sweep authenticates against one observation of the process table,
// so plan count does not multiply subprocess cost at the ten-second cadence.
func TestDiscoverProbesProcessBirthOnce(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	seedProjection(t, root, filepath.Join(base, "a"), projectionSeed{RunID: "run-a", PID: 11})
	seedProjection(t, root, filepath.Join(base, "b"), projectionSeed{RunID: "run-b", PID: 12})
	seedProjection(t, root, filepath.Join(base, "c"), projectionSeed{RunID: "run-c", Phase: PhasePaused})

	probes := 0
	var probed []int
	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:  root,
		PIDRunning: livePIDs,
		ProcessBirth: func(pids []int) (map[int]string, bool) {
			probes++
			probed = append(probed, pids...)
			tokens := make(map[int]string, len(pids))
			for _, pid := range pids {
				tokens[pid] = testStartToken
			}
			return tokens, true
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	if probes != 1 {
		t.Fatalf("probes = %d, want 1", probes)
	}
	if len(probed) != 2 {
		t.Fatalf("probed = %v, want the two live PIDs only", probed)
	}
	if present, _, _ := result.Counts(); present != 3 {
		t.Fatalf("present = %d, want 3", present)
	}
}

func TestPresenceByRunIDCollapsesConflictingDuplicates(t *testing.T) {
	root := t.TempDir()
	base := t.TempDir()
	seedProjection(t, root, filepath.Join(base, "one"), projectionSeed{RunID: "run-dup", PID: 11})
	seedProjection(t, root, filepath.Join(base, "two"), projectionSeed{RunID: "run-dup", Phase: PhaseFinished})

	result, err := DiscoverPlans(DiscoverOptions{
		StateRoot:    root,
		PIDRunning:   livePIDs,
		ProcessBirth: matchingBirth(),
	})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	observation, ok := result.PresenceByRunID()["run-dup"]
	if !ok {
		t.Fatal("run-dup missing from PresenceByRunID")
	}
	if observation.Presence != PresenceIndeterminate {
		t.Fatalf("presence = %q, want indeterminate: a duplicate run ID is itself a reason not to assert absence", observation.Presence)
	}
}

// A state directory with no state.json is a plan ASL registered but never ran
// ("empty" in ASL's own pairing report). It is indeterminate, so it can never
// retire a lease, but it is reported as no-state rather than unreadable: it is
// not a fault, and a state root with hundreds of them must not look broken.
func TestDiscoverNeverRunPlanIsNoStateNotUnreadable(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, strings.Repeat("a", 64))
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "operations.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := DiscoverPlans(DiscoverOptions{StateRoot: root, PIDRunning: livePIDs, ProcessBirth: matchingBirth()})
	if err != nil {
		t.Fatalf("DiscoverPlans: %v", err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("observations = %+v, want one", result.Observations)
	}
	if got := result.Observations[0]; got.Presence != PresenceIndeterminate || got.Reason != ReasonNoState {
		t.Fatalf("observation = %+v, want indeterminate/%s", got, ReasonNoState)
	}
}
