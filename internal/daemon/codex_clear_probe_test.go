package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
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

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

func TestCodexClearProbeDoesNotJournalUnderStateLock(t *testing.T) {
	notifier := NewNotifier(&config.NotificationConfig{
		Permission:           true,
		PermissionDebounceMs: 60_000,
	})
	h := NewHandler(NewStateGuard(time.Hour), notifier)
	notifier.MaybeNotifyTraced(42, "W:api", "work", "permission", false, NotificationTrace{
		TraceID: "trace-codex-clear-pending",
	})

	observerStarted := make(chan NotificationDisposition, 1)
	releaseObserver := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseObserver)
		}
	}()
	notifier.SetObserver(func(disposition NotificationDisposition) {
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

		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
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
		if st == nil || st.PendingReset != resetFlowNone || st.Effective.Icon != "" {
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
		detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
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
		detail = h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

	req := Request{
		Type:     TypeStateUpdate,
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
	if st == nil || st.PendingReset != resetFlowNone || st.Effective.Icon != "" {
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
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Event != screenClearProbeEvent || detail.Events[0].ResultIconState != "" {
		t.Fatalf("events after canceled claimed Stop = %+v, want only cleared-screen audit", detail.Events)
	}
}

// TestProbeCodexClearWindow_DiscardsSnapshotPredatingReducerEvent closes the
// earlier race test's missing interval: eligibility came from a cached stopped
// title, then a prompt or protected terminal landed before the per-window
// clear probe began. The snapshot receipt must reject that route before either
// screen read and leave every state family exactly as the newer event set it.
func TestProbeCodexClearWindow_DiscardsSnapshotPredatingReducerEvent(t *testing.T) {
	cases := []struct {
		name     string
		action   string
		event    string
		wantIcon string
	}{
		{name: "new prompt", action: "active", event: "UserPromptSubmit", wantIcon: "bolt"},
		{name: "protected terminal", action: "stop_failure", event: "Stop", wantIcon: "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			base := time.Unix(1_700_000_000, 0)
			windows := codexProbeWindows(42, palette.BuildTitle("W:api", "stop", false), kitty.UserVars{
				Profile: "work",
				Harness: "codex",
			})
			fake := &fakeKittyClient{
				windows: windows,
				getTextByExtent: map[getTextKey]string{
					{match: kitty.MatchWindowByID(42), extent: "screen"}: codexReportedClearedScreen,
					{match: kitty.MatchWindowByID(42), extent: "all"}:    codexReportedClearedScreen,
				},
			}
			h.SetKittyClient(fake)
			h.publishSnapshot(windows, base)
			snapshot, ready := h.cachedScreenProbeSnapshot()
			if !ready {
				t.Fatal("cached probe snapshot is not ready")
			}

			codexInjectSessionEvent(t, h, 42, "new", tc.action, tc.event, base.Unix()+1)
			reads := 0
			fake.beforeGetText = func(string, string) { reads++ }
			before := screenProbeMutationFingerprint(t, h, 42)
			h.probeCodexScreenPromptsWithSnapshot(base.Add(time.Second), snapshot)

			if reads != 0 {
				t.Fatalf("GetText calls = %d, want 0 for stale stopped snapshot", reads)
			}
			if after := screenProbeMutationFingerprint(t, h, 42); after != before {
				t.Fatalf("state mutated after stale clear snapshot:\n before %s\n after  %s", before, after)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.wantIcon {
				t.Fatalf("effective = %+v, want newer %q preserved", eff, tc.wantIcon)
			}
		})
	}
}

// TestProbeCodexClearWindow_DiscardsObservationAfterConcurrentReducerEvent
// pins both sides of the two-read receipt boundary: the prompt case lands on
// the first read (so capture must happen before it), while the protected error
// lands on the second read (so application must re-check after both). A stale
// clear is a complete no-op across reducer, guard, audit, mute, notification,
// persistence, and renderer state.
func TestProbeCodexClearWindow_DiscardsObservationAfterConcurrentReducerEvent(t *testing.T) {
	type mutationSnapshot struct {
		shadowSeq           uint64
		stateSeq            uint64
		queuedGen           uint64
		effectiveGen        uint64
		eventSeq            uint64
		eventCount          int
		reducerTraceCount   int
		factCount           int
		durableActivityAt   int64
		durableProfile      string
		durableLastAction   string
		effectiveIcon       string
		lastSessionTitle    string
		lastAccount         string
		guard               string
		pendingReset        resetFlowState
		permissionState     copilotToolPermissionState
		dirty               bool
		muted               bool
		renderPending       bool
		pendingNotification bool
	}

	cases := []struct {
		name       string
		injectRead int
		action     string
		event      string
		prompt     bool
		wantIcon   string
		wantGuard  string
	}{
		{
			name:       "prompt during first read",
			injectRead: 1,
			action:     "active",
			event:      "UserPromptSubmit",
			prompt:     true,
			wantIcon:   "bolt",
			wantGuard:  "active",
		},
		{
			name:       "protected terminal during second read",
			injectRead: 2,
			action:     "stop_failure",
			event:      "Stop",
			wantIcon:   "error",
			wantGuard:  "stop_failure",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			no := false
			notifier, rec := newTestNotifier(&config.NotificationConfig{
				Permission:           true,
				PermissionDebounceMs: 60_000,
				StopFailure:          &no,
			})
			h := NewHandler(NewStateGuard(time.Hour), notifier)
			parts := palette.ParseTitleParts(palette.BuildTitle("W:api", "stop", false))
			userVars := kitty.UserVars{Profile: "work", Harness: "codex"}
			fake := &fakeKittyClient{
				getTextByExtent: map[getTextKey]string{
					{match: kitty.MatchWindowByID(42), extent: "screen"}: codexReportedClearedScreen,
					{match: kitty.MatchWindowByID(42), extent: "all"}:    codexReportedClearedScreen,
				},
			}
			h.SetKittyClient(fake)
			h.guard.TryUpdate(42, "stop", false)
			h.stateMu.Lock()
			h.ensureWindowStateLocked(42).Muted = true
			h.stateMu.Unlock()

			// A stale user_clear would cancel this pending permission. The prompt
			// injection itself does not, so that case directly proves the clear
			// made no notification mutation.
			if tc.prompt {
				h.maybeNotify(42, "W:api", "work", "permission", false)
			}

			snapshot := func() mutationSnapshot {
				var got mutationSnapshot
				h.stateMu.Lock()
				st := h.store[42]
				got.shadowSeq = st.ShadowSeq
				got.stateSeq = st.Seq
				got.queuedGen = st.QueuedGen
				got.effectiveGen = st.EffectiveGen
				got.eventSeq = h.eventSeq
				got.eventCount = len(st.EventLog)
				got.effectiveIcon = st.Effective.Icon
				got.lastSessionTitle = st.LastSessionTitle
				got.lastAccount = st.LastAccount
				got.pendingReset = st.PendingReset
				got.permissionState = st.CopilotToolPermissionState
				got.dirty = st.Dirty
				got.muted = st.Muted
				if st.Reducer != nil {
					got.reducerTraceCount = len(st.Reducer.traces)
					got.factCount = len(st.Reducer.facts)
				}
				meta := h.windowMetadata[42]
				got.durableActivityAt = meta.Durable.ActivityAt
				got.durableProfile = meta.Durable.Profile
				got.durableLastAction = meta.Durable.LastAction
				h.stateMu.Unlock()

				got.guard = h.guard.Current(42)
				h.renderQueue.mu.Lock()
				got.renderPending = h.renderQueue.pending[42]
				h.renderQueue.mu.Unlock()
				notifier.mu.Lock()
				_, got.pendingNotification = notifier.pending[42]
				notifier.mu.Unlock()
				return got
			}

			var afterInjection mutationSnapshot
			var reads int
			injected := false
			fake.beforeGetText = func(match, extent string) {
				if match != kitty.MatchWindowByID(42) {
					return
				}
				reads++
				if injected || reads != tc.injectRead {
					return
				}
				injected = true
				ev := NormalizedEvent{
					Source:      SourceID{Kind: SourceHarness, Key: "codex:sess-new"},
					Action:      tc.action,
					SessionID:   "sess-new",
					At:          time.Now().Unix(),
					Event:       tc.event,
					Prompt:      tc.prompt,
					EndsSession: tc.action == "stop_failure",
				}
				resp := h.handleStateUpdate(Request{
					Type:         TypeStateUpdate,
					WindowID:     42,
					Action:       tc.action,
					Event:        tc.event,
					IsPrompt:     tc.prompt,
					Account:      "work",
					Harness:      "codex",
					SessionTitle: "W:api",
					normalized:   &ev,
				})
				if !resp.OK {
					t.Fatalf("concurrent %s rejected: %+v", tc.action, resp)
				}
				afterInjection = snapshot()
			}

			h.probeCodexClearWindow(time.Now(), directCodexProbeBinding(42, h.captureWindowReceipt(42), 0), parts, userVars)
			defer h.cancelPendingNotification(42)

			if !injected || reads != 2 {
				t.Fatalf("injected=%v reads=%d, want true/2", injected, reads)
			}
			if got := snapshot(); got != afterInjection {
				t.Fatalf("state mutated after stale clear:\n got  %+v\n want %+v", got, afterInjection)
			}
			if got := h.guard.Current(42); got != tc.wantGuard {
				t.Fatalf("guard.Current = %q, want concurrent %q", got, tc.wantGuard)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.wantIcon {
				t.Fatalf("effective = %+v, want concurrent %q preserved", eff, tc.wantIcon)
			}

			h.stateMu.Lock()
			fact := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "codex:sess-new"}]
			events := append([]SessionEvent(nil), h.store[42].EventLog...)
			h.stateMu.Unlock()
			if fact == nil || fact.acked {
				t.Fatalf("concurrent fact = %+v, want live and unacknowledged", fact)
			}
			for _, ev := range events {
				if ev.Event == screenClearProbeEvent {
					t.Fatalf("stale clear recorded an audit row: %+v", ev)
				}
			}
			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0 from discarded clear", got)
			}
			if got := len(fake.userVarsCalls()); got != 0 {
				t.Fatalf("SetUserVars calls = %d, want 0 from discarded clear", got)
			}
			if got := rec.len(); got != 0 {
				t.Fatalf("dispatches = %d, want 0", got)
			}
		})
	}
}

func TestProbeCodexScreenPrompts_ManagedOwnerSuppressesUnmanagedCodexSiblingClearProbe(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("W:api", "stop", false),
			kitty.Window{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
			},
			kitty.Window{
				ID:       43,
				IsActive: true,
				ForegroundProcesses: []kitty.ForegroundProcess{
					{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
				},
			},
		),
		getTextByExtent: map[getTextKey]string{
			{match: kitty.MatchWindowByID(43), extent: "screen"}: codexReportedClearedScreen,
			{match: kitty.MatchWindowByID(43), extent: "all"}:    codexReportedClearedScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(43, "stop", false)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if got := h.guard.Current(43); got != "stop" {
		t.Fatalf("sibling guard.Current = %q, want stop", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(resp.Events) != 0 {
		t.Fatalf("sibling events = %d, want 0", len(resp.Events))
	}
}
