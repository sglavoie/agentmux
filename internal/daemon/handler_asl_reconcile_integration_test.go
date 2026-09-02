package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

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
	old := daemonLoopDiscoverPlans
	daemonLoopDiscoverPlans = func(agentloop.DiscoverOptions) (agentloop.DiscoveryResult, error) {
		calls++
		return agentloop.DiscoveryResult{RootPresent: true}, nil
	}
	t.Cleanup(func() { daemonLoopDiscoverPlans = old })

	now := time.Now()
	h.maybeReconcileASLLeases(now, windows)
	h.maybeReconcileASLLeases(now.Add(2*time.Second), windows)
	h.maybeReconcileASLLeases(now.Add(aslReconcileInterval-time.Millisecond), windows)
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
	resp := h.Handle(protocol.Request{
		Type:        protocol.TypeHookEvent,
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

	// Seed the real provider bolt that used to survive both finished ASL facts
	// when Claude's native Stop was missing. Finishing run-A must retire it while
	// run-B's independent lease continues to own the loop marker.
	resp := h.Handle(protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "session-1", TurnID: "turn-1"}),
	})
	if !resp.OK {
		t.Fatalf("UserPromptSubmit response = %+v", resp)
	}

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

// TestHookEventWaitingRunKeepsBareLoop drives the worker mailbox wait through
// the full hook path: the loop lease stays live without claiming that a user
// can answer yet, and the wait ending remains a bare 🔁.
func TestHookEventWaitingRunKeepsBareLoop(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)

	aslLifecycleHookRequest(t, h, "AgentSessionLoopTurnStarted", projectDir, hookevent.HookPayload{RunID: "run-A"})
	aslLifecycleHookRequest(t, h, "AgentSessionLoopWaitStarted", projectDir, hookevent.HookPayload{
		RunID: "run-A", QuestionID: "q-1", QuestionPreview: "ship the shim change?",
	})
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "" {
		t.Fatalf("waiting run = looping=%v icon=%q; want bare 🔁", looping, icon)
	}

	aslLifecycleHookRequest(t, h, "AgentSessionLoopWaitEnded", projectDir, hookevent.HookPayload{
		RunID: "run-A", QuestionID: "q-1", WaitReason: "reply",
	})
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "" {
		t.Fatalf("wait ended = looping=%v icon=%q; want bare 🔁", looping, icon)
	}
}

// TestHookEventWaitStartedCarriesQuestionPreviewToSessionDetail: the preview is
// the one free-text payload field the daemon keeps, and it reaches the TUI only
// through the session detail buffer — never through NormalizedEvent or a
// reducer fact.
func TestHookEventWaitStartedCarriesQuestionPreviewToSessionDetail(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)

	const preview = "Should the shim keep the narrow branch?"
	aslLifecycleHookRequest(t, h, "AgentSessionLoopWaitStarted", projectDir, hookevent.HookPayload{
		RunID: "run-A", QuestionID: "q-1", QuestionPreview: preview,
	})

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if !resp.OK {
		t.Fatalf("session detail response = %+v", resp)
	}
	var found bool
	for _, ev := range resp.Events {
		if ev.Event != events.AgentSessionLoopWaitStarted {
			continue
		}
		found = true
		if ev.QuestionPreview != preview {
			t.Fatalf("QuestionPreview = %q, want %q", ev.QuestionPreview, preview)
		}
	}
	if !found {
		t.Fatalf("no %s event in session detail buffer: %+v", events.AgentSessionLoopWaitStarted, resp.Events)
	}
}

// TestHookEventWaitStartedSanitizesQuestionPreview: the question body is
// model-controlled, so the hook decode boundary must strip terminal controls
// before the daemon copies the preview onto the SessionEvent the detail view
// renders.
func TestHookEventWaitStartedSanitizesQuestionPreview(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)

	esc, bel := string(rune(0x1b)), string(rune(0x07))
	aslLifecycleHookRequest(t, h, "AgentSessionLoopWaitStarted", projectDir, hookevent.HookPayload{
		RunID: "run-A", QuestionID: "q-1",
		QuestionPreview: esc + "[2JShip " + esc + "]0;pwned" + bel + "the shim?",
	})

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if !resp.OK {
		t.Fatalf("session detail response = %+v", resp)
	}
	var found bool
	for _, ev := range resp.Events {
		if ev.Event != events.AgentSessionLoopWaitStarted {
			continue
		}
		found = true
		if want := "Ship the shim?"; ev.QuestionPreview != want {
			t.Fatalf("QuestionPreview = %q, want %q", ev.QuestionPreview, want)
		}
	}
	if !found {
		t.Fatalf("no %s event in session detail buffer: %+v", events.AgentSessionLoopWaitStarted, resp.Events)
	}
}

// TestReconcileSeedsWaitingRunAsBareLoop: restart recovery preserves the live
// worker lease without manufacturing a human handoff from persisted wait state.
func TestReconcileSeedsWaitingRunAsBareLoop(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-A", agentloop.PhaseWaiting)}, nil)

	h.ReconcileASLLeases(time.Now(), windows)

	looping, icon := windowLoopState(t, h, 42)
	if !looping || icon != "" {
		t.Fatalf("seeded waiting state = looping=%v icon=%q; want bare 🔁", looping, icon)
	}
}

// TestReconcileSettlesWaitingLeaseWhenRunVanishes: if the loop dies mid-wait
// no wait-ended event ever arrives, so reconciliation converges its bare lease
// to the finished state after two misses.
func TestReconcileSettlesWaitingLeaseWhenRunVanishes(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)
	now := time.Now()

	stubDaemonLoopDiscover(t, []agentloop.Candidate{loopCandidate(projectDir, "run-A", agentloop.PhaseWaiting)}, nil)
	aslLifecycleHookRequest(t, h, "AgentSessionLoopWaitStarted", projectDir, hookevent.HookPayload{
		RunID: "run-A", QuestionID: "q-1",
	})
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "" {
		t.Fatalf("waiting run = looping=%v icon=%q; want bare 🔁", looping, icon)
	}

	stubDaemonLoopDiscover(t, nil, nil)
	h.ReconcileASLLeases(now, windows)
	if looping, icon := windowLoopState(t, h, 42); !looping || icon != "" {
		t.Fatalf("one miss = looping=%v icon=%q; want bare 🔁 preserved until two misses", looping, icon)
	}

	h.ReconcileASLLeases(now.Add(30*time.Second), windows)
	looping, icon := windowLoopState(t, h, 42)
	if looping || icon != "stop" {
		t.Fatalf("after two misses = looping=%v icon=%q; want the waiting lease settled to 🛑", looping, icon)
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
