package daemon

// Session 8 coverage: per-run ASL phase facts, the new lifecycle events, and
// discovery-backed lease reconciliation. The scenarios pin the plan's ASL
// policy — overlapping runs, nested finishes, lost lifecycle events, and
// restart recovery must all converge to deterministic icons.

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestNormalizeASLLifecyclePhases pins the event → phase (and paused →
// attention action) contract for all nine lifecycle events.
func TestNormalizeASLLifecyclePhases(t *testing.T) {
	cases := []struct {
		event      string
		payload    string
		wantPhase  string
		wantAction string
	}{
		{"AgentSessionLoopStarted", `{"run_id":"r1"}`, ASLPhaseBetween, ""},
		{"AgentSessionLoopTurnStarted", `{"run_id":"r1"}`, ASLPhaseTurn, ""},
		{"AgentSessionLoopTurnHeartbeat", `{"run_id":"r1"}`, ASLPhaseTurn, ""},
		{"AgentSessionLoopTurnFinished", `{"run_id":"r1"}`, ASLPhaseBetween, ""},
		{"AgentSessionLoopVerificationStarted", `{"run_id":"r1"}`, ASLPhaseVerification, ""},
		{"AgentSessionLoopVerificationHeartbeat", `{"run_id":"r1"}`, ASLPhaseVerification, ""},
		{"AgentSessionLoopVerificationFinished", `{"run_id":"r1"}`, ASLPhaseBetween, ""},
		{"AgentSessionLoopPaused", `{"run_id":"r1","pause_reason":"question"}`, ASLPhasePaused, "elicitation"},
		{"AgentSessionLoopPaused", `{"run_id":"r1","pause_reason":"verify-failed"}`, ASLPhasePaused, "failure"},
		{"AgentSessionLoopPaused", `{"run_id":"r1","reason":"blocked"}`, ASLPhasePaused, "failure"},
		{"AgentSessionLoopFinished", `{"run_id":"r1"}`, ASLPhaseFinished, ""},
	}
	for _, tc := range cases {
		n := NormalizedFromHook(tc.event, "claude", tc.payload, 42, false)
		if n.Invalid || !n.Deliverable() {
			t.Errorf("%s: normalized = %+v; want deliverable", tc.event, n)
			continue
		}
		if n.Source != (SourceID{Kind: SourceASL, Key: "r1"}) {
			t.Errorf("%s: source = %+v; want ASL run key r1", tc.event, n.Source)
		}
		if n.Phase != tc.wantPhase || n.Action != tc.wantAction {
			t.Errorf("%s: phase/action = %q/%q, want %q/%q",
				tc.event, n.Phase, n.Action, tc.wantPhase, tc.wantAction)
		}
		if n.RunID != "r1" {
			t.Errorf("%s: run id = %q, want r1", tc.event, n.RunID)
		}
	}
}

// TestReducerOverlappingRunsIndependentLeases: one run finishing never changes
// another run's fact, and the loop marker survives until the last lease
// releases.
func TestReducerOverlappingRunsIndependentLeases(t *testing.T) {
	r := NewReducer(ReducerConfig{})
	turnA := ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-A"}, RunID: "run-A", Phase: ASLPhaseTurn, Seq: 1, At: 100}
	betweenB := ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-B"}, RunID: "run-B", Phase: ASLPhaseBetween, Seq: 1, At: 101}
	r.Apply(turnA)
	r.Apply(betweenB)

	if got := r.Reduce(102); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("turn + between = %+v; want bolt looping", got)
	}

	// B finishes: A's executing turn is untouched.
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-B"}, RunID: "run-B", Phase: ASLPhaseFinished, Seq: 2, At: 103})
	if got := r.Reduce(104); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("after B finished = %+v; want A's bolt + looping preserved", got)
	}

	// A finishes too: no lease remains, the stopped fact becomes visible.
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-A"}, RunID: "run-A", Phase: ASLPhaseFinished, Seq: 2, At: 105})
	if got := r.Reduce(106); got.Icon != "stop" || got.Looping {
		t.Fatalf("after both finished = %+v; want stop without looping", got)
	}
}

// TestReducerBetweenTurnsMasksStopOrderIndependent: between turns the loop
// masks ⏹ (the harness stop that ended the previous turn) regardless of
// arrival order, and unmasks it once the lease releases.
func TestReducerBetweenTurnsMasksStopOrderIndependent(t *testing.T) {
	stop := ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "stop", Seq: 1, At: 100}
	between := ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-A"}, RunID: "run-A", Phase: ASLPhaseBetween, Seq: 1, At: 101}

	for name, events := range map[string][]ReducerEvent{
		"stop then between": {stop, between},
		"between then stop": {between, stop},
	} {
		if got := Replay(ReducerConfig{}, 102, events...); got.Icon != "" || !got.Looping {
			t.Errorf("%s = %+v; want bare loop marker (stop masked)", name, got)
		}
	}

	// Terminal errors are never masked.
	failed := ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:s1"}, Action: "stop_failure", Seq: 2, At: 103}
	if got := Replay(ReducerConfig{}, 104, between, failed); got.Icon != "error" || !got.Looping {
		t.Errorf("between + stop_failure = %+v; want error + looping", got)
	}

	// Once the lease releases, the finished run's own 🛑 becomes visible.
	finished := ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-A"}, RunID: "run-A", Phase: ASLPhaseFinished, Seq: 2, At: 105}
	if got := Replay(ReducerConfig{}, 106, stop, between, finished); got.Icon != "stop" || got.Looping {
		t.Errorf("after finish = %+v; want stop visible without looping", got)
	}
}

// TestReducerPausedRunHoldsAttentionIconWithLease: paused → matching attention
// icon plus 🔁.
func TestReducerPausedRunHoldsAttentionIconWithLease(t *testing.T) {
	paused := ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-A"}, RunID: "run-A", Action: "elicitation", Phase: ASLPhasePaused, Seq: 1, At: 100}
	if got := Replay(ReducerConfig{}, 101, paused); got.Icon != "question" || !got.Looping {
		t.Fatalf("paused(question) = %+v; want question + looping", got)
	}
	warned := ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-A"}, RunID: "run-A", Action: "failure", Phase: ASLPhasePaused, Seq: 1, At: 100}
	if got := Replay(ReducerConfig{}, 101, warned); got.Icon != "warning" || !got.Looping {
		t.Fatalf("paused(failure) = %+v; want warning + looping", got)
	}
}

// TestReducerNestedFinishesIdempotent: duplicate and reordered finishes for the
// same run are no-ops, and a finish for one run cannot end a sibling's lease.
func TestReducerNestedFinishesIdempotent(t *testing.T) {
	r := NewReducer(ReducerConfig{})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-A"}, RunID: "run-A", Phase: ASLPhaseTurn, Seq: 1, At: 100})
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-B"}, RunID: "run-B", Phase: ASLPhaseTurn, Seq: 1, At: 100})

	fin := ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-B"}, RunID: "run-B", Phase: ASLPhaseFinished, Seq: 2, At: 101}
	if d := r.Apply(fin); d != DecisionApplied {
		t.Fatalf("first finish decision = %v, want applied", d)
	}
	if d := r.Apply(fin); d != DecisionDuplicate {
		t.Fatalf("duplicate finish decision = %v, want duplicate", d)
	}
	late := ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-B"}, RunID: "run-B", Phase: ASLPhaseTurn, Seq: 1, At: 100}
	if d := r.Apply(late); d != DecisionReordered {
		t.Fatalf("reordered turn decision = %v, want reordered", d)
	}
	if got := r.Reduce(102); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("after nested finishes = %+v; want run-A's bolt + looping", got)
	}
}

// reconcileTestFixture builds a handler with one managed window (42) owning
// projectDir and a claude harness, plus a snapshot the reconciler can
// attribute candidates against.
func reconcileTestFixture(t *testing.T) (*Handler, []kitty.OSWindow, string) {
	t.Helper()
	projectDir := t.TempDir()
	h := newTestHandler()
	windows := hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api"}),
		[]kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "api", Harness: "claude", AMXDirectory: projectDir},
		}},
	)
	return h, windows, projectDir
}

func loopCandidate(projectDir, runID, phase string) agentloop.Candidate {
	return agentloop.Candidate{
		CWD:     projectDir,
		Harness: "claude",
		RunID:   runID,
		Phase:   phase,
	}
}

func windowLoopState(t *testing.T, h *Handler, windowID int) (looping bool, icon string) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[windowID]
	if st == nil {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.Looping, st.DesiredIcon
}

// TestReconcileSeedsDiscoveredRun: restart recovery — a live run with no fact
// (lost start event, wiped amx_state) is seeded with the phase the loop's own
// state file reports.
func TestReconcileSeedsDiscoveredRun(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-A", agentloop.PhaseTurn)}, nil)

	h.ReconcileASLLeases(time.Now(), windows)

	looping, icon := windowLoopState(t, h, 42)
	if !looping || icon != "bolt" {
		t.Fatalf("seeded state = looping=%v icon=%q; want executing turn ⚡🔁", looping, icon)
	}
}

// TestReconcileSeedsPausedRunAsBareLease: a paused run keeps 🔁 across a
// restart but does not re-alert its attention icon from disk.
func TestReconcileSeedsPausedRunAsBareLease(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	cand := loopCandidate(projectDir, "run-A", agentloop.PhasePaused)
	cand.Paused = true
	stubDaemonLoopDiscover(t, []agentloop.Candidate{cand}, nil)

	h.ReconcileASLLeases(time.Now(), windows)

	looping, icon := windowLoopState(t, h, 42)
	if !looping || icon != "" {
		t.Fatalf("seeded paused state = looping=%v icon=%q; want bare 🔁", looping, icon)
	}
}

// TestReconcileRemovesMissingRunAfterTwoSuccessfulDiscoveries: a lease whose
// run is gone converges to the finished state — but only after two consecutive
// successful discoveries agree, and discovery failures preserve everything.
func TestReconcileRemovesMissingRunAfterTwoSuccessfulDiscoveries(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()

	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-A", agentloop.PhaseTurn)}, nil)
	h.ReconcileASLLeases(now, windows)
	if looping, _ := windowLoopState(t, h, 42); !looping {
		t.Fatal("seed did not establish the lease")
	}

	// Discovery failures must not count toward removal.
	stubDaemonLoopDiscover(t, nil, errors.New("scan failed"))
	h.ReconcileASLLeases(now.Add(30*time.Second), windows)
	h.ReconcileASLLeases(now.Add(60*time.Second), windows)
	if looping, _ := windowLoopState(t, h, 42); !looping {
		t.Fatal("discovery failure released the lease; want preserved")
	}

	// First successful miss: still preserved.
	stubDaemonLoopDiscover(t, nil, nil)
	h.ReconcileASLLeases(now.Add(90*time.Second), windows)
	if looping, _ := windowLoopState(t, h, 42); !looping {
		t.Fatal("one successful miss released the lease; want two required")
	}

	// Second successful miss: converges to the finished state a lost finish
	// event would have produced.
	h.ReconcileASLLeases(now.Add(120*time.Second), windows)
	looping, icon := windowLoopState(t, h, 42)
	if looping || icon != "stop" {
		t.Fatalf("after two misses = looping=%v icon=%q; want stop without looping", looping, icon)
	}
}

// TestReconcileMissStreakResetsWhenRunReappears: a run seen again between two
// misses restarts the count, so a flapping scan cannot accumulate to removal.
func TestReconcileMissStreakResetsWhenRunReappears(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	now := time.Now()
	cand := loopCandidate(projectDir, "run-A", agentloop.PhaseBetween)

	stubDaemonLoopDiscover(t, []agentloop.Candidate{cand}, nil)
	h.ReconcileASLLeases(now, windows)

	stubDaemonLoopDiscover(t, nil, nil)
	h.ReconcileASLLeases(now.Add(30*time.Second), windows) // miss 1

	stubDaemonLoopDiscover(t, []agentloop.Candidate{cand}, nil)
	h.ReconcileASLLeases(now.Add(60*time.Second), windows) // seen: reset

	stubDaemonLoopDiscover(t, nil, nil)
	h.ReconcileASLLeases(now.Add(90*time.Second), windows) // miss 1 again
	if looping, _ := windowLoopState(t, h, 42); !looping {
		t.Fatal("non-consecutive misses released the lease; want streak reset on reappearance")
	}
}

// TestMaybeReconcileHonorsInterval: the periodic entry point reconciles on the
// first tick (startup) and then at most every aslReconcileInterval.
func TestMaybeReconcileHonorsInterval(t *testing.T) {
	h, windows, _ := reconcileTestFixture(t)
	calls := 0
	old := daemonLoopDiscover
	daemonLoopDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		calls++
		return nil, nil
	}
	t.Cleanup(func() { daemonLoopDiscover = old })

	now := time.Now()
	h.maybeReconcileASLLeases(now, windows)
	h.maybeReconcileASLLeases(now.Add(2*time.Second), windows)
	h.maybeReconcileASLLeases(now.Add(10*time.Second), windows)
	if calls != 1 {
		t.Fatalf("discovery calls within interval = %d, want 1 (startup only)", calls)
	}
	h.maybeReconcileASLLeases(now.Add(aslReconcileInterval), windows)
	if calls != 2 {
		t.Fatalf("discovery calls after interval = %d, want 2", calls)
	}
}

// aslLifecycleHookRequest sends one ASL lifecycle hook through the full hook
// path against the fixture's managed window.
func aslLifecycleHookRequest(t *testing.T, h *Handler, event, projectDir string, payload hookevent.HookPayload) {
	t.Helper()
	payload.Source = "agent_session_loop"
	payload.CWD = projectDir
	payload.Harness = "claude"
	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    42,
		Event:       event,
		PayloadJSON: hookPayloadJSON(t, payload),
	})
	if !resp.OK {
		t.Fatalf("%s response = %+v", event, resp)
	}
}

// TestHookEventOverlappingRunsKeepIndependentLeases drives two overlapping
// runs through the full hook path: finishing one run preserves the other's
// lease, and finishing both surfaces the stopped, non-looping outcome.
func TestHookEventOverlappingRunsKeepIndependentLeases(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)
	stubDaemonLoopDiscover(t, nil, nil)

	aslLifecycleHookRequest(t, h, "AgentSessionLoopTurnStarted", projectDir, hookevent.HookPayload{RunID: "run-A"})
	aslLifecycleHookRequest(t, h, "AgentSessionLoopStarted", projectDir, hookevent.HookPayload{RunID: "run-B"})
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "bolt" {
		t.Fatalf("two live runs = looping=%v icon=%q; want ⚡🔁", looping, icon)
	}

	aslLifecycleHookRequest(t, h, "AgentSessionLoopFinished", projectDir, hookevent.HookPayload{RunID: "run-A"})
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "" {
		t.Fatalf("after run-A finish = looping=%v icon=%q; want run-B's bare 🔁 (🛑 masked)", looping, icon)
	}

	aslLifecycleHookRequest(t, h, "AgentSessionLoopFinished", projectDir, hookevent.HookPayload{RunID: "run-B"})
	if looping, icon := windowLoopState(t, h, 42); looping || icon != "stop" {
		t.Fatalf("after both finish = looping=%v icon=%q; want 🛑 without 🔁", looping, icon)
	}
}

// TestHookEventPausedRunShowsAttentionIcon: a paused run surfaces the matching
// attention icon plus 🔁 through the full hook path.
func TestHookEventPausedRunShowsAttentionIcon(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)

	aslLifecycleHookRequest(t, h, "AgentSessionLoopTurnStarted", projectDir, hookevent.HookPayload{RunID: "run-A"})
	aslLifecycleHookRequest(t, h, "AgentSessionLoopPaused", projectDir, hookevent.HookPayload{RunID: "run-A", PauseReason: "question"})
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "question" {
		t.Fatalf("paused run = looping=%v icon=%q; want ❓ + 🔁", looping, icon)
	}
}

// TestHookEventFinishWithoutRunIDTriggersReconciliation: a legacy finish that
// cannot name its run must not clear every lease — reconciliation re-seeds the
// still-running sibling from discovery.
func TestHookEventFinishWithoutRunIDTriggersReconciliation(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)

	// run-B is still alive on disk; the finish event below carries no run id.
	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-B", agentloop.PhaseTurn)}, nil)

	aslLifecycleHookRequest(t, h, "AgentSessionLoopStarted", projectDir, hookevent.HookPayload{})
	aslLifecycleHookRequest(t, h, "AgentSessionLoopFinished", projectDir, hookevent.HookPayload{})

	looping, icon := windowLoopState(t, h, 42)
	if !looping || icon != "bolt" {
		t.Fatalf("after legacy finish = looping=%v icon=%q; want run-B reconciled back to ⚡🔁", looping, icon)
	}
}

// TestHookEventVerificationPhaseContributesActiveWork: verification start and
// heartbeat both render as active work with the lease held.
func TestHookEventVerificationPhaseContributesActiveWork(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)

	aslLifecycleHookRequest(t, h, "AgentSessionLoopVerificationStarted", projectDir, hookevent.HookPayload{RunID: "run-A"})
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "bolt" {
		t.Fatalf("verification started = looping=%v icon=%q; want ⚡🔁", looping, icon)
	}
	aslLifecycleHookRequest(t, h, "AgentSessionLoopVerificationFinished", projectDir, hookevent.HookPayload{RunID: "run-A"})
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "" {
		t.Fatalf("verification finished = looping=%v icon=%q; want bare 🔁 between turns", looping, icon)
	}
}
