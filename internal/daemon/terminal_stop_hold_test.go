package daemon

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

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
