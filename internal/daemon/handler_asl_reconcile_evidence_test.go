package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// planCandidate is a discovered run bound to a specific plan's state directory,
// which is what lets reconciliation correlate a lease back to the plan that
// produced it.
func planCandidate(projectDir, runID, stateDir, phase string) agentloop.Candidate {
	candidate := loopCandidate(projectDir, runID, phase)
	candidate.StateDir = stateDir
	candidate.PlanDir = stateDir + "/plan"
	candidate.Generation = 1
	return candidate
}

func presentObservation(candidate agentloop.Candidate) agentloop.PlanObservation {
	return agentloop.PlanObservation{
		StateDir:   candidate.StateDir,
		PlanDir:    candidate.PlanDir,
		RunID:      candidate.RunID,
		Presence:   agentloop.PresencePresent,
		Reason:     agentloop.ReasonLiveProcess,
		Generation: candidate.Generation,
		Sequence:   candidate.SourceSequence,
		Candidate:  candidate,
	}
}

func indeterminateObservation(stateDir, reason string) agentloop.PlanObservation {
	return agentloop.PlanObservation{
		StateDir: stateDir,
		Presence: agentloop.PresenceIndeterminate,
		Reason:   reason,
	}
}

func absentObservation(stateDir, runID string) agentloop.PlanObservation {
	return agentloop.PlanObservation{
		StateDir: stateDir,
		RunID:    runID,
		Presence: agentloop.PresenceConfirmedAbsent,
		Reason:   agentloop.ReasonProcessExited,
	}
}

func sweep(observations ...agentloop.PlanObservation) agentloop.DiscoveryResult {
	return agentloop.DiscoveryResult{StateRoot: "/state", RootPresent: true, Observations: observations}
}

func leaseLive(t *testing.T, h *Handler, windowID int, key string) bool {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[windowID]
	if st == nil || st.Reducer == nil {
		t.Fatalf("window %d has no reducer", windowID)
	}
	_, live := st.Reducer.LeaseState(key)
	return live
}

// One plan whose projection cannot be read must neither retire its own lease nor
// stop the rest of the sweep from being acted on. Both halves matter: reading
// "I could not tell" as absence duplicates work, and refusing to act on the
// whole sweep because one plan is corrupt starves every other plan.
func TestReconcileCorruptPlanNeitherClearsItsLeaseNorHidesHealthyPlans(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()
	corrupt := planCandidate(projectDir, "run-corrupt", "/state/corrupt", agentloop.PhaseTurn)

	stubDaemonDiscoverPlans(t, sweep(presentObservation(corrupt)), nil)
	h.ReconcileASLLeases(now, windows)
	if !leaseLive(t, h, 42, "run-corrupt") {
		t.Fatal("seed did not establish the corrupt plan's lease")
	}

	// The plan goes unreadable while a second, healthy plan appears.
	healthy := planCandidate(projectDir, "run-healthy", "/state/healthy", agentloop.PhaseTurn)
	stubDaemonDiscoverPlans(t, sweep(
		indeterminateObservation("/state/corrupt", agentloop.ReasonInvalidProjection),
		presentObservation(healthy),
	), nil)

	for i := 1; i <= 5; i++ {
		h.ReconcileASLLeases(now.Add(time.Duration(i)*asl.ReconcileInterval), windows)
	}

	if !leaseLive(t, h, 42, "run-corrupt") {
		t.Fatal("repeated unreadable evidence retired the corrupt plan's lease; want it preserved")
	}
	if !leaseLive(t, h, 42, "run-healthy") {
		t.Fatal("the healthy plan was never seeded; one corrupt plan hid it")
	}
	if got := h.ASLReconcileMetrics().InvalidProjections; got == 0 {
		t.Fatal("invalid projections metric = 0, want the unreadable plan counted")
	}
}

// Indeterminate evidence holds a streak where it is. Resetting it would let a
// plan that alternates unreadable and gone never retire; advancing it would let
// an unreadable plan retire a healthy run.
func TestReconcileIndeterminateEvidenceNeitherAdvancesNorResetsAMissStreak(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()
	candidate := planCandidate(projectDir, "run-A", "/state/a", agentloop.PhaseTurn)

	stubDaemonDiscoverPlans(t, sweep(presentObservation(candidate)), nil)
	h.ReconcileASLLeases(now, windows)

	// Miss one.
	stubDaemonDiscoverPlans(t, sweep(absentObservation("/state/a", "run-A")), nil)
	h.ReconcileASLLeases(now.Add(10*time.Second), windows)
	if !leaseLive(t, h, 42, "run-A") {
		t.Fatal("one confirmed absence retired the lease; want two required")
	}

	// Two unreadable sweeps in the middle: the streak must survive them without
	// growing.
	stubDaemonDiscoverPlans(t, sweep(indeterminateObservation("/state/a", agentloop.ReasonUnreadableState)), nil)
	h.ReconcileASLLeases(now.Add(20*time.Second), windows)
	h.ReconcileASLLeases(now.Add(30*time.Second), windows)
	if !leaseLive(t, h, 42, "run-A") {
		t.Fatal("unreadable evidence advanced the miss streak to retirement")
	}

	// The second real confirmation retires it: the earlier miss was preserved,
	// not reset.
	stubDaemonDiscoverPlans(t, sweep(absentObservation("/state/a", "run-A")), nil)
	h.ReconcileASLLeases(now.Add(40*time.Second), windows)
	if leaseLive(t, h, 42, "run-A") {
		t.Fatal("unreadable evidence reset the miss streak; want the second confirmation to retire the lease")
	}
}

// A present observation clears the streak outright, so a run that flaps in and
// out of one scan can never accumulate to retirement.
func TestReconcilePresentEvidenceClearsTheMissStreak(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()
	candidate := planCandidate(projectDir, "run-A", "/state/a", agentloop.PhaseTurn)
	present := sweep(presentObservation(candidate))
	absent := sweep(absentObservation("/state/a", "run-A"))

	stubDaemonDiscoverPlans(t, present, nil)
	h.ReconcileASLLeases(now, windows)

	for i, result := range []agentloop.DiscoveryResult{absent, present, absent, present, absent} {
		stubDaemonDiscoverPlans(t, result, nil)
		h.ReconcileASLLeases(now.Add(time.Duration(i+1)*asl.ReconcileInterval), windows)
	}
	if !leaseLive(t, h, 42, "run-A") {
		t.Fatal("alternating evidence retired the lease; want only consecutive confirmations to count")
	}
}

// A state root that does not exist looks exactly like a state root with no runs
// in it. Reading the first as absence would retire every lease on the machine
// the moment the configured path is wrong.
func TestReconcileMissingStateRootPreservesLeases(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()

	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-A", agentloop.PhaseTurn)}, nil)
	h.ReconcileASLLeases(now, windows)

	stubDaemonDiscoverPlans(t, agentloop.DiscoveryResult{StateRoot: "/state", RootPresent: false}, nil)
	h.ReconcileASLLeases(now.Add(10*time.Second), windows)
	h.ReconcileASLLeases(now.Add(20*time.Second), windows)
	h.ReconcileASLLeases(now.Add(30*time.Second), windows)

	if !leaseLive(t, h, 42, "run-A") {
		t.Fatal("an absent state root retired the lease; want evidence withheld")
	}
}

// Sweeps race writes. A read that went backwards on either monotonic counter is
// an older view of the run, and acting on it would resurrect a position the run
// has already left.
func TestReconcileRejectsObservationsThatWentBackwards(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()

	current := planCandidate(projectDir, "run-A", "/state/a", agentloop.PhaseTurn)
	current.Generation = 7
	current.SourceSequence = 20
	stubDaemonDiscoverPlans(t, sweep(presentObservation(current)), nil)
	h.ReconcileASLLeases(now, windows)
	before := h.ASLReconcileMetrics().StaleObservations

	stale := current
	stale.Generation = 6
	stubDaemonDiscoverPlans(t, sweep(presentObservation(stale)), nil)
	h.ReconcileASLLeases(now.Add(10*time.Second), windows)

	// Same generation, rewound notification stream.
	reordered := current
	reordered.SourceSequence = 3
	stubDaemonDiscoverPlans(t, sweep(presentObservation(reordered)), nil)
	h.ReconcileASLLeases(now.Add(20*time.Second), windows)

	if got := h.ASLReconcileMetrics().StaleObservations; got != before+2 {
		t.Fatalf("stale observations = %d, want %d", got, before+2)
	}
	if !leaseLive(t, h, 42, "run-A") {
		t.Fatal("a stale observation disturbed the live lease")
	}
}

// Reconciliation runs at startup and every ten seconds, so both restoring a
// presence and retiring a confirmed absence land inside the thirty seconds the
// contract promises.
func TestReconcileCadenceRetiresConfirmedAbsenceWithinThirtySeconds(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()
	candidate := planCandidate(projectDir, "run-A", "/state/a", agentloop.PhaseTurn)

	stubDaemonDiscoverPlans(t, sweep(presentObservation(candidate)), nil)
	h.maybeReconcileASLLeases(now, windows)
	if !leaseLive(t, h, 42, "run-A") {
		t.Fatal("startup sweep did not restore presence")
	}

	stubDaemonDiscoverPlans(t, sweep(absentObservation("/state/a", "run-A")), nil)
	deadline := now.Add(30 * time.Second)
	for at := now; !at.After(deadline); at = at.Add(time.Second) {
		h.maybeReconcileASLLeases(at, windows)
	}
	if leaseLive(t, h, 42, "run-A") {
		t.Fatal("confirmed absence was not retired within 30 seconds")
	}
}

// A sweep parses one state document per plan the operator has ever run, so its
// cost grows without bound while the bounded startup snapshot does not. Running
// it inside that budget made a long plan history enough to push an otherwise
// healthy daemon into permanently degraded startup. The startup reconciliation
// belongs to the first maintenance tick instead, which this pins from both
// sides: restore must not sweep, and the very next tick must.
func TestStartupRestoreLeavesTheSweepToTheFirstMaintenanceTick(t *testing.T) {
	h := newTestHandler()
	h.useKittyClient(&fakeKittyClient{windows: fakeOSWindowsWithTab(42, "api")})

	sweeps := 0
	old := daemonLoopDiscoverPlans
	daemonLoopDiscoverPlans = func(agentloop.DiscoverOptions) (agentloop.DiscoveryResult, error) {
		sweeps++
		return agentloop.DiscoveryResult{StateRoot: "/state", RootPresent: true}, nil
	}
	t.Cleanup(func() { daemonLoopDiscoverPlans = old })

	now := time.Now()
	if ready, reason := h.restoreStateFromKittyBounded(now, initialSnapshotBudget); !ready {
		t.Fatalf("bounded restore = (%v, %q); want ready", ready, reason)
	}
	if sweeps != 0 {
		t.Fatalf("startup restore ran %d sweeps inside the readiness budget, want 0", sweeps)
	}

	h.maintenanceTick(now)
	if sweeps != 1 {
		t.Fatalf("first maintenance tick ran %d sweeps, want 1", sweeps)
	}
}

// The sweep must reuse one cache across ticks: without it every reconciliation
// re-parses every state document on the machine, forever.
func TestReconcileReusesOneProjectionCacheAcrossSweeps(t *testing.T) {
	h, windows, _ := reconcileTestFixture(t)

	var caches []*agentloop.ProjectionCache
	old := daemonLoopDiscoverPlans
	daemonLoopDiscoverPlans = func(opts agentloop.DiscoverOptions) (agentloop.DiscoveryResult, error) {
		caches = append(caches, opts.Cache)
		return agentloop.DiscoveryResult{StateRoot: "/state", RootPresent: true}, nil
	}
	t.Cleanup(func() { daemonLoopDiscoverPlans = old })

	now := time.Now()
	h.ReconcileASLLeases(now, windows)
	h.ReconcileASLLeases(now.Add(asl.ReconcileInterval), windows)

	if len(caches) != 2 {
		t.Fatalf("sweeps = %d, want 2", len(caches))
	}
	if caches[0] == nil {
		t.Fatal("sweep ran without a projection cache")
	}
	if caches[0] != caches[1] {
		t.Fatal("each sweep built its own cache, so nothing is ever reused")
	}
}

// The metrics are the operator's only view of why reconciliation is or is not
// converging, so they must reach the health surface rather than staying
// internal to the handler.
func TestHealthReportsASLReconciliationMetrics(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	h.SetASLStateRoot("/tmp/asl-state")
	now := time.Now()
	candidate := planCandidate(projectDir, "run-A", "/state/a", agentloop.PhaseTurn)

	stubDaemonDiscoverPlans(t, sweep(
		presentObservation(candidate),
		indeterminateObservation("/state/b", agentloop.ReasonUnsupportedContract),
		absentObservation("/state/c", "run-C"),
	), nil)
	h.ReconcileASLLeases(now, windows)

	resp := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if !resp.OK {
		t.Fatalf("health response = %+v", resp)
	}
	metrics := resp.ASLReconcile
	if metrics == nil {
		t.Fatal("health carries no asl_reconcile metrics")
	}
	if metrics.StateRoot != "/tmp/asl-state" {
		t.Errorf("StateRoot = %q, want the configured root", metrics.StateRoot)
	}
	if metrics.PlansPresent != 1 || metrics.PlansIndeterminate != 1 || metrics.PlansAbsent != 1 {
		t.Errorf("plan counts = %d/%d/%d, want 1 present, 1 indeterminate, 1 absent",
			metrics.PlansPresent, metrics.PlansIndeterminate, metrics.PlansAbsent)
	}
	if metrics.UnsupportedContracts != 1 {
		t.Errorf("UnsupportedContracts = %d, want 1", metrics.UnsupportedContracts)
	}
	if metrics.Sweeps != 1 {
		t.Errorf("Sweeps = %d, want 1", metrics.Sweeps)
	}
	if metrics.IntervalSeconds != asl.ReconcileInterval.Seconds() {
		t.Errorf("IntervalSeconds = %v, want %v", metrics.IntervalSeconds, asl.ReconcileInterval.Seconds())
	}
	if metrics.ContractSetID != "" && metrics.ContractSetID != candidate.ContractSetID {
		t.Errorf("ContractSetID = %q", metrics.ContractSetID)
	}
}

// A failed sweep says nothing about any plan, so it must leave every lease and
// every streak exactly as it found them while still being visible to an
// operator.
func TestReconcileScanFailureIsRecordedAndChangesNothing(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()

	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-A", agentloop.PhaseTurn)}, nil)
	h.ReconcileASLLeases(now, windows)

	stubDaemonLoopDiscover(t, nil, errScanFailed{})
	for i := 1; i <= 4; i++ {
		h.ReconcileASLLeases(now.Add(time.Duration(i)*asl.ReconcileInterval), windows)
	}
	if !leaseLive(t, h, 42, "run-A") {
		t.Fatal("scan failures retired the lease")
	}
	if got := h.ASLReconcileMetrics().SweepErrors; got != 4 {
		t.Fatalf("SweepErrors = %d, want 4", got)
	}
}

type errScanFailed struct{}

func (errScanFailed) Error() string { return "scan failed" }
