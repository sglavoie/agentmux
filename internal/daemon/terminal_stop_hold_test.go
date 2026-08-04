package daemon

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	terminalHoldTestDelay   = 6 * time.Second
	terminalHoldTestTimeout = 15 * time.Second
)

// newTerminalHoldTestHandler builds the only pane shape in which the
// confirmed-idle gate arms: an interactive, screen-readable window owned by a
// harness that has an idle stop probe. Stop notifications are enabled so "no
// notification fired" is a real assertion rather than a disabled-config
// tautology.
func newTerminalHoldTestHandler(t *testing.T) (*Handler, *callRecorder) {
	t.Helper()
	notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
	h := NewHandler(NewStateGuard(time.Hour), notifier)
	// Wire the live mute lookup the daemon installs, so mute is consulted at
	// dispatch time exactly as it is in production.
	notifier.SetIsMuted(h.IsMuted)
	h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, true)
	h.SetKittyClient(&fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "", false), []kitty.Window{{
		ID:       42,
		IsActive: true,
		UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
	}})})
	return h, rec
}

func terminalHoldPending(t *testing.T, h *Handler, windowID int) (pendingStopHold, bool) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	pending, ok := h.pendingStopHolds[windowID]
	return pending, ok
}

// confirmTerminalHold drives the probe confirmation seam the way
// applyAuthoritativeIdle does — under stateMu — without needing a live screen or
// transcript read.
func confirmTerminalHold(t *testing.T, h *Handler, windowID int, source SourceID) bool {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.confirmStopHoldLocked(windowID, source)
}

// claudeScreenProbeSource is the SourceID the Claude screen probe settles under,
// and the one the confirmation seam maps to StopConfirmedByClaudeScreen.
var claudeScreenProbeSource = SourceID{Kind: SourceProbe, Key: claudeScreenStopProbeSource}

// newTerminalHoldProbeHandler builds the pane shape where the confirmed-idle
// gate and the harness's own screen probe both apply: an interactive managed tab
// whose screen the fake client can read while its stop is withheld.
func newTerminalHoldProbeHandler(t *testing.T, harnessName, screen string) (*Handler, *callRecorder) {
	t.Helper()
	notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
	h := NewHandler(NewStateGuard(time.Hour), notifier)
	notifier.SetIsMuted(h.IsMuted)
	h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, true)
	h.SetKittyClient(&fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harnessName,
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): screen},
	})
	return h, rec
}

// releaseHoldAtTestEnd drops a still-pending hold when the test returns. A probe
// test asserts on the confirmation, not on the commit, so without this the
// multi-second hold timer would outlive the test and repaint a handler nothing
// is watching any more.
func releaseHoldAtTestEnd(t *testing.T, h *Handler, windowID int) {
	t.Helper()
	t.Cleanup(func() { h.cancelStopHold(windowID) })
}

func windowFacts(t *testing.T, h *Handler, windowID int) []SourceFactSnapshot {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok || st.Reducer == nil {
		t.Fatalf("window %d has no reducer", windowID)
	}
	return st.Reducer.Facts()
}

// assertNoProbeStopFact proves a probe's idle evidence was consumed as
// confirmation of the held native stop instead of settling a synthetic stop of
// its own.
func assertNoProbeStopFact(t *testing.T, h *Handler, windowID int) {
	t.Helper()
	for _, fact := range windowFacts(t, h, windowID) {
		if fact.Kind == SourceProbe {
			t.Fatalf("probe fact %+v settled; the held native stop owns this transition", fact)
		}
	}
}

// armTerminalHold drives a prompt then a native Stop and asserts the Stop
// entered the terminal class instead of painting immediately.
func armTerminalHold(t *testing.T, h *Handler) {
	t.Helper()
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit"}); !resp.OK {
		t.Fatalf("UserPromptSubmit response = %+v", resp)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("icon after prompt = %q, want bolt", eff.Icon)
	}
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
		t.Fatalf("Stop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, 42)
	if !armed || pending.class != stopHoldClassTerminal {
		t.Fatalf("pending hold = %+v/%v, want an armed terminal hold", pending, armed)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("icon while held = %q, want bolt", eff.Icon)
	}
}

// deliverProcessStop fires a native Stop the way the hook client delivers one:
// carrying the session it belongs to and the exact process fingerprint that
// emitted it, so the daemon can tell this stop's producer apart from any sibling
// process sharing the window.
func deliverProcessStop(t *testing.T, h *Handler, windowID int, identity HarnessProcessIdentity, sessionID string) {
	t.Helper()
	resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: windowID, Event: "Stop",
		Harness: identity.Harness, HarnessProcess: &identity,
		PayloadJSON: `{"session_id":"` + sessionID + `"}`,
	})
	if !resp.OK {
		t.Fatalf("Stop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, windowID)
	if !armed || pending.class != stopHoldClassTerminal {
		t.Fatalf("pending hold = %+v/%v, want an armed terminal hold", pending, armed)
	}
	if eff, _ := windowEffective(t, h, windowID); eff.Icon != "bolt" {
		t.Fatalf("icon while held = %q, want bolt", eff.Icon)
	}
}

// renameHeldWindow moves the durable title/profile of a window whose stop is
// already held. No hook event can do this without also superseding the hold, so
// the durable fields a later non-terminal delivery would have written are set
// directly.
func renameHeldWindow(t *testing.T, h *Handler, windowID int, title, account string) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.ensureWindowStateLocked(windowID)
	st.LastSessionTitle = title
	st.LastAccount = account
}

func awaitStopHoldClaim(t *testing.T, claims <-chan struct{}, failure string, resume chan struct{}) {
	t.Helper()
	select {
	case <-claims:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal(failure)
	}
}

func lastStopEvent(t *testing.T, h *Handler, windowID int) SessionEvent {
	t.Helper()
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: windowID})
	for i := len(detail.Events) - 1; i >= 0; i-- {
		if detail.Events[i].Event == "Stop" {
			return detail.Events[i]
		}
	}
	t.Fatalf("no committed Stop event in %+v", detail.Events)
	return SessionEvent{}
}

func stopHoldOutcomes(t *testing.T, h *Handler, windowID int) []StopHoldOutcome {
	t.Helper()
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: windowID})
	var out []StopHoldOutcome
	for _, ev := range detail.Events {
		if ev.Event == EventTerminalStopHold {
			out = append(out, ev.StopHoldOutcome)
		}
	}
	return out
}

// TestTerminalStopHold_WorkAfterStopKeepsBolt is the reported bug: Claude fires
// its native Stop hook and then keeps working, so the tab used to alternate
// ⚡🛑⚡🛑 and notify on every false terminal. The held stop must never reach the
// reducer once the next PostToolUse proves the harness is still running.
func TestTerminalStopHold_WorkAfterStopKeepsBolt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, rec := newTerminalHoldTestHandler(t)
		armTerminalHold(t, h)

		time.Sleep(terminalHoldTestDelay / 2)
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "PostToolUse"}); !resp.OK {
			t.Fatalf("PostToolUse response = %+v", resp)
		}
		if _, stillHeld := terminalHoldPending(t, h, 42); stillHeld {
			t.Fatal("continued tool work left the stop addressable")
		}

		// Let the original timer generation fully elapse: a superseded hold must
		// stay inert rather than repaint the stale terminal.
		time.Sleep(2 * terminalHoldTestDelay)
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("icon after superseded hold = %q, want bolt", eff.Icon)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("notifications = %d, want 0 (%+v)", got, rec.snapshot())
		}
		outcomes := stopHoldOutcomes(t, h, 42)
		want := []StopHoldOutcome{StopHoldOutcomeHeld, StopHoldOutcomeSuperseded}
		if len(outcomes) != len(want) {
			t.Fatalf("hold rows = %v, want %v", outcomes, want)
		}
		for i, outcome := range outcomes {
			if outcome != want[i] {
				t.Fatalf("hold rows = %v, want %v", outcomes, want)
			}
		}
	})
}

// TestTerminalStopHold_UncontestedStopCommitsAtDeadline pins the other half of
// the gate. Nothing confirms this window, so the quiet period elapsing is not
// enough: the stop keeps waiting one probe interval at a time and only the hard
// deadline releases it, still painting 🛑 and notifying exactly once.
func TestTerminalStopHold_UncontestedStopCommitsAtDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, rec := newTerminalHoldTestHandler(t)
		armTerminalHold(t, h)

		time.Sleep(terminalHoldTestDelay + time.Second)
		synctest.Wait()
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("icon after the quiet period alone = %q, want bolt: an unconfirmed hold must not commit at delay", eff.Icon)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("notifications after the quiet period = %d, want 0 (%+v)", got, rec.snapshot())
		}

		time.Sleep(terminalHoldTestTimeout)
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon after the deadline = %q, want stop", eff.Icon)
		}
		if got := rec.len(); got != 1 {
			t.Fatalf("notifications = %d, want 1 (%+v)", got, rec.snapshot())
		}
		if _, stillHeld := terminalHoldPending(t, h, 42); stillHeld {
			t.Fatal("committed hold stayed addressable")
		}
		ev := lastStopEvent(t, h, 42)
		if ev.StopHoldOutcome != StopHoldOutcomeDeadline || ev.StopConfirmedBy != StopConfirmedByDeadline {
			t.Fatalf("released stop = outcome %q by %q, want deadline/deadline", ev.StopHoldOutcome, ev.StopConfirmedBy)
		}
		if want := int(terminalHoldTestTimeout.Milliseconds()); ev.StopHoldWaitedMs != want {
			t.Fatalf("stop_hold_waited_ms = %d, want %d", ev.StopHoldWaitedMs, want)
		}
	})
}

// TestTerminalStopHold_ConfirmationBeforeQuietPeriodStillWaits proves the gate
// is an AND and not an OR: idle evidence that lands while the window is still
// inside its quiet period is recorded, but the stop is only released once the
// quiet period itself has elapsed.
func TestTerminalStopHold_ConfirmationBeforeQuietPeriodStillWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, rec := newTerminalHoldTestHandler(t)
		armTerminalHold(t, h)

		time.Sleep(terminalHoldTestDelay / 2)
		if !confirmTerminalHold(t, h, 42, claudeScreenProbeSource) {
			t.Fatal("the confirmation seam did not record idle evidence for the held stop")
		}
		synctest.Wait()
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("icon right after confirmation = %q, want bolt: confirmation alone must not commit", eff.Icon)
		}

		// One instant short of the quiet period the stop is still withheld.
		time.Sleep(terminalHoldTestDelay/2 - time.Millisecond)
		synctest.Wait()
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("icon just before quietUntil = %q, want bolt", eff.Icon)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("notifications before quietUntil = %d, want 0 (%+v)", got, rec.snapshot())
		}

		time.Sleep(time.Millisecond)
		synctest.Wait()
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon at quietUntil = %q, want stop", eff.Icon)
		}
		ev := lastStopEvent(t, h, 42)
		if ev.StopHoldOutcome != StopHoldOutcomeConfirmed || ev.StopConfirmedBy != StopConfirmedByClaudeScreen {
			t.Fatalf("released stop = outcome %q by %q, want confirmed/%s", ev.StopHoldOutcome, ev.StopConfirmedBy, StopConfirmedByClaudeScreen)
		}
		if want := int(terminalHoldTestDelay.Milliseconds()); ev.StopHoldWaitedMs != want {
			t.Fatalf("stop_hold_waited_ms = %d, want %d (released exactly at quietUntil)", ev.StopHoldWaitedMs, want)
		}
	})
}

// TestTerminalStopHold_ConfirmationAfterQuietPeriodCommitsImmediately covers
// the other order: once the quiet period has elapsed there is nothing left to
// wait for, so confirmation must pull the commit in rather than let it drift to
// the next re-arm tick.
func TestTerminalStopHold_ConfirmationAfterQuietPeriodCommitsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, rec := newTerminalHoldTestHandler(t)
		armTerminalHold(t, h)
		start := time.Now()

		time.Sleep(terminalHoldTestDelay + time.Second)
		synctest.Wait()
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("icon while re-armed = %q, want bolt", eff.Icon)
		}

		if !confirmTerminalHold(t, h, 42, claudeScreenProbeSource) {
			t.Fatal("the confirmation seam did not record idle evidence for the re-armed hold")
		}
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon after confirmation = %q, want stop", eff.Icon)
		}
		if got := rec.len(); got != 1 {
			t.Fatalf("notifications = %d, want 1 (%+v)", got, rec.snapshot())
		}
		if waited := time.Since(start); waited >= terminalHoldTestDelay+stopHoldRetryInterval {
			t.Fatalf("confirmed commit landed after %v; it must not wait for the next re-arm tick", waited)
		}
		ev := lastStopEvent(t, h, 42)
		if ev.StopHoldOutcome != StopHoldOutcomeConfirmed || ev.StopConfirmedBy != StopConfirmedByClaudeScreen {
			t.Fatalf("released stop = outcome %q by %q, want confirmed/%s", ev.StopHoldOutcome, ev.StopConfirmedBy, StopConfirmedByClaudeScreen)
		}
	})
}

// TestTerminalStopHold_ProbeConfirmsHeldStop is the feature's headline path: the
// harness's own idle probe — the same evidence that settles a lost-hook stop
// when nothing is held — releases the withheld native stop instead of settling a
// stop of its own. The committed stop keeps its SourceHarness provenance and
// records which probe confirmed it, and no SourceProbe fact is ever created.
func TestTerminalStopHold_ProbeConfirmsHeldStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, rec := newTerminalHoldProbeHandler(t, harness.Claude, claudeIdleScreen)
		base := time.Now()
		armTerminalHold(t, h)

		// Claude requires two agreeing screen reads: one sample confirms nothing.
		h.ProbeClaudeScreenStop(base)
		if pending, ok := terminalHoldPending(t, h, 42); !ok || pending.confirmed {
			t.Fatalf("hold after one sample = %+v/%v, want an unconfirmed hold", pending, ok)
		}

		h.ProbeClaudeScreenStop(base.Add(time.Second))
		pending, ok := terminalHoldPending(t, h, 42)
		if !ok || !pending.confirmed || pending.confirmedBy != StopConfirmedByClaudeScreen {
			t.Fatalf("hold after two samples = %+v/%v, want confirmed by %s", pending, ok, StopConfirmedByClaudeScreen)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("icon while confirmed inside the quiet period = %q, want bolt", eff.Icon)
		}

		time.Sleep(terminalHoldTestDelay)
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon after the confirmed hold committed = %q, want stop", eff.Icon)
		}
		if got := rec.len(); got != 1 {
			t.Fatalf("notifications = %d, want 1 (%+v)", got, rec.snapshot())
		}
		ev := lastStopEvent(t, h, 42)
		if ev.StopHoldOutcome != StopHoldOutcomeConfirmed || ev.StopConfirmedBy != StopConfirmedByClaudeScreen {
			t.Fatalf("released stop = outcome %q by %q, want confirmed/%s", ev.StopHoldOutcome, ev.StopConfirmedBy, StopConfirmedByClaudeScreen)
		}
		detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
		if probe, ok := lastEventNamed(detail.Events, screenStopProbeEvent); ok {
			t.Fatalf("%q row recorded while a native stop was held: %+v", screenStopProbeEvent, probe)
		}
		assertNoProbeStopFact(t, h, 42)
		facts := windowFacts(t, h, 42)
		nativeStop := false
		for _, fact := range facts {
			nativeStop = nativeStop || (fact.Kind == SourceHarness && fact.Action == "stop")
		}
		if !nativeStop {
			t.Fatalf("facts = %+v, want the committed stop to be a %s fact", facts, SourceHarness)
		}
	})
}

// TestConfirmStopHoldLocked_OnlyTerminalHoldsClaimProbeEvidence pins the
// lost-hook non-regression at the seam itself: with nothing armed, or with only
// the reset-class debounce armed, the probe's evidence is not claimed and the
// probe settles its own stop exactly as it did before the gate existed.
func TestConfirmStopHoldLocked_OnlyTerminalHoldsClaimProbeEvidence(t *testing.T) {
	cases := []struct {
		name string
		arm  func(*testing.T, *Handler)
		want bool
	}{
		{name: "no_hold"},
		{
			name: "reset_class_hold",
			arm: func(t *testing.T, h *Handler) {
				h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, false)
				req := Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop", Harness: harness.Claude}
				if !h.holdStopForConfirmation(req, hookevent.HookPayload{}) {
					t.Fatal("reset-sensitive stop was not held")
				}
			},
		},
		{
			name: "terminal_class_hold",
			arm: func(t *testing.T, h *Handler) {
				req := Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop", Harness: harness.Claude}
				if !h.holdStopForConfirmation(req, hookevent.HookPayload{}) {
					t.Fatal("terminal stop was not held")
				}
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTerminalHoldTestHandler(t)
			if tc.arm != nil {
				tc.arm(t, h)
			}
			t.Cleanup(func() {
				if pending, ok := terminalHoldPending(t, h, 42); ok && pending.timer != nil {
					pending.timer.Stop()
				}
			})
			if got := confirmTerminalHold(t, h, 42, claudeScreenProbeSource); got != tc.want {
				t.Fatalf("confirmStopHoldLocked = %v, want %v", got, tc.want)
			}
			if !tc.want {
				return
			}
			pending, _ := terminalHoldPending(t, h, 42)
			if !pending.confirmed || pending.confirmedBy != StopConfirmedByClaudeScreen {
				t.Fatalf("confirmed hold = %+v, want confirmed by %s", pending, StopConfirmedByClaudeScreen)
			}
		})
	}
}

// TestStopConfirmedByProbeCoversEveryIdleProbeSource pins the coupling between
// the three idle probe source keys and the exported release vocabulary: a
// renamed probe source must not silently start writing an unknown
// StopConfirmedBy value into diagnostics.
func TestStopConfirmedByProbeCoversEveryIdleProbeSource(t *testing.T) {
	cases := []struct {
		source SourceID
		want   StopConfirmedBy
	}{
		{SourceID{Kind: SourceProbe, Key: claudeScreenStopProbeSource}, StopConfirmedByClaudeScreen},
		{SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}, StopConfirmedByCodexScreen},
		{SourceID{Kind: SourceProbe, Key: copilotTranscriptStopProbeSource}, StopConfirmedByCopilotTranscript},
	}
	for _, tc := range cases {
		if got := stopConfirmedByProbe(tc.source); got != tc.want {
			t.Fatalf("stopConfirmedByProbe(%+v) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

// TestTerminalStopHold_CommitRefreshesVolatileFields pins the commit-time
// refresh. The request captured when the hold armed can be seconds stale by the
// time the deadline releases it, so the tab title, profile, and mute state the
// stop is committed with must be re-read from the window, not replayed.
func TestTerminalStopHold_CommitRefreshesVolatileFields(t *testing.T) {
	t.Run("title_and_account", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h, rec := newTerminalHoldTestHandler(t)
			armTerminalHold(t, h)
			renameHeldWindow(t, h, 42, "renamed", "other-profile")

			time.Sleep(2 * terminalHoldTestTimeout)
			synctest.Wait()

			calls := rec.snapshot()
			if len(calls) != 1 {
				t.Fatalf("notifications = %d, want 1 (%+v)", len(calls), calls)
			}
			if calls[0].title != "renamed" || calls[0].account != "other-profile" {
				t.Fatalf("notification = %+v, want the refreshed title/profile", calls[0])
			}
			if ev := lastStopEvent(t, h, 42); ev.Profile != "other-profile" {
				t.Fatalf("committed stop profile = %q, want the refreshed other-profile", ev.Profile)
			}
		})
	})

	t.Run("muted", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h, rec := newTerminalHoldTestHandler(t)
			armTerminalHold(t, h)
			h.setMutedFromSnapshot(42, true)

			time.Sleep(2 * terminalHoldTestTimeout)
			synctest.Wait()

			if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
				t.Fatalf("icon after the deadline = %q, want stop even while muted", eff.Icon)
			}
			if got := rec.len(); got != 0 {
				t.Fatalf("notifications = %d, want 0: a window muted during the hold must not notify (%+v)", got, rec.snapshot())
			}
		})
	})
}

// TestTerminalStopHold_ConfirmationDeadlineClaimRace covers the one ordering the
// two-phase timer introduces: the deadline callback has fired and claimed its
// generation when confirmation lands and resets the same timer to zero. Both
// callbacks are then in flight for one generation and exactly one of them may
// commit.
func TestTerminalStopHold_ConfirmationDeadlineClaimRace(t *testing.T) {
	h, _ := newTerminalHoldTestHandler(t)
	// A far-future quiet period keeps the real timer from firing on its own, so
	// the only two commit attempts are the simulated deadline callback and the
	// one confirmation pulls in.
	h.SetStopConfirm(time.Hour, 2*time.Hour, true)
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit"}); !resp.OK {
		t.Fatalf("UserPromptSubmit response = %+v", resp)
	}
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
		t.Fatalf("Stop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, 42)
	if !armed || pending.class != stopHoldClassTerminal {
		t.Fatalf("pending hold = %+v/%v, want an armed terminal hold", pending, armed)
	}
	// Backdate the quiet period so confirmation takes the immediate-commit branch.
	h.stateMu.Lock()
	held := h.pendingStopHolds[42]
	held.quietUntil = time.Now().Add(-time.Second)
	h.pendingStopHolds[42] = held
	h.stateMu.Unlock()

	// Park every callback at the post-claim seam so both are simultaneously
	// between claiming the generation and applying it. Releasing them together is
	// what puts the two commits in a real race for the commit mutex.
	claims := make(chan struct{}, 2)
	resume := make(chan struct{})
	var released sync.WaitGroup
	released.Add(2)
	h.afterDeferredStopClaimForTest = func(_ int, _ uint64) {
		defer released.Done()
		claims <- struct{}{}
		<-resume
	}

	go h.commitStopHold(42, pending.seq)
	awaitStopHoldClaim(t, claims, "deadline callback did not reach the post-claim seam", resume)

	if !confirmTerminalHold(t, h, 42, claudeScreenProbeSource) {
		t.Fatal("the confirmation seam did not record idle evidence against the claimed hold")
	}
	awaitStopHoldClaim(t, claims, "confirmation did not reset the timer into a second claim", resume)

	close(resume)
	released.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, stillHeld := terminalHoldPending(t, h, 42); !stillHeld {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("raced hold stayed addressable")
		}
		time.Sleep(time.Millisecond)
	}
	// Both callbacks are past the seam, but the loser may still be blocked on the
	// commit mutex. Reading the log from inside that mutex — the same
	// resetStopCommitMu-then-stateMu order every commit path takes — guarantees no
	// commit is half-applied while the events are counted.
	h.resetStopCommitMu.Lock()
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	h.resetStopCommitMu.Unlock()

	stops := 0
	for _, ev := range detail.Events {
		if ev.Event == "Stop" {
			stops++
		}
	}
	if stops != 1 {
		t.Fatalf("committed Stop events = %d, want exactly 1 (%+v)", stops, detail.Events)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("icon after the raced commit = %q, want stop", eff.Icon)
	}
}

// TestTerminalStopHold_ProcessExitBypassesGate covers the one release the gate
// must never delay: the process that produced the held stop is itself verifiably
// gone, so the window is terminal no matter what a screen probe would say. That
// exact exit settles the stop directly and retires the hold, and the cancelled
// generation must stay inert for the rest of what would have been its hold
// window.
//
// A second window exits in the same pass having never received a native
// terminal, so the two exported evidence values are discriminated per candidate
// rather than per pass.
func TestTerminalStopHold_ProcessExitBypassesGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, rec := newTerminalHoldTestHandler(t)
		identity := HarnessProcessIdentity{Harness: harness.Claude, PID: 7101, StartToken: "Thu Jul 16 10:20:30 2026"}
		unheld := HarnessProcessIdentity{Harness: harness.Claude, PID: 7102, StartToken: "Thu Jul 16 10:20:31 2026"}
		applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
		applyLifecycleEvent(t, h, 43, unheld, "session-43", "active", 1, false)
		h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{
			statuses: map[HarnessProcessIdentity]HarnessProcessInspection{
				identity: HarnessProcessMissing,
				unheld:   HarnessProcessMissing,
			},
		})

		deliverProcessStop(t, h, 42, identity, "session")

		time.Sleep(terminalHoldTestDelay / 2)
		if committed := h.processLifecycleTick(t.Context(), time.Now()); committed != 2 {
			t.Fatalf("process exit committed = %d, want 2", committed)
		}
		if _, stillHeld := terminalHoldPending(t, h, 42); stillHeld {
			t.Fatal("verified process exit left the held stop addressable")
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon after the exit = %q, want stop, without waiting for the gate", eff.Icon)
		}

		// Ride past the quiet period and the hard deadline the hold would have had.
		time.Sleep(2 * terminalHoldTestTimeout)
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon after the cancelled hold window = %q, want stop", eff.Icon)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("notifications = %d, want 0: a synthetic exit stop is silent (%+v)", got, rec.snapshot())
		}
		outcomes := stopHoldOutcomes(t, h, 42)
		want := []StopHoldOutcome{StopHoldOutcomeHeld, StopHoldOutcomeBypassed}
		if len(outcomes) != len(want) {
			t.Fatalf("hold rows = %v, want %v", outcomes, want)
		}
		for i, outcome := range outcomes {
			if outcome != want[i] {
				t.Fatalf("hold rows = %v, want %v", outcomes, want)
			}
		}
		if settled := settledStopEvents(t, h, 42); settled != 1 {
			t.Fatalf("settled stop events = %d, want exactly 1", settled)
		}
		// The bypassed hold proves the native hook was received even though the
		// exit is what synthesized the reducer stop.
		bypassedExit := lifecycleExitEvent(t, h, 42)
		if bypassedExit.TerminalHookEvidence != processTerminalHookEvidenceObserved ||
			bypassedExit.SynthesizedStop == nil || !*bypassedExit.SynthesizedStop {
			t.Fatalf("bypassed-hold exit = %+v, want observed evidence with a synthesized stop", bypassedExit)
		}
		unheldExit := lifecycleExitEvent(t, h, 43)
		if unheldExit.TerminalHookEvidence != processTerminalHookEvidenceAbsent ||
			unheldExit.SynthesizedStop == nil || !*unheldExit.SynthesizedStop {
			t.Fatalf("exit with no native terminal = %+v, want absent evidence with a synthesized stop", unheldExit)
		}
		terminalFacts := 0
		for _, fact := range windowFacts(t, h, 42) {
			if fact.Action == "stop" {
				terminalFacts++
			}
		}
		if terminalFacts != 1 {
			t.Fatalf("terminal facts = %d, want exactly 1 (%+v)", terminalFacts, windowFacts(t, h, 42))
		}
	})
}

// TestTerminalStopHold_UnrelatedProcessExitPreservesGate covers the exits that
// prove nothing about the held stop. One window routinely carries several
// ordinary process groups at once — an interactive parent plus the `claude -p`
// children it spawned, or a second session sharing the tab — so settling one of
// them says nothing about whether the parent is done. Retiring the hold by
// window ID would paint 🛑 over a session still working, discard the deadline
// that is the parent's only convergence bound, and stamp observed terminal
// evidence on an exit that never received a native hook.
func TestTerminalStopHold_UnrelatedProcessExitPreservesGate(t *testing.T) {
	held := HarnessProcessIdentity{Harness: harness.Claude, PID: 7201, StartToken: "Thu Jul 16 10:20:30 2026"}
	cases := []struct {
		name      string
		identity  HarnessProcessIdentity
		sessionID string
		pipeMode  bool
	}{
		{
			name:      "pipe_child",
			identity:  HarnessProcessIdentity{Harness: harness.Claude, PID: 7202, StartToken: "Thu Jul 16 10:21:30 2026"},
			sessionID: "pipe",
			pipeMode:  true,
		},
		{
			name:      "sibling_session",
			identity:  HarnessProcessIdentity{Harness: harness.Claude, PID: 7203, StartToken: "Thu Jul 16 10:22:30 2026"},
			sessionID: "sibling",
			pipeMode:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, rec := newTerminalHoldTestHandler(t)
				applyLifecycleEvent(t, h, 42, held, "session", "active", 1, false)
				applyLifecycleEvent(t, h, 42, tc.identity, tc.sessionID, "active", 1, tc.pipeMode)
				// Only the unrelated process is gone; the held stop's own process is
				// left unpolled so this pass can never settle it.
				h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{
					statuses: map[HarnessProcessIdentity]HarnessProcessInspection{
						tc.identity: HarnessProcessMissing,
					},
				})

				deliverProcessStop(t, h, 42, held, "session")
				armed, _ := terminalHoldPending(t, h, 42)

				time.Sleep(terminalHoldTestDelay / 2)
				if committed := h.processLifecycleTick(t.Context(), time.Now()); committed != 1 {
					t.Fatalf("unrelated exit committed = %d, want 1", committed)
				}

				pending, stillHeld := terminalHoldPending(t, h, 42)
				if !stillHeld {
					t.Fatal("an unrelated process exit discarded the held stop of a process that is still running")
				}
				if pending.seq != armed.seq ||
					!pending.quietUntil.Equal(armed.quietUntil) || !pending.deadline.Equal(armed.deadline) {
					t.Fatalf("hold after the unrelated exit = %+v, want its original gate %+v", pending, armed)
				}
				if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
					t.Fatalf("icon after the unrelated exit = %q, want bolt: the held session is still working", eff.Icon)
				}
				if outcomes := stopHoldOutcomes(t, h, 42); len(outcomes) != 1 || outcomes[0] != StopHoldOutcomeHeld {
					t.Fatalf("hold rows = %v, want just [%v]: nothing bypassed this gate", outcomes, StopHoldOutcomeHeld)
				}
				// The withheld hook belongs to the surviving process, so it is not
				// evidence about the one that exited.
				exit := lifecycleExitEvent(t, h, 42)
				if exit.TerminalHookEvidence != processTerminalHookEvidenceAbsent {
					t.Fatalf("unrelated exit = %+v, want absent terminal evidence", exit)
				}
				if got := rec.len(); got != 0 {
					t.Fatalf("notifications = %d, want 0 while the stop is still held (%+v)", got, rec.snapshot())
				}

				// The gate still owns the held stop, so it converges on its own hard
				// deadline exactly as if the unrelated exit had never happened.
				time.Sleep(2 * terminalHoldTestTimeout)
				synctest.Wait()
				if _, neverReleased := terminalHoldPending(t, h, 42); neverReleased {
					t.Fatal("the preserved hold never reached its deadline")
				}
				if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
					t.Fatalf("icon after the deadline = %q, want stop", eff.Icon)
				}
			})
		})
	}
}

// settledStopEvents counts the rows that actually painted a stop. The synthetic
// hold rows carry action "stop" too but apply nothing, so they are excluded.
func settledStopEvents(t *testing.T, h *Handler, windowID int) int {
	t.Helper()
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: windowID})
	settled := 0
	for _, ev := range detail.Events {
		if ev.Action == "stop" && ev.Event != EventTerminalStopHold {
			settled++
		}
	}
	return settled
}

func TestSupersedesStopHold(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want bool
	}{
		{"prompt", Request{Action: "active", IsPrompt: true}, true},
		{"user_clear", Request{Action: "user_clear"}, true},
		{"clear", Request{Action: "clear"}, true},
		{"active", Request{Action: "active"}, true},
		{"subagent", Request{Action: "subagent"}, true},
		{"compact", Request{Action: "compact"}, true},
		{"permission", Request{Action: "permission"}, true},
		{"elicitation", Request{Action: "elicitation"}, true},
		{"failure", Request{Action: "failure"}, true},
		{"stop", Request{Action: "stop"}, false},
		{"loop_marker", Request{Action: "loop_marker"}, false},
		// Reset-flow cleanup keeps the hold so the stop can fold into the same
		// user_clear when it commits.
		{"reset_flow_user_clear", Request{Action: "user_clear", ResetFlow: true}, false},
		{"reset_flow_prompt", Request{Action: "active", IsPrompt: true, ResetFlow: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := supersedesStopHold(tc.req); got != tc.want {
				t.Fatalf("supersedesStopHold(%+v) = %v, want %v", tc.req, got, tc.want)
			}
		})
	}
}

// TestTerminalStopHold_SupersededByPromptAndClear drives the two events that
// retire a held terminal stop through the live hook path rather than the
// predicate: a fresh turn and an F8 wipe.
func TestTerminalStopHold_SupersededByPromptAndClear(t *testing.T) {
	for _, event := range []string{"UserPromptSubmit", "user_clear"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, rec := newTerminalHoldTestHandler(t)
				armTerminalHold(t, h)

				time.Sleep(terminalHoldTestDelay / 3)
				if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: event}); !resp.OK {
					t.Fatalf("%s response = %+v", event, resp)
				}

				time.Sleep(2 * terminalHoldTestDelay)
				synctest.Wait()

				if eff, _ := windowEffective(t, h, 42); eff.Icon == "stop" {
					t.Fatalf("%s left the superseded hold free to paint stop", event)
				}
				if got := rec.len(); got != 0 {
					t.Fatalf("notifications = %d, want 0 (%+v)", got, rec.snapshot())
				}
			})
		})
	}
}

// TestTerminalStopHold_BypassMatrix pins the arm-time decisions that keep a
// stop on its pre-gate behaviour. A bypassed stop is not "released
// immediately": a reset-sensitive one still falls back to the reset class,
// exactly as before the gate existed.
func TestTerminalStopHold_BypassMatrix(t *testing.T) {
	baseReq := Request{
		Type:     TypeStateUpdate,
		WindowID: 42,
		Action:   "stop",
		Event:    "Stop",
		Harness:  harness.Claude,
	}
	cases := []struct {
		name      string
		handler   func(*Handler)
		mutate    func(*Request)
		wantHeld  bool
		wantClass stopHoldClass
	}{
		{
			name:      "armable",
			wantHeld:  true,
			wantClass: stopHoldClassTerminal,
		},
		{
			name:      "stop_confirm_disabled",
			handler:   func(h *Handler) { h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, false) },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:      "zero_delay",
			handler:   func(h *Handler) { h.SetStopConfirm(0, terminalHoldTestTimeout, true) },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:      "no_kitty_client",
			handler:   func(h *Handler) { h.SetKittyClient(nil) },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:      "pipe_mode",
			mutate:    func(req *Request) { req.PipeMode = true },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:      "harness_without_stop_probe",
			mutate:    func(req *Request) { req.Harness = "" },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:     "context_limit",
			mutate:   func(req *Request) { req.ContextLimit = true },
			wantHeld: false,
		},
		{
			name:     "reset_flow",
			mutate:   func(req *Request) { req.ResetFlow = true },
			wantHeld: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTerminalHoldTestHandler(t)
			if tc.handler != nil {
				tc.handler(h)
			}
			req := baseReq
			if tc.mutate != nil {
				tc.mutate(&req)
			}

			held := h.holdStopForConfirmation(req, hookevent.HookPayload{})
			pending, armed := terminalHoldPending(t, h, 42)
			if armed && pending.timer != nil {
				pending.timer.Stop()
			}
			if held != tc.wantHeld || armed != tc.wantHeld {
				t.Fatalf("held = %v (pending %v), want %v", held, armed, tc.wantHeld)
			}
			if tc.wantHeld && pending.class != tc.wantClass {
				t.Fatalf("hold class = %v, want %v", pending.class, tc.wantClass)
			}
		})
	}
}

// TestTerminalStopHold_LiveASLLeaseBypassesTerminalClass covers the remaining
// bypass, which needs a real lease rather than a synthesized request: Reduce
// already masks "stop" while a loop lease is live, so holding would only add
// latency for an identical icon.
func TestTerminalStopHold_LiveASLLeaseBypassesTerminalClass(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	h.SetKittyClient(&fakeKittyClient{windows: windows})
	stubDaemonLoopDiscover(t, nil, nil)

	aslLifecycleHookRequest(t, h, "AgentSessionLoopStarted", projectDir, hookevent.HookPayload{RunID: "run-1"})
	if eff, _ := windowEffective(t, h, 42); !eff.Looping {
		t.Fatalf("effective = %+v, want a live loop lease before the stop", eff)
	}

	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
		t.Fatalf("Stop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, 42)
	if armed && pending.timer != nil {
		pending.timer.Stop()
	}
	if !armed || pending.class != stopHoldClassReset {
		t.Fatalf("pending hold = %+v/%v, want the reset class under a live lease", pending, armed)
	}
	if rows := stopHoldOutcomes(t, h, 42); len(rows) != 0 {
		t.Fatalf("hold rows = %v, want none for a bypassed stop", rows)
	}
}

// TestTerminalStopHold_ShadowSeqBumpsOncePerHold pins the receipt contract:
// arming withholds a real transition and must invalidate off-lock screen reads
// once, but a re-arm during the same hold must not discard the idle evidence
// the gate is accumulating.
func TestTerminalStopHold_ShadowSeqBumpsOncePerHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _ := newTerminalHoldTestHandler(t)
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit"}); !resp.OK {
			t.Fatalf("UserPromptSubmit response = %+v", resp)
		}
		before := h.captureWindowReceipt(42)

		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
			t.Fatalf("first Stop response = %+v", resp)
		}
		armed := h.captureWindowReceipt(42)
		if armed != before+1 {
			t.Fatalf("receipt after arm = %d, want %d", armed, before+1)
		}
		first, ok := terminalHoldPending(t, h, 42)
		if !ok {
			t.Fatal("first Stop was not held")
		}

		time.Sleep(terminalHoldTestDelay / 3)
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
			t.Fatalf("second Stop response = %+v", resp)
		}
		if got := h.captureWindowReceipt(42); got != armed {
			t.Fatalf("receipt after re-arm = %d, want %d unchanged", got, armed)
		}
		second, ok := terminalHoldPending(t, h, 42)
		if !ok {
			t.Fatal("re-arm dropped the hold")
		}
		if second.seq == first.seq {
			t.Fatalf("re-arm reused generation %d; the superseded timer must be unable to commit", first.seq)
		}
		if !second.armedAt.Equal(first.armedAt) {
			t.Fatalf("re-arm reset armedAt from %v to %v; the window has been withholding continuously", first.armedAt, second.armedAt)
		}
		if rows := stopHoldOutcomes(t, h, 42); len(rows) != 1 || rows[0] != StopHoldOutcomeHeld {
			t.Fatalf("hold rows = %v, want exactly one held row across arm and re-arm", rows)
		}

		time.Sleep(2 * terminalHoldTestDelay)
		synctest.Wait()
	})
}
