package daemon

import (
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestHandler_HookEventEnrichesFromKittySnapshot(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: hookEventWindows(palette.BuildTitle("api", "key", false), []kitty.Window{{
			ID: 42,
			UserVars: kitty.UserVars{
				Profile: "work",
				Harness: "claude",
			},
		}}),
	}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{
		Type:          TypeHookEvent,
		WindowID:      42,
		Event:         "PreToolUse",
		PayloadJSON:   hookPayloadJSON(t, hookevent.HookPayload{ToolName: "Bash", Source: "hook"}),
		PipeMode:      true,
		KittyListenOn: "unix:/tmp/kitty",
	})
	if !resp.OK || resp.Allowed || resp.IconState != "" {
		t.Fatalf("TypeHookEvent response = %+v; want ok-only response", resp)
	}

	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %d, want 1 (%+v)", len(detail.Events), detail.Events)
	}
	ev := detail.Events[0]
	if ev.Event != "PreToolUse" || ev.Action != "active" || ev.ToolName != "Bash" || ev.Source != "hook" {
		t.Fatalf("event identity = %+v; want PreToolUse active Bash hook", ev)
	}
	if ev.ResultIconState != "bolt" || ev.CurrentIconState != "key" {
		t.Fatalf("event icons = current %q result %q; want key -> bolt", ev.CurrentIconState, ev.ResultIconState)
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	st := h.store[42]
	h.stateMu.Unlock()
	if meta.Durable.SessionTitle != "api" || meta.Durable.Profile != "work" || meta.Durable.Harness != "claude" {
		t.Fatalf("metadata = %+v; want title/api profile/work harness/claude", meta.Durable)
	}
	if st == nil || st.LastPipeMode != true || st.LastAccount != "work" {
		t.Fatalf("state = %+v; want pipe mode and account from snapshot/request", st)
	}
}

func TestHandler_HookEventKeepsStampedProducerSeparateFromTargetMetadata(t *testing.T) {
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{
		windows: hookEventWindows("api", []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}}),
	})

	resp := h.Handle(Request{
		Type:              TypeHookEvent,
		WindowID:          42,
		Event:             "UserPromptSubmit",
		Harness:           " Codex ",
		ProducerConfigDir: "/Users/dev/.codex-work",
		PayloadJSON:       `{"session_id":"codex-session"}`,
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}

	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %+v, want one producer-stamped event", detail.Events)
	}
	event := detail.Events[0]
	if event.SourceKind != string(SourceHarness) || event.SourceKey != "codex:codex-session" {
		t.Fatalf("normalized source = %q/%q, want harness/codex:codex-session", event.SourceKind, event.SourceKey)
	}
	if event.ProducerConfigDir != "/Users/dev/.codex-work" {
		t.Fatalf("producer config dir = %q, want stamped producer home preserved beside target metadata", event.ProducerConfigDir)
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if meta.Durable.Harness != harness.Claude {
		t.Fatalf("durable target harness = %q, want claude", meta.Durable.Harness)
	}
}

func TestHandler_HookEventExplicitUnknownProducerIsNotInferredFromTarget(t *testing.T) {
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{
		windows: hookEventWindows("api", []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}}),
	})

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    42,
		Event:       "Notification",
		Harness:     "future-harness",
		PayloadJSON: `{"session_id":"unknown-session","notification_type":"idle_prompt"}`,
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}

	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %+v, want one unknown-producer event", detail.Events)
	}
	event := detail.Events[0]
	if event.Action != "idle" {
		t.Fatalf("unknown-producer action = %q, want harness-agnostic idle", event.Action)
	}
	if event.SourceKind != string(SourceLegacy) || event.SourceKey != "win:42" {
		t.Fatalf("unknown-producer source = %q/%q, want legacy/win:42", event.SourceKind, event.SourceKey)
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	h.stateMu.Unlock()
	if meta.Durable.Harness != harness.Claude {
		t.Fatalf("durable target harness = %q, want claude", meta.Durable.Harness)
	}
}

func TestHandler_HookEventUnknownProducerDoesNotAcquireCopilotPermissionBehavior(t *testing.T) {
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{
		windows: hookEventWindows("api", []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}}),
	})

	resp := h.Handle(Request{
		Type:     TypeHookEvent,
		WindowID: 42,
		Event:    "permissionRequest",
		Harness:  "future-harness",
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].ResultIconState != "key" {
		t.Fatalf("unknown-producer permission events = %+v, want generic key transition", detail.Events)
	}
}

func TestHandler_HookEventCodexAutoCompactRestoresBolt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{
			windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: harness.Codex},
			}}),
		}
		h.SetKittyClient(fake)

		for _, event := range []struct {
			name    string
			payload hookevent.HookPayload
		}{
			{name: "PreCompact", payload: hookevent.HookPayload{Trigger: "auto"}},
			{name: "PostCompact", payload: hookevent.HookPayload{Trigger: "auto"}},
			{name: "PostToolUse"},
		} {
			resp := h.Handle(Request{
				Type:        TypeHookEvent,
				WindowID:    42,
				Event:       event.name,
				PayloadJSON: hookPayloadJSON(t, event.payload),
			})
			if !resp.OK {
				t.Fatalf("%s response = %+v", event.name, resp)
			}
		}

		detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
		if len(detail.Events) != 3 {
			t.Fatalf("events = %d, want 3 (%+v)", len(detail.Events), detail.Events)
		}
		wantActions := []string{"compact", "active", "active"}
		wantIcons := []string{"compact", "bolt", "bolt"}
		for i, event := range detail.Events {
			if event.Suppressed || event.Action != wantActions[i] || event.ResultIconState != wantIcons[i] {
				t.Fatalf("event[%d] = %+v; want allowed action=%q icon=%q", i, event, wantActions[i], wantIcons[i])
			}
		}

		h.stateMu.Lock()
		state := h.store[42]
		h.stateMu.Unlock()
		if state == nil || state.LastAction != "active" || state.DesiredIcon != "bolt" {
			t.Fatalf("state = %+v; want active bolt after automatic compaction", state)
		}

		synctest.Wait()
		if got := rec.len(); got != 0 {
			t.Fatalf("automatic compaction fired %d stop notifications, want 0: %+v", got, rec.snapshot())
		}
	})
}

func TestHandler_HookEventCodexManualCompactRemainsStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{
			windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: harness.Codex},
			}}),
		}
		h.SetKittyClient(fake)

		for _, event := range []struct {
			name    string
			payload hookevent.HookPayload
		}{
			{name: "PreCompact", payload: hookevent.HookPayload{Trigger: "manual"}},
			{name: "PostCompact", payload: hookevent.HookPayload{Trigger: "manual"}},
			{name: "PostToolUse"},
		} {
			resp := h.Handle(Request{
				Type:        TypeHookEvent,
				WindowID:    42,
				Event:       event.name,
				PayloadJSON: hookPayloadJSON(t, event.payload),
			})
			if !resp.OK {
				t.Fatalf("%s response = %+v", event.name, resp)
			}
		}

		detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
		if len(detail.Events) != 3 {
			t.Fatalf("events = %d, want 3 (%+v)", len(detail.Events), detail.Events)
		}
		if event := detail.Events[1]; event.Suppressed || event.Action != "stop" || event.ResultIconState != "stop" {
			t.Fatalf("PostCompact event = %+v; want allowed stop icon", event)
		}
		if event := detail.Events[2]; !event.Suppressed || event.Action != "active" || event.SuppressionReason != suppressionReasonActiveAfterTerminal {
			t.Fatalf("PostToolUse event = %+v; want active-after-terminal suppression", event)
		}

		h.stateMu.Lock()
		state := h.store[42]
		h.stateMu.Unlock()
		if state == nil || state.LastAction != "stop" || state.DesiredIcon != "stop" {
			t.Fatalf("state = %+v; want manual compaction to remain stopped", state)
		}

		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("manual compaction fired %d stop notifications, want 1: %+v", got, rec.snapshot())
		}
	})
}

func TestHandler_HookEventOwnerMismatchNoOps(t *testing.T) {
	stubDaemonLoopDiscover(t, nil, nil)
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows("api", []kitty.Window{
		{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude}},
		{ID: 43, IsActive: false},
	})}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    43,
		Event:       "Notification",
		Harness:     harness.Claude,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "inactive", NotificationType: "agent_idle"}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}

	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 1 || !detail.Events[0].Suppressed ||
		detail.Events[0].SuppressionReason != suppressionReasonOwnerMismatch {
		t.Fatalf("events = %+v; want owner_mismatch suppression", detail.Events)
	}
	h.stateMu.Lock()
	st := h.store[43]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "" {
		t.Fatalf("state = %+v; owner mismatch should not set a desired icon", st)
	}
}

func TestHandler_HookEventTabTitleOwnerStillUpdates(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: hookEventWindows("api", []kitty.Window{
			{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Codex}},
			{ID: 43},
		}),
	}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "PostToolUse"})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Suppressed || detail.Events[0].Action != "active" {
		t.Fatalf("events = %+v; want one allowed active event", detail.Events)
	}
}

func TestHandler_HookEventScreenContextLimitPromotesStopFailure(t *testing.T) {
	h := newTestHandler()
	// Run the async context-limit probe inline so the promotion is deterministic
	// while still exercising the asynchronous request path.
	h.probeRunner = func(fn func()) { fn() }
	tabTitle := palette.BuildTitle("api", "bolt", false)
	fake := &fakeKittyClient{
		windows: hookEventWindows(tabTitle, []kitty.Window{{
			ID: 42,
			UserVars: kitty.UserVars{
				Profile: "work",
				Harness: "claude",
			},
		}}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): "Context limit reached - /compact or /clear to continue\n",
		},
	}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    42,
		Event:       "Notification",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{NotificationType: "agent_idle"}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	// The asynchronous probe appends a stop_failure upgrade once it sees the
	// banner on the source screen. It is always the final applied event.
	if len(detail.Events) == 0 {
		t.Fatalf("events = 0; want at least the async stop_failure upgrade")
	}
	ev := detail.Events[len(detail.Events)-1]
	if ev.Action != "stop_failure" || ev.ResultIconState != "error" || !ev.ContextLimit {
		t.Fatalf("upgrade event = %+v; want stop_failure error context-limit", ev)
	}
	if ev.CurrentIconState != "bolt" {
		t.Fatalf("upgrade event context = %+v; want current bolt", ev)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.DesiredIcon != "error" {
		t.Fatalf("state = %+v; want error icon after async context-limit promotion", st)
	}
}

func TestHandler_HookEventStopReasonContextLimitSkipsScreenProbe(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: "copilot"},
		}}),
		getTextErr: errHookEventGetTextShouldNotRun{},
	}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    42,
		Event:       "agentStop",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{StopReasonCamel: " context_limit_reached "}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %d, want 1 (%+v)", len(detail.Events), detail.Events)
	}
	ev := detail.Events[0]
	if ev.Action != "stop_failure" || !ev.ContextLimit || ev.StopReason != " context_limit_reached " {
		t.Fatalf("event = %+v; want stop_reason context-limit stop_failure", ev)
	}
}

func TestHandler_HookEventIgnoredAndUnknownAreLoggedSuppressions(t *testing.T) {
	cases := []struct {
		name   string
		event  string
		reason string
	}{
		{name: "instructions observer", event: "InstructionsLoaded", reason: suppressionReasonIgnoredEvent},
		{name: "message observer", event: "MessageDisplay", reason: suppressionReasonIgnoredEvent},
		{name: "cwd observer", event: "CwdChanged", reason: suppressionReasonIgnoredEvent},
		{name: "worktree observer", event: "WorktreeRemove", reason: suppressionReasonIgnoredEvent},
		{name: "unknown", event: "BogusEvent", reason: suppressionReasonUnknownEvent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{windows: hookEventWindows("api", []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
			}})}
			h.SetKittyClient(fake)

			resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: tc.event})
			if !resp.OK {
				t.Fatalf("hook event response = %+v", resp)
			}
			detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
			if len(detail.Events) != 1 {
				t.Fatalf("events = %d, want 1 (%+v)", len(detail.Events), detail.Events)
			}
			ev := detail.Events[0]
			if !ev.Suppressed || ev.SuppressionReason != tc.reason || ev.Event != tc.event {
				t.Fatalf("event = %+v; want suppression reason %q", ev, tc.reason)
			}
			if tc.reason == suppressionReasonIgnoredEvent {
				h.stateMu.Lock()
				state := h.store[42]
				h.stateMu.Unlock()
				if state == nil || state.Reducer != nil || state.StateSet || state.LastAction != "" || state.DesiredIcon != "" {
					t.Fatalf("observer event mutated reducer/renderer state: %+v", state)
				}
			}
		})
	}
}

func TestHandler_HookEventAgentSessionLoopStartedTargetsWorkspace(t *testing.T) {
	projectDir := t.TempDir()
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop"}),
		[]kitty.Window{
			{
				ID: 42,
				UserVars: kitty.UserVars{
					Profile:      "api",
					Harness:      "claude",
					AMXDirectory: projectDir,
				},
			},
			{ID: 43},
		},
	)}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{
		Type:     TypeHookEvent,
		WindowID: 43,
		Event:    "AgentSessionLoopStarted",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source:        "agent_session_loop",
			CWD:           projectDir,
			Harness:       "claude",
			KittyWindowID: 43,
		}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Action != "loop_marker" {
		t.Fatalf("events = %+v; want loop_marker on project pane", detail.Events)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || !st.Looping || !st.LoopingSet || st.DesiredIcon != "" {
		t.Fatalf("state = %+v; want loop marker true without changing stop icon", st)
	}
}

// TestHandler_HookEventLoopLifecycleTargetsActiveTitleOwner verifies that loop
// lifecycle fan-out lands on the window that actually owns the tab title (the
// active managed pane) rather than the first managed window in Kitty order. Two
// managed panes share a workspace, but only the active title owner should carry
// the loop marker.
func TestHandler_HookEventLoopLifecycleTargetsActiveTitleOwner(t *testing.T) {
	projectDir := t.TempDir()
	h := newTestHandler()
	// Window 42 comes first in Kitty order but is inactive; window 44 is the
	// active managed pane, so it owns the tab title. With no window id in the
	// payload, matching falls to CWD and both managed panes qualify — the fan-out
	// must still resolve to the active owner (44), not the first managed (42).
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop"}),
		[]kitty.Window{
			{
				ID:       42,
				IsActive: false,
				UserVars: kitty.UserVars{Profile: "api", Harness: "claude", AMXDirectory: projectDir},
			},
			{
				ID:       44,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "api", Harness: "claude", AMXDirectory: projectDir},
			},
		},
	)}
	h.SetKittyClient(fake)

	resp := h.Handle(Request{
		Type:     TypeHookEvent,
		WindowID: 44,
		Event:    "AgentSessionLoopStarted",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source:  "agent_session_loop",
			CWD:     projectDir,
			Harness: "claude",
		}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}

	h.stateMu.Lock()
	owner := h.store[44]
	first := h.store[42]
	h.stateMu.Unlock()
	if owner == nil || !owner.Looping || !owner.LoopingSet {
		t.Fatalf("active title owner state = %+v; want loop marker on window 44", owner)
	}
	if first != nil && (first.Looping || first.LoopingSet) {
		t.Fatalf("first managed window state = %+v; loop marker must not land on window 42", first)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 44})
	if len(detail.Events) != 1 || detail.Events[0].Action != "loop_marker" {
		t.Fatalf("owner events = %+v; want loop_marker on the active title owner", detail.Events)
	}
}

func TestHandler_HookEventAgentSessionLoopFinishedClearsLoopMarker(t *testing.T) {
	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, nil, nil)

	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop", Looping: true}),
		[]kitty.Window{
			{
				ID: 42,
				UserVars: kitty.UserVars{
					Profile:      "api",
					Harness:      "claude",
					AMXDirectory: projectDir,
				},
			},
			{ID: 43},
		},
	)}
	h.SetKittyClient(fake)
	h.stateMu.Lock()
	h.ensureWindowStateLocked(42).Looping = true
	h.ensureWindowStateLocked(42).LoopingSet = true
	h.stateMu.Unlock()

	resp := h.Handle(Request{
		Type:     TypeHookEvent,
		WindowID: 43,
		Event:    "AgentSessionLoopFinished",
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source:        "agent_session_loop",
			Reason:        "run_finished",
			CWD:           projectDir,
			Harness:       "claude",
			KittyWindowID: 43,
		}),
	})
	if !resp.OK {
		t.Fatalf("hook event response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Action != "idle" || detail.Events[0].Event != "AgentSessionLoopFinished" {
		t.Fatalf("events = %+v; want idle AgentSessionLoopFinished on project pane", detail.Events)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.Looping || !st.LoopingSet {
		t.Fatalf("state = %+v; want loop marker cleared", st)
	}
}

func TestHandler_HookEventDefersResetSensitiveStop(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 40 * time.Millisecond
	t.Cleanup(func() {
		resetFlowStopDelay = oldDelay
	})

	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		fake := &fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
		}})}
		h.SetKittyClient(fake)

		resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"})
		if !resp.OK {
			t.Fatalf("Stop response = %+v", resp)
		}
		if detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}); len(detail.Events) != 0 {
			t.Fatalf("events before deferred timer = %+v; want none", detail.Events)
		}

		time.Sleep(resetFlowStopDelay / 4)
		resp = h.Handle(Request{
			Type:        TypeHookEvent,
			WindowID:    42,
			Event:       "SessionEnd",
			PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{Reason: "clear"}),
		})
		if !resp.OK {
			t.Fatalf("SessionEnd response = %+v", resp)
		}

		time.Sleep(resetFlowStopDelay)
		synctest.Wait()

		detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
		if len(detail.Events) != 2 {
			t.Fatalf("events = %d, want 2 (%+v)", len(detail.Events), detail.Events)
		}
		for _, ev := range detail.Events {
			if ev.Action != "user_clear" || !ev.ResetFlow || ev.ResultIconState != "" {
				t.Fatalf("event = %+v; reset-sensitive stop should be rewritten to user_clear", ev)
			}
		}
	})
}

// TestHandler_HookEventDeferredResetStopSupersededByPromptAndClear covers the
// ordering hazard where a Stop is still held in the reset-defer window when the
// next turn's prompt and an F8 clear both arrive. Both are newer authoritative
// events, so the held stop must be cancelled and must never fire afterward to
// repaint a stale stop icon over the wiped tab.
func TestHandler_HookEventDeferredResetStopSupersededByPromptAndClear(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 60 * time.Millisecond // a real, nonzero debounce interval
	t.Cleanup(func() {
		resetFlowStopDelay = oldDelay
	})

	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()
		fake := &fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
			ID:       42,
			IsActive: true,
			UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude},
		}})}
		h.SetKittyClient(fake)

		// The Stop enters the reset-defer window rather than painting immediately.
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
			t.Fatalf("Stop response = %+v", resp)
		}
		// Advance less than the debounce so the stop is still pending.
		time.Sleep(resetFlowStopDelay / 3)

		// A fresh prompt begins the next turn and must cancel the pending stop.
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit"}); !resp.OK {
			t.Fatalf("UserPromptSubmit response = %+v", resp)
		}
		// F8 then wipes the tab, still before the original stop timer fires.
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "user_clear"}); !resp.OK {
			t.Fatalf("user_clear response = %+v", resp)
		}

		// Let the original debounce fully elapse; the superseded stop must not fire.
		time.Sleep(resetFlowStopDelay)
		synctest.Wait()

		h.stateMu.Lock()
		st := h.store[42]
		h.stateMu.Unlock()
		if st == nil || st.DesiredIcon != "" {
			t.Fatalf("state = %+v; want cleared icon after prompt+F8 supersede the deferred stop", st)
		}
		detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
		for _, ev := range detail.Events {
			if ev.ResultIconState == "stop" {
				t.Fatalf("events = %+v; the deferred stop must not fire after being superseded", detail.Events)
			}
		}
	})
}

func TestHandler_CopilotLateInitialSessionStartPreservesPromptAndAllowsStop(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 0
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "", false), []kitty.Window{{
		ID:       42,
		IsActive: true,
		UserVars: kitty.UserVars{Profile: "copilot", Harness: "copilot"},
	}})}
	h.SetKittyClient(fake)

	promptPayload := hookPayloadJSON(t, hookevent.HookPayload{SessionIDCamel: "session-1"})
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "userPromptSubmitted", PayloadJSON: promptPayload}); !resp.OK {
		t.Fatalf("userPromptSubmitted response = %+v", resp)
	}

	startPayload := hookPayloadJSON(t, hookevent.HookPayload{SessionIDCamel: "session-1", Source: "new"})
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "sessionStart", PayloadJSON: startPayload}); !resp.OK {
		t.Fatalf("sessionStart response = %+v", resp)
	}
	h.stateMu.Lock()
	if got := h.store[42].DesiredIcon; got != "bolt" {
		h.stateMu.Unlock()
		t.Fatalf("late initial sessionStart desired icon = %q, want bolt", got)
	}
	h.stateMu.Unlock()

	stopPayload := hookPayloadJSON(t, hookevent.HookPayload{SessionIDCamel: "session-1", StopReasonCamel: "end_turn"})
	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "agentStop", PayloadJSON: stopPayload}); !resp.OK {
		t.Fatalf("agentStop response = %+v", resp)
	}
	h.stateMu.Lock()
	if got := h.store[42].DesiredIcon; got != "stop" {
		h.stateMu.Unlock()
		t.Fatalf("agentStop desired icon = %q, want stop", got)
	}
	h.stateMu.Unlock()

	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 3 {
		t.Fatalf("events = %+v, want prompt/start/stop", detail.Events)
	}
	if detail.Events[1].ResetFlow || detail.Events[1].ResultIconState != IconStatePreserve {
		t.Fatalf("late sessionStart event = %+v, want non-reset preserve", detail.Events[1])
	}
}

func TestHandler_HookEventAutoApprovedPermissionLogsSuppressionReason(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows(palette.BuildTitle("api", "bolt", false), []kitty.Window{{
		ID:       42,
		UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
	}})}
	h.SetKittyClient(fake)

	h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "PreToolUse"})
	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "PermissionRequest"})
	if !resp.OK {
		t.Fatalf("PermissionRequest response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 2 {
		t.Fatalf("events = %+v; want two events", detail.Events)
	}
	ev := detail.Events[1]
	if !ev.Suppressed || ev.SuppressionReason != suppressionReasonAutoApprovedPerm || ev.ResultIconState != IconStatePreserve {
		t.Fatalf("event = %+v; want auto-approved permission suppression", ev)
	}
}

func hookPayloadJSON(t *testing.T, payload hookevent.HookPayload) string {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal hook payload: %v", err)
	}
	return string(data)
}

func hookEventWindows(tabTitle string, windows []kitty.Window) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:      100,
			Title:   tabTitle,
			Windows: windows,
		}},
	}}
}

func stubDaemonLoopDiscover(t *testing.T, candidates []agentloop.Candidate, err error) {
	t.Helper()
	old := daemonLoopDiscover
	daemonLoopDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		out := make([]agentloop.Candidate, len(candidates))
		copy(out, candidates)
		return out, err
	}
	t.Cleanup(func() {
		daemonLoopDiscover = old
	})
}

type errHookEventGetTextShouldNotRun struct{}

func (errHookEventGetTextShouldNotRun) Error() string { return "GetText should not run" }
