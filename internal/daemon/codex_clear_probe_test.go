package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeCodexScreenPrompts_ClearScreenClearsStoppedCodexTab(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "stop", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getTextByExtent: map[getTextKey]string{
			{match: kitty.MatchWindowByID(42), extent: "screen"}: codexReportedClearedScreen,
			{match: kitty.MatchWindowByID(42), extent: "all"}:    codexReportedClearedScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(42, "stop", false)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(42) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(42))
	}
	if want := palette.BuildTitle("W:api", "", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if got := h.guard.Current(42); got != "" {
		t.Fatalf("guard.Current = %q, want empty after user_clear", got)
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.Event != screenClearProbeEvent || ev.Source != screenPromptProbeSource {
		t.Fatalf("event identity = (%q, %q), want (%q, %q)", ev.Event, ev.Source, screenClearProbeEvent, screenPromptProbeSource)
	}
	if ev.Action != "user_clear" || !ev.ResetFlow {
		t.Fatalf("event action/reset_flow = (%q, %v), want (user_clear, true)", ev.Action, ev.ResetFlow)
	}
	if ev.ResultIconState != "" || ev.CurrentIconState != "stop" {
		t.Fatalf("event icons = current %q result %q, want stop -> empty", ev.CurrentIconState, ev.ResultIconState)
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if meta.Durable.Harness != "codex" || meta.Durable.Profile != "work" || meta.Durable.SessionTitle != "W:api" {
		t.Fatalf("durable metadata = %+v, want codex/work/W:api", meta.Durable)
	}
}

func TestProbeCodexScreenPrompts_ClearScreenKeepsStopWithTranscriptScrollback(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "stop", false), kitty.UserVars{
			Harness: "codex",
		}),
		getTextByExtent: map[getTextKey]string{
			{match: kitty.MatchWindowByID(42), extent: "screen"}: codexClearedScreen,
			{match: kitty.MatchWindowByID(42), extent: "all"}:    codexClearedScreenWithTranscript,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(42, "stop", false)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if got := h.guard.Current(42); got != "stop" {
		t.Fatalf("guard.Current = %q, want stop", got)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

func TestCodexClearProbeDoesNotJournalUnderStateLock(t *testing.T) {
	notifier := notification.NewNotifier(&config.NotificationConfig{
		Permission:           true,
		PermissionDebounceMs: 60_000,
	})
	h := NewHandler(NewStateGuard(time.Hour), notifier)
	notifier.MaybeNotifyTraced(42, "W:api", "work", "permission", false, notification.NotificationTrace{
		TraceID: "trace-codex-clear-pending",
	})

	observerStarted := make(chan notification.NotificationDisposition, 1)
	releaseObserver := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseObserver)
		}
	}()
	notifier.SetObserver(func(disposition notification.NotificationDisposition) {
		observerStarted <- disposition
		<-releaseObserver
	})

	capturedSeq := h.captureWindowReceipt(42)
	parts := palette.ParseTitleParts(palette.BuildTitle("W:api", "stop", false))
	clearDone := make(chan struct{})
	go func() {
		h.applyCodexClearObservation(
			time.Unix(1_700_000_000, 0),
			directCodexProbeBinding(42, capturedSeq, 0),
			parts,
			kitty.UserVars{Profile: "work", Harness: "codex"},
		)
		close(clearDone)
	}()

	select {
	case disposition := <-observerStarted:
		if disposition.Outcome != "cancelled" || disposition.Reason != "superseded" {
			t.Fatalf("disposition = %+v, want cancelled/superseded", disposition)
		}
	case <-time.After(time.Second):
		t.Fatal("pending-notification cancellation observer was not invoked")
	}

	stateLockAcquired := make(chan struct{})
	go func() {
		h.stateMu.Lock()
		_ = len(h.store) // Protected observation proves the lock is available.
		h.stateMu.Unlock()
		close(stateLockAcquired)
	}()
	select {
	case <-stateLockAcquired:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("notification disposition observer held stateMu during Codex clear")
	}

	close(releaseObserver)
	released = true
	select {
	case <-clearDone:
	case <-time.After(time.Second):
		t.Fatal("Codex clear did not finish after disposition observer was released")
	}
}

// TestProbeCodexClearWindow_CancelsDeferredStopGenerationSafely covers the
// ordering where a reset-sensitive Stop has been acknowledged into the
// debounce map but has not reached the reducer. A receipt-valid clear must
// remove that older timer atomically; conversely, a Stop accepted after the
// clear's receipt was captured must invalidate the clear and retain its own
// pending generation.
func TestProbeCodexClearWindow_CancelsDeferredStopGenerationSafely(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 80 * time.Millisecond
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	synctest.Test(t, func(t *testing.T) {
		// Receipt-valid clear cancels an older pending stop.
		h := newTestHandler()
		h.SetStopConfirm(0, 0, false) // exercise the reset hold class, not the terminal one
		base := time.Now()
		fake := &fakeKittyClient{
			windows: codexProbeWindows(42, palette.BuildTitle("W:api", "stop", false), kitty.UserVars{
				Profile: "work",
				Harness: "codex",
			}),
			getTextByExtent: map[getTextKey]string{
				{match: kitty.MatchWindowByID(42), extent: "screen"}: codexReportedClearedScreen,
				{match: kitty.MatchWindowByID(42), extent: "all"}:    codexReportedClearedScreen,
			},
		}
		h.SetKittyClient(fake)

		if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
			t.Fatalf("older Stop response = %+v", resp)
		}
		h.stateMu.Lock()
		pendingBefore, armed := h.pendingStopHolds[42]
		stopReceipt := h.windowReceiptLocked(42)
		h.stateMu.Unlock()
		if !armed || pendingBefore.timer == nil {
			t.Fatalf("older pending reset stop = %+v, want armed timer", pendingBefore)
		}
		if stopReceipt == 0 {
			t.Fatal("deferred Stop did not advance the window receipt")
		}

		h.ProbeCodexScreenPrompts(base)

		h.stateMu.Lock()
		_, stillPending := h.pendingStopHolds[42]
		st := h.store[42]
		h.stateMu.Unlock()
		if stillPending {
			t.Fatal("receipt-valid Codex clear left the older deferred Stop pending")
		}
		if st == nil || st.PendingReset != state.ResetFlowNone || st.Effective.Icon != "" {
			t.Fatalf("state after clear = %+v, want cleared reducer and reset flow", st)
		}
		beforeTimer := screenProbeMutationFingerprint(t, h, 42)
		titleCallsBefore := len(fake.titleCalls())
		userVarsCallsBefore := len(fake.userVarsCalls())

		// Release the original timer generation. Timer.Stop may race with an
		// already queued callback, so the deleted map entry is the final guard.
		time.Sleep(resetFlowStopDelay)
		synctest.Wait()

		if afterTimer := screenProbeMutationFingerprint(t, h, 42); afterTimer != beforeTimer {
			t.Fatalf("canceled deferred Stop mutated state after clear:\n before %s\n after  %s", beforeTimer, afterTimer)
		}
		if got := len(fake.titleCalls()); got != titleCallsBefore {
			t.Fatalf("late SetTabTitle calls = %d, want %d", got, titleCallsBefore)
		}
		if got := len(fake.userVarsCalls()); got != userVarsCallsBefore {
			t.Fatalf("late SetUserVars calls = %d, want %d", got, userVarsCallsBefore)
		}
		detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if len(detail.Events) != 1 || detail.Events[0].Event != screenClearProbeEvent || detail.Events[0].ResultIconState != "" {
			t.Fatalf("events after canceled older timer = %+v, want only the cleared-screen audit", detail.Events)
		}

		// A clear captured before another deferred stop is stale and must leave
		// that newer pending generation intact.
		h = newTestHandler()
		h.SetStopConfirm(0, 0, false) // exercise the reset hold class, not the terminal one
		fake = &fakeKittyClient{windows: codexProbeWindows(42, palette.BuildTitle("W:api", "stop", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		})}
		h.SetKittyClient(fake)
		capturedSeq := h.captureWindowReceipt(42)

		if resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
			t.Fatalf("newer Stop response = %+v", resp)
		}
		h.stateMu.Lock()
		newerPending, armed := h.pendingStopHolds[42]
		h.stateMu.Unlock()
		if !armed || newerPending.timer == nil {
			t.Fatalf("newer pending reset stop = %+v, want armed timer", newerPending)
		}

		parts := palette.ParseTitleParts(palette.BuildTitle("W:api", "stop", false))
		h.applyCodexClearObservation(time.Now(), directCodexProbeBinding(42, capturedSeq, 0), parts, kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		})

		h.stateMu.Lock()
		pendingAfter, stillPending := h.pendingStopHolds[42]
		h.stateMu.Unlock()
		if !stillPending || pendingAfter.seq != newerPending.seq {
			t.Fatalf("pending after stale clear = %+v/%v, want newer generation %d retained", pendingAfter, stillPending, newerPending.seq)
		}

		time.Sleep(resetFlowStopDelay)
		synctest.Wait()
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective after newer timer = %+v, want stop", eff)
		}
		detail = h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if len(detail.Events) != 1 || detail.Events[0].Event != "Stop" || detail.Events[0].ResultIconState != "stop" {
			t.Fatalf("events after newer timer = %+v, want only committed Stop", detail.Events)
		}
	})
}

// TestCodexClearDeferredStopClaimRace covers the timer ordering that remains
// after ordinary pending-timer cancellation: the callback has fired and
// claimed its generation but has not applied the Stop yet. A receipt-valid
// clear must be able to cancel that claim, and resuming the callback must not
// append a late Stop audit row or advance the render/state generation.
func TestCodexClearDeferredStopClaimRace(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = time.Hour
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	h := newTestHandler()
	claimed := make(chan struct{})
	resume := make(chan struct{})
	h.afterDeferredStopClaimForTest = func(windowID int, seq uint64) {
		if windowID != 42 || seq == 0 {
			t.Errorf("claimed deferred Stop = window %d seq %d, want window 42 and nonzero seq", windowID, seq)
		}
		close(claimed)
		<-resume
	}

	req := protocol.Request{
		Type:     protocol.TypeStateUpdate,
		WindowID: 42,
		Action:   "stop",
		Event:    "Stop",
		Harness:  "codex",
	}
	if !h.holdStopForConfirmation(req, hookevent.HookPayload{}) {
		t.Fatal("reset-sensitive Stop was not deferred")
	}
	h.stateMu.Lock()
	pending, armed := h.pendingStopHolds[42]
	if armed && pending.timer != nil {
		pending.timer.Stop()
	}
	h.stateMu.Unlock()
	if !armed || pending.seq == 0 {
		t.Fatalf("pending reset Stop = %+v/%v, want armed generation", pending, armed)
	}

	commitDone := make(chan struct{})
	go func() {
		defer close(commitDone)
		h.commitStopHold(req.WindowID, pending.seq)
	}()
	select {
	case <-claimed:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("deferred Stop callback did not reach the post-claim seam")
	}

	// Claiming alone does not move the receipt, so the clear captured after the
	// claim remains current and can win the final commit boundary.
	capturedSeq := h.captureWindowReceipt(42)
	parts := palette.ParseTitleParts(palette.BuildTitle("W:api", "stop", false))
	h.applyCodexClearObservation(time.Unix(1_700_000_000, 0), directCodexProbeBinding(42, capturedSeq, 0), parts, kitty.UserVars{
		Profile: "work",
		Harness: "codex",
	})

	h.stateMu.Lock()
	_, stillPending := h.pendingStopHolds[42]
	st := h.store[42]
	h.stateMu.Unlock()
	if stillPending {
		close(resume)
		t.Fatal("receipt-valid Codex clear left the claimed deferred Stop addressable")
	}
	if st == nil || st.PendingReset != state.ResetFlowNone || st.Effective.Icon != "" {
		close(resume)
		t.Fatalf("state after clear = %+v, want cleared reducer and reset flow", st)
	}
	beforeResume := screenProbeMutationFingerprint(t, h, 42)

	close(resume)
	select {
	case <-commitDone:
	case <-time.After(time.Second):
		t.Fatal("deferred Stop callback did not return after its claim was canceled")
	}

	if afterResume := screenProbeMutationFingerprint(t, h, 42); afterResume != beforeResume {
		t.Fatalf("canceled claimed Stop mutated state after clear:\n before %s\n after  %s", beforeResume, afterResume)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Event != screenClearProbeEvent || detail.Events[0].ResultIconState != "" {
		t.Fatalf("events after canceled claimed Stop = %+v, want only cleared-screen audit", detail.Events)
	}
}
