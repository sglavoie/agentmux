package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
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

func TestProbeCodexScreenPrompts_CompletedTurnStopsStuckBolt(t *testing.T) {
	h := newTestHandler()
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
	h.guard.TryUpdate(42, "active", false)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

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

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.Event != screenStopProbeEvent || ev.Source != screenPromptProbeSource {
		t.Fatalf("event identity = (%q, %q), want (%q, %q)", ev.Event, ev.Source, screenStopProbeEvent, screenPromptProbeSource)
	}
	if ev.Action != "stop" || ev.ResultIconState != "stop" || ev.CurrentIconState != "bolt" {
		t.Fatalf("event = %+v, want stop bolt->stop", ev)
	}

	h.stateMu.Lock()
	lastAction := h.store[42].LastAction
	meta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if lastAction != "stop" {
		t.Fatalf("lastAction = %q, want stop", lastAction)
	}
	if meta.Durable.LastAction != "stop" || meta.Durable.Harness != "codex" {
		t.Fatalf("durable metadata = %+v, want codex/stop", meta.Durable)
	}
}

func TestProbeCodexScreenPrompts_ActiveWorkingTurnKeepsBolt(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexWorkingTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(42, "active", false)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

func TestProbeCodexScreenPrompts_CompletedTurnForcesCodexForForeground(t *testing.T) {
	h := newTestHandler()
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

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "stop", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if meta.Durable.Harness != "codex" || meta.Durable.LastAction != "stop" {
		t.Fatalf("durable metadata = %+v, want codex/stop", meta.Durable)
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
