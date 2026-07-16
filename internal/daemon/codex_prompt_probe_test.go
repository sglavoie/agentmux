package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeCodexScreenPrompts_EditPromptReplacesKeyWithQuestion(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "key", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexEditApprovalScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(42, "permission", true)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeCodexScreenPrompts(now)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(42) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(42))
	}
	if want := palette.BuildTitle("W:api", "question", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if got := h.guard.Current(42); got != "elicitation" {
		t.Fatalf("guard.Current = %q, want elicitation", got)
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.Event != screenPromptProbeEvent || ev.Source != screenPromptProbeSource {
		t.Fatalf("event identity = (%q, %q), want (%q, %q)", ev.Event, ev.Source, screenPromptProbeEvent, screenPromptProbeSource)
	}
	if ev.PromptKind != string(CodexPromptEditApproval) || ev.Action != "elicitation" {
		t.Fatalf("event prompt/action = (%q, %q), want (%q, elicitation)", ev.PromptKind, ev.Action, CodexPromptEditApproval)
	}
	if ev.ResultIconState != "question" || ev.CurrentIconState != "key" {
		t.Fatalf("event icons = current %q result %q, want key -> question", ev.CurrentIconState, ev.ResultIconState)
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if meta.Durable.Harness != "codex" || meta.Durable.Profile != "work" || meta.Durable.LastAction != "elicitation" {
		t.Fatalf("durable metadata = %+v, want codex/work/elicitation", meta.Durable)
	}
}

func TestProbeCodexScreenPrompts_CommandPromptStaysKey(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexCommandApprovalScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "key", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.PromptKind != string(CodexPromptCommandApproval) || ev.Action != "permission" || ev.ResultIconState != "key" {
		t.Fatalf("event = %+v, want command permission key", ev)
	}
}

func TestProbeCodexScreenPrompts_RequestUserInputReplacesBoltWithQuestion(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "question", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if got := h.guard.Current(42); got != "elicitation" {
		t.Fatalf("guard.Current = %q, want elicitation", got)
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.PromptKind != string(CodexPromptRequestUserInput) || ev.Action != "elicitation" {
		t.Fatalf("event prompt/action = (%q, %q), want (%q, elicitation)", ev.PromptKind, ev.Action, CodexPromptRequestUserInput)
	}
	if ev.ResultIconState != "question" || ev.CurrentIconState != "bolt" {
		t.Fatalf("event icons = current %q result %q, want bolt -> question", ev.CurrentIconState, ev.ResultIconState)
	}
}

func TestProbeCodexScreenPrompts_UnmanagedCodexForegroundReplacesBoltWithQuestion(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("W:api", "bolt", false),
			[]string{"/opt/homebrew/bin/codex", "--yolo"},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "question", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if got := h.guard.Current(42); got != "elicitation" {
		t.Fatalf("guard.Current = %q, want elicitation", got)
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.PromptKind != string(CodexPromptRequestUserInput) || ev.ResultIconState != "question" {
		t.Fatalf("event = %+v, want request_user_input/question", ev)
	}
	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	lastHarness := h.store[42].LastHarness
	h.stateMu.Unlock()
	if meta.Durable.Harness != harness.Codex || lastHarness != harness.Codex {
		t.Fatalf("ownerless Codex identity = durable %q LastHarness %q, want explicit codex", meta.Durable.Harness, lastHarness)
	}
}

func TestProbeCodexScreenPrompts_ManagedOwnerSuppressesUnmanagedCodexSiblingNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{
			windows: codexProbeSplitWindows(
				palette.BuildTitle("W:api", "bolt", false),
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
			getText: map[string]string{
				kitty.MatchWindowByID(43): codexRequestUserInputScreen,
			},
		}
		h.SetKittyClient(fake)
		h.guard.TryUpdate(43, "active", false)

		h.ProbeCodexScreenPrompts(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("dispatches = %d; want 0 from unmanaged sibling", got)
		}
		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d, want 0", got)
		}
		resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
		if len(resp.Events) != 0 {
			t.Fatalf("sibling events = %d, want 0", len(resp.Events))
		}
		if got := h.guard.Current(43); got != "active" {
			t.Fatalf("sibling guard.Current = %q, want active", got)
		}
	})
}

// TestProbeCodexScreenPrompts_DetectedPromptNotifiesOnceOnTransition pins the
// probe notification path: a screen-probe-detected prompt (the probe exists
// precisely because Codex hooks miss these) must fire exactly one macOS
// notification on the state transition, and a second tick with the prompt
// still visible must not re-dispatch.
func TestProbeCodexScreenPrompts_DetectedPromptNotifiesOnceOnTransition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{
			windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
				Profile: "work",
				Harness: "codex",
			}),
			getText: map[string]string{
				kitty.MatchWindowByID(42): codexRequestUserInputScreen,
			},
		}
		h.SetKittyClient(fake)
		h.guard.TryUpdate(42, "active", false)

		now := time.Now()
		h.ProbeCodexScreenPrompts(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("first probe dispatches = %d; want 1", got)
		}
		call := rec.snapshot()[0]
		if call.windowID != 42 || call.account != "work" || call.title != "W:api" {
			t.Fatalf("dispatch = %+v, want window 42 / work / W:api", call)
		}
		if call.subtitle != "User input requested" {
			t.Fatalf("dispatch subtitle = %q, want %q", call.subtitle, "User input requested")
		}

		// Second tick: same prompt still on screen, tab title now shows ❓ —
		// no transition, so no additional dispatch.
		fake.mu.Lock()
		fake.windows[0].Tabs[0].Title = palette.BuildTitle("W:api", "question", false)
		fake.mu.Unlock()
		h.ProbeCodexScreenPrompts(now.Add(2 * time.Second))
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("second probe total dispatches = %d; want 1 (no per-tick spam)", got)
		}
	})
}

// TestProbeCodexScreenPrompts_DetectedPromptMutedWindowNoDispatch verifies the
// probe notification honors the mute gate: the icon still swaps, but the
// notifier's isMuted closure (wired to Handler.IsMuted, as in daemon.Run)
// suppresses the dispatch.
func TestProbeCodexScreenPrompts_DetectedPromptMutedWindowNoDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := NewHandler(guard, notifier)
		notifier.SetIsMuted(h.IsMuted)
		fake := &fakeKittyClient{
			windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
				Profile: "work",
				Harness: "codex",
			}),
			getText: map[string]string{
				kitty.MatchWindowByID(42): codexRequestUserInputScreen,
			},
		}
		h.SetKittyClient(fake)
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Muted = true
		h.stateMu.Unlock()

		h.ProbeCodexScreenPrompts(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("muted window dispatches = %d; want 0", got)
		}
		if got := len(fake.titleCalls()); got != 1 {
			t.Fatalf("SetTabTitle calls = %d, want 1 (icon swap must still happen)", got)
		}
	})
}

// TestProbeCodexScreenPrompts_DiscardsStaleMuteSnapshot pins the Kitty
// snapshot-generation boundary independently of the reducer receipt. A newer
// mute-only snapshot leaves ShadowSeq unchanged while the old prompt read is
// blocked; resuming that read must discard the complete observation before it
// mutates prompt state or schedules a notification.
func TestProbeCodexScreenPrompts_DiscardsStaleMuteSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := NewHandler(guard, notifier)
		notifier.SetIsMuted(h.IsMuted)

		base := time.Now()
		oldWindows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		})
		fake := &fakeKittyClient{
			windows: oldWindows,
			getText: map[string]string{
				kitty.MatchWindowByID(42): codexRequestUserInputScreen,
			},
		}
		h.SetKittyClient(fake)
		codexSeedActiveBolt(t, h, 42, "old", base.Unix())
		h.publishSnapshot(oldWindows, base)
		snapshot, ready := h.cachedScreenProbeSnapshot()
		if !ready {
			t.Fatal("cached probe snapshot is not ready")
		}

		entered := make(chan struct{})
		release := make(chan struct{})
		fake.beforeGetText = func(string, string) {
			close(entered)
			<-release
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.probeCodexScreenPromptsWithSnapshot(base.Add(time.Second), snapshot)
		}()
		<-entered

		receiptDuringIO := h.captureWindowReceipt(42)
		newWindows := codexProbeWindows(42, palette.BuildTitleFull("W:api", "bolt", false, true), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		})
		h.publishSnapshot(newWindows, base.Add(2*time.Second))
		// Keep the test explicit about the mute-only state change even though
		// publication now refreshes the cache in the same critical section.
		h.refreshMutedFromSnapshot(newWindows)
		receiptAfterPublish := h.captureWindowReceipt(42)
		mutedAfterPublish := h.IsMuted(42)
		want := screenProbeMutationFingerprint(t, h, 42)

		close(release)
		<-done
		synctest.Wait()

		if receiptDuringIO != snapshot.receipt(42) {
			t.Fatalf("receipt changed during prompt I/O = %d, want %d", receiptDuringIO, snapshot.receipt(42))
		}
		if receiptAfterPublish != snapshot.receipt(42) {
			t.Fatalf("mute-only publication advanced receipt = %d, want %d", receiptAfterPublish, snapshot.receipt(42))
		}
		if !mutedAfterPublish {
			t.Fatal("new snapshot did not mute window 42")
		}
		if got := screenProbeMutationFingerprint(t, h, 42); got != want {
			t.Fatalf("stale prompt mutated state:\n got  %s\n want %s", got, want)
		}
		if !h.IsMuted(42) {
			t.Fatal("stale prompt overwrote the newer muted cache value")
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("stale muted prompt dispatches = %d, want 0", got)
		}
	})
}

func TestProbeCodexScreenPrompts_PreservesLoopMarkerAndHarnessEmoji(t *testing.T) {
	h := newTestHandler()
	tabTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "🦊api",
		State:   "bolt",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, tabTitle, kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "🦊api",
		State:   "question",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

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
		pendingBefore, armed := h.pendingResetStops[42]
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
		_, stillPending := h.pendingResetStops[42]
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
		newerPending, armed := h.pendingResetStops[42]
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
		pendingAfter, stillPending := h.pendingResetStops[42]
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
	if !h.deferResetSensitiveStop(req, hookevent.HookPayload{}) {
		t.Fatal("reset-sensitive Stop was not deferred")
	}
	h.stateMu.Lock()
	pending, armed := h.pendingResetStops[42]
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
		h.commitDeferredResetStop(req, pending.seq)
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
	_, stillPending := h.pendingResetStops[42]
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

func TestProbeCodexScreenPrompts_DoesNotOverwriteProtectedIcons(t *testing.T) {
	protected := []string{"stop", "warning", "error", "subagent", "compact"}
	for _, state := range protected {
		t.Run(state, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{
				windows: codexProbeWindows(42, palette.BuildTitle("W:api", state, false), kitty.UserVars{
					Harness: "codex",
				}),
				getText: map[string]string{
					kitty.MatchWindowByID(42): codexEditApprovalScreen,
				},
			}
			h.SetKittyClient(fake)

			h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
			if len(resp.Events) != 0 {
				t.Fatalf("events = %d, want 0", len(resp.Events))
			}
		})
	}
}

func TestProbeCodexScreenPrompts_IgnoresNonCodexManagedWindows(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexEditApprovalScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

func TestProbeCodexScreenPrompts_IgnoresUnmanagedNonCodexForeground(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("W:api", "bolt", false),
			[]string{"/bin/zsh"},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

func TestProbeCodexScreenPrompts_MissedPromptKeepsQuestionState(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "question", false), kitty.UserVars{
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): "ordinary shell output\n",
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(42, "elicitation", true)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if got := h.guard.Current(42); got != "elicitation" {
		t.Fatalf("guard.Current = %q, want elicitation", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

// TestProbeCodexScreenPrompts_CompletedTurnStopsStuckBolt drives the completed-
// turn recovery through the authoritative-idle path. It seeds a real
// session-scoped UserPromptSubmit fact (not just a legacy guard) so the stuck ⚡
// is backed by a live interactive reducer fact — the reducer precedence the old
// legacy-handler stop could not defeat. The synthetic stop must retire that
// stale bolt, converge the tab to 🛑, and record a ScreenStopProbe row carrying
// the ACTUAL reduced state.
func TestProbeCodexScreenPrompts_CompletedTurnStopsStuckBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	boltTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟AiBroker",
		State:   "bolt",
		Looping: true,
	})
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, boltTitle, kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexCompletedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeCodexScreenPrompts(base)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(42) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(42))
	}
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟AiBroker",
		State:   "stop",
		Looping: true,
	})
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}

	if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("effective = %+v visible %q, want stop/stop", eff, visible)
	}

	// The stale interactive bolt fact was retired, so it can no longer mask 🛑.
	h.stateMu.Lock()
	old := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "codex:sess-1"}]
	h.stateMu.Unlock()
	if old == nil || !old.acked {
		t.Fatalf("stale interactive bolt was not retired: %+v", old)
	}

	// The ScreenStopProbe row is attributed to the codex_screen probe source and
	// reports the ACTUAL reduced result (stop), not the requested action.
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent)
	if !ok {
		t.Fatalf("no %q row in %+v", screenStopProbeEvent, resp.Events)
	}
	if ev.Action != "stop" {
		t.Fatalf("ScreenStopProbe action = %q, want stop", ev.Action)
	}
	if ev.Source != string(SourceProbe)+":"+screenPromptProbeSource {
		t.Fatalf("ScreenStopProbe source = %q, want %q", ev.Source, string(SourceProbe)+":"+screenPromptProbeSource)
	}
}

// TestProbeCodexScreenPrompts_InterruptedTurnStopsStuckBolt is the interruption
// twin of the completed-turn recovery: a user-interrupted Codex turn that left
// no Stop hook settles the same way through the authoritative-idle path.
func TestProbeCodexScreenPrompts_InterruptedTurnStopsStuckBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexInterruptedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeCodexScreenPrompts(base)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "stop", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop after interruption", eff)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent); !ok || ev.Action != "stop" {
		t.Fatalf("ScreenStopProbe row = %+v ok=%v, want stop", ev, ok)
	}
}

// TestProbeCodexScreenPrompts_ConcurrentPromptInvalidatesStopProbe proves the
// captured-sequence guard: a fresh prompt landing during the (slow) screen read
// advances the reducer receipt past the value the probe captured, so the stale
// completed-turn read is discarded and the newer interactive bolt is preserved.
func TestProbeCodexScreenPrompts_ConcurrentPromptInvalidatesStopProbe(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexCompletedTurnScreen,
		},
	}
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// The idle probe reads the screen on its own GetText call (the prompt probe
	// reads first). Seeding a fresh prompt on that second read models a concurrent
	// UserPromptSubmit landing after the probe captured the receipt sequence.
	var screenReads int
	fake.beforeGetText = func(match, extent string) {
		if match != kitty.MatchWindowByID(42) || extent != "screen" {
			return
		}
		screenReads++
		if screenReads == 2 {
			codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix()+1)
		}
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(base)

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the newer bolt preserved against a stale stop probe", eff)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent); ok {
		t.Fatalf("ScreenStopProbe row recorded despite a concurrent prompt: %+v", ev)
	}
}

// TestProbeCodexScreenPrompt_DiscardsObservationAfterConcurrentReducerEvent
// pins the window-wide receipt contract for the prompt-classification path: a
// reducer event landing during the (slow) off-lock screen read advances the
// receipt past the value the probe captured, so the complete observation is
// discarded. A discarded observation performs no reducer, guard, audit,
// renderer, or notification mutation — a concurrent prompt, terminal, error,
// or clear always outranks the stale screen prompt.
func TestProbeCodexScreenPrompt_DiscardsObservationAfterConcurrentReducerEvent(t *testing.T) {
	cases := []struct {
		name string
		// inject lands a concurrent reducer event while the probe's screen read
		// (which classifies as a request_user_input prompt) is in flight.
		inject func(t *testing.T, h *Handler, at int64)
		// wantIcon is the concurrent event's reduced icon, which must survive.
		wantIcon string
		// wantGuard is the concurrent event's guard entry, which the discarded
		// probe must not replace with its own prompt guard.
		wantGuard string
		// wantSessionAcked is whether the concurrent event itself retired the
		// seeded interactive fact; the stale prompt (applied with Prompt: true)
		// must never acknowledge a newer terminal fact on its own.
		wantSessionAcked bool
	}{
		{
			name: "prompt",
			inject: func(t *testing.T, h *Handler, at int64) {
				codexInjectSessionEvent(t, h, 42, "sess-1", "active", "UserPromptSubmit", at)
			},
			wantIcon:  "bolt",
			wantGuard: "active",
		},
		{
			name: "terminal",
			inject: func(t *testing.T, h *Handler, at int64) {
				codexInjectSessionEvent(t, h, 42, "sess-1", "stop", "Stop", at)
			},
			wantIcon:  "stop",
			wantGuard: "stop",
		},
		{
			name: "error",
			inject: func(t *testing.T, h *Handler, at int64) {
				codexInjectSessionEvent(t, h, 42, "sess-1", "stop_failure", "Stop", at)
			},
			wantIcon:  "error",
			wantGuard: "stop_failure",
		},
		{
			name: "clear",
			inject: func(t *testing.T, h *Handler, at int64) {
				resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: "codex"})
				if !resp.OK {
					t.Fatalf("inject user_clear rejected: %+v", resp)
				}
			},
			wantIcon:         "",
			wantGuard:        "",
			wantSessionAcked: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := NewStateGuard(time.Hour)
				// Permission notifications enabled so an applied (non-discarded)
				// prompt WOULD dispatch; stop_failure disabled so the injected
				// error event cannot dispatch on its own.
				no := false
				notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true, StopFailure: &no})
				h := NewHandler(guard, notifier)
				base := time.Now()
				fake := &fakeKittyClient{
					windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
						Profile: "work",
						Harness: "codex",
					}),
					getText: map[string]string{
						kitty.MatchWindowByID(42): codexRequestUserInputScreen,
					},
				}
				codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

				// Land the concurrent reducer event during the prompt probe's
				// screen read: after the probe captured the receipt, before it
				// applies the classified prompt.
				injected := false
				fake.beforeGetText = func(match, extent string) {
					if injected || match != kitty.MatchWindowByID(42) || extent != "screen" {
						return
					}
					injected = true
					tc.inject(t, h, base.Unix()+1)
				}
				h.SetKittyClient(fake)

				h.ProbeCodexScreenPrompts(base)
				synctest.Wait()

				// The concurrent event's reduced state stands.
				if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.wantIcon {
					t.Fatalf("effective icon = %q, want %q (concurrent %s must outrank the stale screen prompt)",
						eff.Icon, tc.wantIcon, tc.name)
				}
				// No reducer mutation: the discarded prompt left no probe fact and
				// did not acknowledge the session's own fact.
				h.stateMu.Lock()
				probeFact := h.store[42].Reducer.facts[SourceID{Kind: SourceProbe, Key: screenPromptProbeSource}]
				sessionFact := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "codex:sess-1"}]
				h.stateMu.Unlock()
				if probeFact != nil {
					t.Fatalf("discarded observation left a probe fact: %+v", probeFact)
				}
				if sessionFact == nil || sessionFact.acked != tc.wantSessionAcked {
					t.Fatalf("session fact = %+v, want acked=%v (only the concurrent event may retire it)",
						sessionFact, tc.wantSessionAcked)
				}
				// No guard mutation: the concurrent event's guard entry survives.
				if got := h.guard.Current(42); got != tc.wantGuard {
					t.Fatalf("guard.Current = %q, want %q", got, tc.wantGuard)
				}
				// No audit mutation: not even a suppressed ScreenPromptProbe row.
				resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
				for _, ev := range resp.Events {
					if ev.Event == screenPromptProbeEvent {
						t.Fatalf("discarded observation recorded an audit row: %+v", ev)
					}
				}
				// No renderer mutation from the stale prompt: the title never
				// shows the discarded question icon.
				stale := palette.BuildTitle("W:api", "question", false)
				for _, call := range fake.titleCalls() {
					if call.title == stale {
						t.Fatalf("discarded observation rendered the stale prompt icon %q", call.title)
					}
				}
				// No notification for the discarded prompt (or anything else).
				if got := rec.len(); got != 0 {
					t.Fatalf("dispatches = %d; want 0 after a discarded observation", got)
				}
			})
		})
	}
}

// codexInjectSessionEvent lands one session-scoped normalized event on the
// window mid-test, modelling a hook that arrives while a probe's screen read
// is in flight.
func codexInjectSessionEvent(t *testing.T, h *Handler, windowID int, session, action, event string, at int64) {
	t.Helper()
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "codex:" + session},
		Action:    action,
		SessionID: session,
		At:        at,
		Event:     event,
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: windowID, Action: action, Event: event, normalized: &ev})
	if !resp.OK {
		t.Fatalf("inject codex %s rejected: %+v", action, resp)
	}
}

func TestProbeCodexScreenPrompts_ActiveWorkingTurnKeepsBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexWorkingTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeCodexScreenPrompts(base)

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the working bolt preserved", eff)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent); ok {
		t.Fatalf("ScreenStopProbe row recorded for a still-working turn: %+v", ev)
	}
}

func TestProbeCodexScreenPrompts_CompletedTurnForcesCodexForForeground(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("W:api", "bolt", false),
			[]string{"/opt/homebrew/bin/codex", "--yolo"},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexCompletedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	// The tab is detected as Codex only by its foreground process, but the stuck
	// ⚡ is still backed by a real interactive fact that the authoritative-idle
	// path settles.
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeCodexScreenPrompts(base)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "stop", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop", eff)
	}
}

func TestProbeCodexScreenPrompts_ManagedOwnerSuppressesUnmanagedCodexSiblingStopProbe(t *testing.T) {
	h := newTestHandler()
	boltTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟AiBroker",
		State:   "bolt",
		Looping: true,
	})
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			boltTitle,
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
		getText: map[string]string{
			kitty.MatchWindowByID(43): codexCompletedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(43, "active", false)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(resp.Events) != 0 {
		t.Fatalf("sibling events = %d, want 0", len(resp.Events))
	}
	if got := h.guard.Current(43); got != "active" {
		t.Fatalf("sibling guard.Current = %q, want active", got)
	}
}

func TestProbeCodexScreenPrompts_ManagedNonCodexOwnerSuppressesUnmanagedCodexSibling(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("W:api", "bolt", false),
			kitty.Window{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
			},
			kitty.Window{
				ID:       43,
				IsActive: true,
				ForegroundProcesses: []kitty.ForegroundProcess{
					{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
				},
			},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(43): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(43, "active", false)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(resp.Events) != 0 {
		t.Fatalf("sibling events = %d, want 0", len(resp.Events))
	}
}

func TestProbeCodexScreenPrompts_UnmanagedTabProbesOnlyActiveCodexForeground(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("W:api", "bolt", false),
			kitty.Window{
				ID: 42,
				ForegroundProcesses: []kitty.ForegroundProcess{
					{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
				},
			},
			kitty.Window{
				ID:       43,
				IsActive: true,
				ForegroundProcesses: []kitty.ForegroundProcess{
					{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
				},
			},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
			kitty.MatchWindowByID(43): codexCommandApprovalScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(43) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(43))
	}
	if want := palette.BuildTitle("W:api", "key", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}); len(resp.Events) != 0 {
		t.Fatalf("inactive foreground events = %d, want 0", len(resp.Events))
	}
	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43}); len(resp.Events) != 1 {
		t.Fatalf("active foreground events = %d, want 1", len(resp.Events))
	}
}

// codexSeedActiveBolt folds a real session-scoped UserPromptSubmit fact into a
// window so its stuck ⚡ is backed by a live interactive reducer fact — the
// reducer precedence a synthetic screen stop must defeat. It clears the dirty
// mark left by seeding so the probe's own render is the only title write the
// fake observes, and returns the resulting receipt sequence.
func codexSeedActiveBolt(t *testing.T, h *Handler, windowID int, session string, at int64) uint64 {
	t.Helper()
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "codex:" + session},
		Action:    "active",
		SessionID: session,
		At:        at,
		Event:     "UserPromptSubmit",
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "active", Event: "UserPromptSubmit", normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed codex active %q rejected: %+v", session, resp)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[windowID]
	st.Dirty = false
	st.DirtySince = time.Time{}
	return st.ShadowSeq
}

func directCodexProbeBinding(windowID int, receipt, snapshotGen uint64) screenProbeBinding {
	binding := screenProbeBinding{
		SourceWindowID:      windowID,
		TargetWindowID:      windowID,
		SourceReceipt:       receipt,
		TargetReceipt:       receipt,
		SnapshotGen:         snapshotGen,
		legacyDirectManaged: true,
	}
	binding.Producer.Harness = harness.Codex
	return binding
}

// lastEventNamed returns the most recent event with the given name.
func lastEventNamed(events []SessionEvent, name string) (SessionEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event == name {
			return events[i], true
		}
	}
	return SessionEvent{}, false
}

func codexProbeForegroundWindows(windowID int, tabTitle string, foregroundCmdline []string) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: tabTitle,
					Windows: []kitty.Window{
						{
							ID: windowID,
							ForegroundProcesses: []kitty.ForegroundProcess{
								{Cmdline: foregroundCmdline},
							},
						},
					},
				},
			},
		},
	}
}

func codexProbeSplitWindows(tabTitle string, windows ...kitty.Window) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:      100,
					Title:   tabTitle,
					Windows: windows,
				},
			},
		},
	}
}

func codexProbeWindows(windowID int, tabTitle string, userVars kitty.UserVars) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: tabTitle,
					Windows: []kitty.Window{
						{ID: windowID, UserVars: userVars},
					},
				},
			},
		},
	}
}

const codexEditApprovalScreen = `
▌ Would you like to make the following edits?

  src/app.go
  + return nil

› 1. Yes, apply these edits
  2. No, keep editing

Press enter to confirm
`

const codexRequestUserInputScreen = `
Pick the deployment target.

› Staging (Recommended)
  Production
  None of the above

↑/↓ to navigate questions
ctrl + p / ctrl + n change question
Type your answer (optional)
Add notes
Proceed
`

const codexCommandApprovalScreen = `
▌ Would you like to run the following command?

  go test ./...

› 1. Yes, run it
  2. No

Press enter to confirm
`
