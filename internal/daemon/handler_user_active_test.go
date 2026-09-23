package daemon

// user_active (F11 / the dashboard's "Mark active") is the operator forcing ⚡
// onto a tab. It acknowledges whatever is visible exactly as user_clear does,
// then contributes a bolt under its own synthetic source, which the next real
// hook/ASL event, an ASL supervisor question, or a verified harness exit retires
// before it applies. The screen idle probe retires it only once it has seen the
// pane working since the mark, never over the idle screen it was pressed on.
// Otherwise it ages out at StaleActive like any other ⚡.

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/hookevent"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// userActiveOwnerFixture is a one-pane managed Claude tab showing icon.
func userActiveOwnerFixture(t *testing.T, icon string) (*Handler, *fakeKittyClient) {
	t.Helper()
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitle("api", icon, false),
		[]kitty.Window{{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}},
	)}
	h.SetKittyClient(fake)
	return h, fake
}

// seedHarnessAction folds one real session-scoped Claude action.
func seedHarnessAction(t *testing.T, h *Handler, windowID int, action, event string, at time.Time) {
	t.Helper()
	ev := protocol.NormalizedEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:sess"},
		Action:    action,
		SessionID: "sess",
		Event:     event,
		At:        at.Unix(),
		Prompt:    event == "UserPromptSubmit",
	}
	req := protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: action, Event: event, IsPrompt: ev.Prompt, Normalized: &ev}
	if resp := h.handleStateUpdate(req); !resp.OK {
		t.Fatalf("seed %s rejected: %+v", action, resp)
	}
}

func markActive(t *testing.T, h *Handler, windowID int) {
	t.Helper()
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: windowID, Event: "user_active"}); !resp.OK {
		t.Fatalf("user_active response = %+v", resp)
	}
}

func TestHandler_UserActiveReplacesSettledIcons(t *testing.T) {
	cases := []struct {
		action string
		icon   string
	}{
		{"stop", "stop"},
		{"permission", "key"},
		{"stop_failure", "error"},
	}
	for _, tc := range cases {
		t.Run(tc.icon, func(t *testing.T) {
			h, _ := userActiveOwnerFixture(t, tc.icon)
			seedHarnessAction(t, h, 42, tc.action, "Stop", time.Now())
			if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.icon {
				t.Fatalf("seeded effective = %+v, want %s", eff, tc.icon)
			}

			markActive(t, h, 42)

			eff, desired := windowEffective(t, h, 42)
			if eff.Icon != "bolt" || desired != "bolt" {
				t.Fatalf("after user_active: effective %+v desired %q, want bolt", eff, desired)
			}
			if eff.Source != protocol.UserActiveSource() {
				t.Fatalf("bolt source = %+v, want the user_active source", eff.Source)
			}
			if g := h.guard.Current(42); g != "active" {
				t.Fatalf("guard = %q, want active", g)
			}
			h.stateMu.Lock()
			dirty := h.store[42].Dirty
			h.stateMu.Unlock()
			if !dirty {
				t.Fatal("window not dirty after user_active; the ⚡ would wait for maintenance")
			}
		})
	}
}

func TestHandler_UserActiveFromSiblingPanesTargetsOwner(t *testing.T) {
	// The managed case mirrors TestHandler_UserClearFromManagedNonOwnerTargetsOwner:
	// the active managed pane owns the title, and F11 arrives from the other one.
	siblings := map[string][2]kitty.Window{
		"unmanaged": {
			{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude}},
			{ID: 43, IsActive: true},
		},
		"managed": {
			{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude}},
			{ID: 43, UserVars: kitty.UserVars{Profile: "scratch", Harness: harness.Claude}},
		},
	}
	for name, panes := range siblings {
		t.Run(name, func(t *testing.T) {
			h := newTestHandler()
			owner, sibling := panes[0], panes[1]
			fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab(
				palette.BuildTitle("api", "stop", false),
				[]kitty.Window{owner, sibling},
			)}
			h.SetKittyClient(fake)
			seedHarnessAction(t, h, 42, "stop", "Stop", time.Now())

			markActive(t, h, 43)

			if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
				t.Fatalf("owner effective = %+v, want bolt", eff)
			}
			h.stateMu.Lock()
			src := h.store[43]
			h.stateMu.Unlock()
			if src != nil && src.Effective.Icon != "" {
				t.Fatalf("source pane effective = %+v, want untouched", src.Effective)
			}
			for _, ev := range h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43}).Events {
				if ev.SuppressionReason == suppressionReasonOwnerMismatch {
					t.Fatalf("source event = %+v; want no owner_mismatch", ev)
				}
			}
		})
	}
}

func TestHandler_UserActiveOwnerlessIconFreeTabIsNoOp(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: fakeOSWindowsWithSplitTab("api", []kitty.Window{{ID: 43, IsActive: true}})}
	h.SetKittyClient(fake)

	markActive(t, h, 43)

	h.stateMu.Lock()
	st := h.store[43]
	h.stateMu.Unlock()
	if st != nil && (st.DesiredIcon != "" || st.StateSet || st.Dirty) {
		t.Fatalf("state = %+v; ownerless icon-free user_active must not create renderer state", st)
	}
}

// TestHandler_UserActiveNextRealEventDecides pins the retirement contract: the
// next real hook event replaces the forced ⚡ with its own outcome.
func TestHandler_UserActiveNextRealEventDecides(t *testing.T) {
	t.Run("stop", func(t *testing.T) {
		h, _ := userActiveOwnerFixture(t, "")
		markActive(t, h, 42)
		seedHarnessAction(t, h, 42, "stop", "Stop", time.Now())
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective after real Stop = %+v, want stop", eff)
		}
	})
	t.Run("permission", func(t *testing.T) {
		h, _ := userActiveOwnerFixture(t, "")
		markActive(t, h, 42)
		seedHarnessAction(t, h, 42, "permission", "PermissionRequest", time.Now())
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "key" {
			t.Fatalf("effective after real permission = %+v, want key", eff)
		}
	})
	t.Run("prompt", func(t *testing.T) {
		h, _ := userActiveOwnerFixture(t, "")
		markActive(t, h, 42)
		seedHarnessAction(t, h, 42, "active", "UserPromptSubmit", time.Now())
		eff, _ := windowEffective(t, h, 42)
		if eff.Icon != "bolt" || eff.Source.Kind != protocol.SourceHarness {
			t.Fatalf("effective after real prompt = %+v, want the harness's own bolt", eff)
		}
		h.stateMu.Lock()
		retired := h.store[42].Reducer.RetireUserActive(time.Now().Unix())
		h.stateMu.Unlock()
		if retired {
			t.Fatal("the user_active fact was still live after a real prompt")
		}
	})
}

// TestHandler_UserActiveFromDashboardRetiresLikeF11 covers the un-stamped
// state_update the TUI sends: it must land on the same retirable source.
func TestHandler_UserActiveFromDashboardRetiresLikeF11(t *testing.T) {
	h := newTestHandler()
	seedHarnessAction(t, h, 42, "stop", "Stop", time.Now())
	if resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "user_active"}); !resp.OK {
		t.Fatalf("user_active response = %+v", resp)
	}
	eff, _ := windowEffective(t, h, 42)
	if eff.Icon != "bolt" || eff.Source != protocol.UserActiveSource() {
		t.Fatalf("effective after dashboard mark = %+v, want the user_active bolt", eff)
	}
	seedHarnessAction(t, h, 42, "stop", "Stop", time.Now().Add(time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after real Stop = %+v, want stop", eff)
	}
}

// TestHandler_UserActiveKeepsClearHoldAndAgesOut pins the F8-then-F11 flow on a
// stale Claude title: the forced ⚡ survives the screen probes, keeps the clear's
// promotion hold, and ages out at StaleActive to an empty tab rather than to a
// re-promoted ⚡ or a probe-settled 🛑.
func TestHandler_UserActiveKeepsClearHoldAndAgesOut(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "✳ File refactoring for code size reduction"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
	h.SetKittyClient(fake)

	claudeSeedActiveBolt(t, h, 42, "sess-done", base.Unix())
	seedHarnessAction(t, h, 42, "stop", "Stop", base.Add(time.Second))
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"}); !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}
	markActive(t, h, 42)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after F8 then F11 = %+v, want bolt", eff)
	}
	h.stateMu.Lock()
	held := h.store[42].BusyPromotionHeld()
	h.stateMu.Unlock()
	if !held {
		t.Fatal("user_active released the clear's busy-promotion hold")
	}

	marked := base.Add(2 * time.Second)
	for sample := 1; sample <= claudeIdleProbeLongestRun+2; sample++ {
		h.ProbeClaudeScreenStop(marked.Add(time.Duration(sample) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective after probe sample %d = %+v, want the forced bolt to hold", sample, eff)
		}
	}

	aged := marked.Add(11 * time.Minute)
	h.SweepStaleWithSnapshot(aged, nil)
	h.ProbeClaudeScreenStop(aged.Add(time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
		t.Fatalf("effective after the forced bolt aged out = %+v, want empty", eff)
	}
	for _, name := range []string{screenActiveProbeEvent, screenStopProbeEvent} {
		if got := countEventsNamed(t, h, 42, name); got != 0 {
			t.Fatalf("%s rows = %d, want 0 under the forced bolt and the held clear", name, got)
		}
	}
}

// hooklessClaudeMarkFixture is the reported shape: a managed Claude tab whose
// session delivers no hooks, marked ⚡ by the operator over an idle screen.
func hooklessClaudeMarkFixture(t *testing.T) (*Handler, *fakeKittyClient) {
	t.Helper()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "✳ Claude Code"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	markActive(t, h, 42)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" || eff.Source != protocol.UserActiveSource() {
		t.Fatalf("effective after F11 = %+v, want the user_active bolt", eff)
	}
	return h, fake
}

// TestHandler_UserActiveProbeSettlesAfterObservedWork pins the probe half of
// retirement: the idle screen the mark was pressed on never settles it, but a
// turn the probe watched run and finish afterwards does.
func TestHandler_UserActiveProbeSettlesAfterObservedWork(t *testing.T) {
	h, fake := hooklessClaudeMarkFixture(t)
	base := time.Now()

	for sample := 1; sample <= claudeIdleProbeLongestRun+1; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" || eff.Source != protocol.UserActiveSource() {
		t.Fatalf("effective over the pressed-on idle screen = %+v, want the forced bolt", eff)
	}

	fake.getText[kitty.MatchWindowByID(42)] = claudeWorkingScreen
	h.ProbeClaudeScreenStop(base.Add(10 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective while working = %+v, want bolt", eff)
	}

	fake.getText[kitty.MatchWindowByID(42)] = claudeIdleScreen
	for sample := 1; sample <= claudeIdleProbeLongestRun; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(10+sample) * time.Second))
	}
	eff, _ := windowEffective(t, h, 42)
	if eff.Icon != "stop" || eff.Source.Kind != protocol.SourceProbe || eff.Source.Key != claudeScreenStopProbeSource {
		t.Fatalf("effective after the observed turn finished = %+v, want the Claude screen probe's stop", eff)
	}
	h.stateMu.Lock()
	retired := h.store[42].Reducer.RetireUserActive(time.Now().Unix())
	armed := h.store[42].UserActiveArmed()
	h.stateMu.Unlock()
	if retired {
		t.Fatal("the user_active fact was still live after the probe settled")
	}
	if armed {
		t.Fatal("the settle left the mark armed for the next F11")
	}
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 1 {
		t.Fatalf("%s rows = %d, want 1", screenStopProbeEvent, got)
	}
}

// TestHandler_UserActiveReArmsOnEachMark proves a new F11 forgets work observed
// under an earlier mark.
func TestHandler_UserActiveReArmsOnEachMark(t *testing.T) {
	h, fake := hooklessClaudeMarkFixture(t)
	base := time.Now()
	fake.getText[kitty.MatchWindowByID(42)] = claudeWorkingScreen
	h.ProbeClaudeScreenStop(base)
	h.stateMu.Lock()
	armed := h.store[42].UserActiveArmed()
	h.stateMu.Unlock()
	if !armed {
		t.Fatal("a working screen under the mark did not arm it")
	}

	markActive(t, h, 42)
	fake.getText[kitty.MatchWindowByID(42)] = claudeIdleScreen
	for sample := 1; sample <= claudeIdleProbeLongestRun+1; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" || eff.Source != protocol.UserActiveSource() {
		t.Fatalf("effective after a fresh mark over idle = %+v, want the forced bolt", eff)
	}
}

// TestHandler_UserActiveHeldStopConfirmedByProbe covers a real Stop withheld
// under the mark: the probe confirms the hold instead of stepping aside, so the
// stop does not wait for the hold's deadline.
func TestHandler_UserActiveHeldStopConfirmedByProbe(t *testing.T) {
	h, _ := newTerminalHoldProbeHandler(t, harness.Claude, claudeIdleScreen)
	releaseHoldAtTestEnd(t, h, 42)
	base := time.Now()
	armTerminalHold(t, h)
	h.cancelStopHold(42)

	markActive(t, h, 42)
	if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
		t.Fatalf("Stop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, 42)
	if !armed || pending.class != stopHoldClassTerminal {
		t.Fatalf("pending hold = %+v/%v, want a terminal hold under the mark", pending, armed)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("icon while held = %q, want bolt", eff.Icon)
	}

	for sample := 0; sample < claudeIdleProbeLongestRun; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
	}
	pending, ok := terminalHoldPending(t, h, 42)
	if !ok || !pending.confirmed || pending.confirmedBy != protocol.StopConfirmedByClaudeScreen {
		t.Fatalf("hold after agreeing screens = %+v/%v, want confirmed by %s", pending, ok, protocol.StopConfirmedByClaudeScreen)
	}
	for _, fact := range windowFacts(t, h, 42) {
		if fact.Kind == protocol.SourceProbe && fact.Key == claudeScreenStopProbeSource {
			t.Fatalf("probe fact %+v settled; the held native stop owns this transition", fact)
		}
	}

	h.commitStopHold(42, pending.seq)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("icon after the confirmed hold committed = %+v, want stop", eff)
	}
}

// TestHandler_UserActiveRetiredBySupervisorQuestion proves an ASL question
// under the mark replaces it, so answering the question does not bring ⚡ back.
func TestHandler_UserActiveRetiredBySupervisorQuestion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, windows, projectDir := reconcileTestFixture(t)
		h.SetKittyClient(&fakeKittyClient{windows: windows})
		statePath := filepath.Join(t.TempDir(), "state.json")
		aslLifecycleHookRequest(t, h, events.AgentSessionLoopStarted, projectDir, hookevent.HookPayload{
			RunID:     supervisorRunID,
			StatePath: statePath,
		})
		markActive(t, h, 42)
		if got := supervisorVisibleState(t, h); got.Icon != "bolt" || got.Source != protocol.UserActiveSource() {
			t.Fatalf("after F11 = %+v, want the user_active bolt", got)
		}

		now := time.Now().UTC().Truncate(time.Second)
		deliverSupervisorMailboxQuestion(t, h, supervisorMailboxQuestionPayload(projectDir, statePath, supervisorQuestionID, 7, 7, now))
		synctest.Wait()
		if got := supervisorVisibleState(t, h); got.Icon != "question" {
			t.Fatalf("question under the mark = %+v, want question", got)
		}

		target := asl.SupervisorTarget{WindowID: 42, Candidate: agentloop.Candidate{RunID: supervisorRunID, StateDir: filepath.Dir(statePath)}}
		deliverSupervisorMailboxReply(t, h, supervisorMailboxReplyPayload(target, 7, 8))
		synctest.Wait()
		if got := supervisorVisibleState(t, h); got.Icon == "question" || got.Source == protocol.UserActiveSource() {
			t.Fatalf("after the reply = %+v, want the question cleared and the mark retired", got)
		}
	})
}

// TestHandler_UserActiveRetiredByProcessExit proves a verified harness exit
// settles the tab instead of leaving the forced ⚡ to age out.
func TestHandler_UserActiveRetiredByProcessExit(t *testing.T) {
	h := newTestHandler()
	identity := protocol.HarnessProcessIdentity{Harness: harness.Claude, PID: 7201, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
	if resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "user_active"}); !resp.OK {
		t.Fatalf("user_active response = %+v", resp)
	}
	// F11 acknowledges every fact, and exit detection only reads live ones, so
	// the process needs a fact of its own that landed under the mark without
	// retiring it. No hook path does that today; applying one directly keeps
	// the exit path honest should one ever appear.
	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.Apply(protocol.ReducerEvent{
		Source:         protocol.SourceID{Kind: protocol.SourceHarness, Key: identity.Harness + ":session"},
		Action:         "active",
		Seq:            2,
		At:             time.Now().Unix(),
		SessionID:      "session",
		HarnessProcess: identity,
	})
	st.ShadowSeq++
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(time.Now().Unix()))
	mark, known := st.Reducer.Fact(protocol.UserActiveSource())
	h.stateMu.Unlock()
	if !known || mark.Acked {
		t.Fatalf("user_active fact = %+v, want it still live before the exit", mark)
	}
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{
		statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessMissing},
	})

	if committed := h.processLifecycleTick(t.Context(), time.Now()); committed != 1 {
		t.Fatalf("process exit committed = %d, want 1", committed)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon == "bolt" || eff.Source == protocol.UserActiveSource() {
		t.Fatalf("effective after the harness exited = %+v, want the mark retired", eff)
	}
}
