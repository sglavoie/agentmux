package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func unmanagedRouteWindows(title, ownerHarness string, sourceActive bool) []kitty.OSWindow {
	return hookEventWindows(title, []kitty.Window{
		{
			ID:       42,
			IsActive: !sourceActive,
			UserVars: kitty.UserVars{Profile: "managed", Harness: ownerHarness},
		},
		{ID: 43, IsActive: sourceActive},
	})
}

func TestHandler_UnmanagedRouteCrossHarnessMatrix(t *testing.T) {
	for _, ownerHarness := range []string{harness.Claude, harness.Codex, harness.Copilot} {
		for _, producerHarness := range []string{harness.Claude, harness.Codex, harness.Copilot} {
			name := ownerHarness + "_owner_" + producerHarness + "_producer"
			t.Run(name, func(t *testing.T) {
				h := newTestHandler()
				h.SetKittyClient(&fakeKittyClient{windows: unmanagedRouteWindows("api", ownerHarness, true)})
				sessionID := fmt.Sprintf("%s-on-%s", producerHarness, ownerHarness)

				resp := h.Handle(Request{
					Type:        TypeHookEvent,
					WindowID:    43,
					Event:       "UserPromptSubmit",
					Harness:     producerHarness,
					PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: sessionID}),
				})
				if !resp.OK {
					t.Fatalf("hook response = %+v", resp)
				}

				detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
				if len(detail.Events) != 1 {
					t.Fatalf("target events = %+v; want one", detail.Events)
				}
				event := detail.Events[0]
				if event.Suppressed || event.RouteDecision != routeDecisionOwner ||
					event.SourceKind != string(SourceHarness) || event.SourceKey != producerHarness+":"+sessionID {
					t.Fatalf("routed event = %+v", event)
				}

				h.stateMu.Lock()
				st := h.store[42]
				meta := h.windowMetadata[42]
				var fact *sourceFact
				if st != nil && st.Reducer != nil {
					fact = st.Reducer.facts[SourceID{Kind: SourceHarness, Key: producerHarness + ":" + sessionID}]
				}
				h.stateMu.Unlock()
				if st == nil || st.DesiredIcon != "bolt" {
					t.Fatalf("target state = %+v; want bolt", st)
				}
				if meta.Durable.Harness != ownerHarness {
					t.Fatalf("durable harness = %q; want managed owner %q", meta.Durable.Harness, ownerHarness)
				}
				if fact == nil || fact.originWindowID != 43 || fact.sessionID != sessionID {
					t.Fatalf("routed fact = %+v; want session %q from origin 43", fact, sessionID)
				}
			})
		}
	}
}

func TestHandler_ActiveUnmanagedNonPromptEstablishesRoute(t *testing.T) {
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: unmanagedRouteWindows("api", harness.Codex, true)})

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    43,
		Event:       "PreToolUse",
		Harness:     harness.Claude,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "active-claude"}),
	})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 || detail.Events[0].Suppressed ||
		detail.Events[0].SourceKey != "claude:active-claude" || detail.Events[0].RouteDecision != routeDecisionOwner {
		t.Fatalf("target events = %+v; want active unmanaged Claude route", detail.Events)
	}
}

func TestHandler_UnmanagedPromptTakeoverAndStickyTerminal(t *testing.T) {
	stubDaemonLoopDiscover(t, nil, nil)
	h := newTestHandler()
	h.SetStopConfirm(0, 0, false) // pin the immediate-stop path, not the confirmed-idle hold
	initial := hookEventWindows("api", []kitty.Window{
		{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "codex-target", Harness: harness.Codex}},
		{ID: 44, IsActive: false, UserVars: kitty.UserVars{Profile: "other-target", Harness: harness.Copilot}},
		{ID: 43, IsActive: false},
	})
	h.SetKittyClient(&fakeKittyClient{windows: initial})
	payload := hookevent.HookPayload{SessionID: "claude-session"}

	// The cached snapshot still says the managed pane is active, but the prompt
	// itself proves the user focused the unmanaged source.
	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 43, Event: "UserPromptSubmit", Harness: harness.Claude,
		PayloadJSON: hookPayloadJSON(t, payload),
	}); !resp.OK {
		t.Fatalf("prompt response = %+v", resp)
	}
	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse", Harness: harness.Claude,
		PayloadJSON: hookPayloadJSON(t, payload),
	}); !resp.OK {
		t.Fatalf("tool response = %+v", resp)
	}

	// Focus moves to a different managed pane. The remembered live tuple still
	// admits Claude's terminal onto the original Codex target, not the new owner.
	h.publishSnapshot(hookEventWindows("api", []kitty.Window{
		{ID: 42, IsActive: false, UserVars: kitty.UserVars{Profile: "codex-target", Harness: harness.Codex}},
		{ID: 44, IsActive: true, UserVars: kitty.UserVars{Profile: "other-target", Harness: harness.Copilot}},
		{ID: 43, IsActive: false},
	}), time.Now())
	payload.NotificationType = "idle_prompt"
	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 43, Event: "Notification", Harness: harness.Claude,
		PayloadJSON: hookPayloadJSON(t, payload),
	}); !resp.OK {
		t.Fatalf("terminal response = %+v", resp)
	}

	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 3 || detail.Events[1].Suppressed || detail.Events[1].Action != "active" ||
		detail.Events[2].Suppressed || detail.Events[2].Action != "stop" ||
		detail.Events[2].RouteDecision != routeDecisionOwner {
		t.Fatalf("target events = %+v; want routed Claude terminal", detail.Events)
	}
	if other := h.Handle(Request{Type: TypeSessionDetail, WindowID: 44}); len(other.Events) != 0 {
		t.Fatalf("new title owner events = %+v; terminal must stay on original target", other.Events)
	}
	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	fact := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:claude-session"}]
	h.stateMu.Unlock()
	if meta.Durable.Harness != harness.Codex {
		t.Fatalf("durable harness = %q; want codex", meta.Durable.Harness)
	}
	if fact == nil || !fact.terminated || fact.originWindowID != 43 {
		t.Fatalf("terminal fact = %+v; want terminated origin 43", fact)
	}

	// Terminal application closes the ordinary route. Inactive trailing work is
	// diagnosed on the source and cannot inherit the old target binding.
	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 43, Event: "PostToolUse", Harness: harness.Claude,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "claude-session"}),
	}); !resp.OK {
		t.Fatalf("trailing response = %+v", resp)
	}
	sourceDetail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(sourceDetail.Events) != 1 || !sourceDetail.Events[0].Suppressed ||
		sourceDetail.Events[0].SuppressionReason != suppressionReasonOwnerMismatch {
		t.Fatalf("source events = %+v; want closed-route owner_mismatch", sourceDetail.Events)
	}
}

// TestHandler_PipeModeUnmanagedSourceDoesNotBorrowOwner covers the common
// layout of one plain split beside a managed harness pane: a background
// `claude -p` in the plain pane must not drive the managed session's icons,
// while an interactive run in the same pane still borrows the owner.
func TestHandler_PipeModeUnmanagedSourceDoesNotBorrowOwner(t *testing.T) {
	stubDaemonLoopDiscover(t, nil, nil)
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: unmanagedRouteWindows("api", harness.Codex, true)})

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    43,
		Event:       "UserPromptSubmit",
		Harness:     harness.Claude,
		PipeMode:    true,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "pipe-run"}),
	})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}

	if owner := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}); len(owner.Events) != 0 {
		t.Fatalf("owner events = %+v; want the pipe run kept off the managed session", owner.Events)
	}
	h.stateMu.Lock()
	ownerState := h.store[42]
	h.stateMu.Unlock()
	if ownerState != nil && ownerState.DesiredIcon != "" {
		t.Fatalf("owner icon = %q; want no icon from a background pipe run", ownerState.DesiredIcon)
	}

	source := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(source.Events) != 1 || !source.Events[0].Suppressed ||
		source.Events[0].SuppressionReason != suppressionReasonUnmanagedPipeSource {
		t.Fatalf("source events = %+v; want unmanaged_pipe_source suppression", source.Events)
	}

	// Regression guard: the same pane running interactively still borrows the
	// owner exactly as before.
	if resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    43,
		Event:       "UserPromptSubmit",
		Harness:     harness.Claude,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "interactive-run"}),
	}); !resp.OK {
		t.Fatalf("interactive hook response = %+v", resp)
	}
	owner := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(owner.Events) != 1 || owner.Events[0].Suppressed ||
		owner.Events[0].RouteDecision != routeDecisionOwner ||
		owner.Events[0].SourceKey != "claude:interactive-run" {
		t.Fatalf("owner events = %+v; want the interactive run routed to the owner", owner.Events)
	}
}

// TestHandler_PipeModeUnmanagedSourceRoutesWithExactLoopCandidate verifies the
// ASL exception survives the pipe-mode gate: an `asl run` drives its harness
// through pipe mode, and an exact live loop candidate is independent
// authorization to borrow the managed owner.
func TestHandler_PipeModeUnmanagedSourceRoutesWithExactLoopCandidate(t *testing.T) {
	projectDir := t.TempDir()
	stubDaemonLoopDiscover(t, []agentloop.Candidate{aslCandidate(43, projectDir)}, nil)

	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: aslSiblingWindows("api", projectDir, 42, 43)})

	resp := h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    43,
		Event:       "UserPromptSubmit",
		Harness:     harness.Claude,
		PipeMode:    true,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "loop-run"}),
	})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}

	owner := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(owner.Events) != 1 || owner.Events[0].Suppressed ||
		owner.Events[0].RouteDecision != routeDecisionOwner ||
		owner.Events[0].SourceKey != "claude:loop-run" {
		t.Fatalf("owner events = %+v; want the loop's hook routed to the owner", owner.Events)
	}
}

// TestHandler_PipeModeOwnerlessTabStrandsNoIcon covers a tab holding only plain
// panes. Without the guard the source path creates durable state that renders
// an icon on a terminal that owns no session.
func TestHandler_PipeModeOwnerlessTabStrandsNoIcon(t *testing.T) {
	stubDaemonLoopDiscover(t, nil, nil)
	h := newTestHandler()
	fake := &fakeKittyClient{windows: hookEventWindows("api", []kitty.Window{
		{ID: 43, IsActive: true},
		{ID: 44, IsActive: false},
	})}
	h.SetKittyClient(fake)

	// The prompt is what strands the icon: ungated it sets a durable bolt that
	// the pipe run's own Stop then declines to retire.
	for _, event := range []string{"UserPromptSubmit", "Stop"} {
		if resp := h.Handle(Request{
			Type:        TypeHookEvent,
			WindowID:    43,
			Event:       event,
			Harness:     harness.Claude,
			PipeMode:    true,
			PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "pipe-run"}),
		}); !resp.OK {
			t.Fatalf("%s response = %+v", event, resp)
		}
	}

	h.stateMu.Lock()
	st := h.store[43]
	h.stateMu.Unlock()
	if st != nil && (st.DesiredIcon != "" || st.StateSet || st.Effective.Icon != "") {
		t.Fatalf("plain pane state = %+v; want no icon state", st)
	}
	if calls := fake.titleCalls(); len(calls) != 0 {
		t.Fatalf("SetTabTitle calls = %+v; want none", calls)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 2 {
		t.Fatalf("source events = %+v; want both events recorded as suppressed", detail.Events)
	}
	for _, event := range detail.Events {
		if !event.Suppressed || event.SuppressionReason != suppressionReasonUnmanagedPipeSource {
			t.Fatalf("source event = %+v; want unmanaged_pipe_source suppression", event)
		}
	}
}

// TestHandler_PipeModeOwnerlessTabStillMarksLoop verifies the ownerless guard
// excludes ASL lifecycle events, so `asl run` in a plain-only tab keeps its
// loop marker.
func TestHandler_PipeModeOwnerlessTabStillMarksLoop(t *testing.T) {
	stubDaemonLoopDiscover(t, nil, nil)
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: hookEventWindows("api", []kitty.Window{
		{ID: 43, IsActive: true},
	})})

	resp := h.Handle(Request{
		Type:     TypeHookEvent,
		WindowID: 43,
		Event:    "AgentSessionLoopStarted",
		PipeMode: true,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{
			Source: "agent_session_loop", Harness: harness.Claude, RunID: "run-1", CWD: t.TempDir(),
		}),
	})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}

	h.stateMu.Lock()
	st := h.store[43]
	looping := st != nil && st.Effective.Looping
	h.stateMu.Unlock()
	if !looping {
		t.Fatalf("loop state = %+v; want an active loop marker", st)
	}
}

func TestHandler_ManagedNonOwnerPersistsWithoutRenderingUntilFocused(t *testing.T) {
	title := palette.BuildTitle("api", "error", false)
	windows := hookEventWindows(title, []kitty.Window{
		{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "one", Harness: harness.Claude}},
		{ID: 43, IsActive: false, UserVars: kitty.UserVars{Profile: "two", Harness: harness.Codex}},
	})
	fake := &fakeKittyClient{windows: windows}
	h := newTestHandler()
	h.SetKittyClient(fake)

	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 43, Event: "PreToolUse", Harness: harness.Codex,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "managed-two"}),
	}); !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 1 || detail.Events[0].Suppressed || detail.Events[0].CurrentIconState != "" {
		t.Fatalf("managed non-owner events = %+v; want its own empty -> bolt transition", detail.Events)
	}
	h.renderSnapshot(windows)
	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("inactive managed pane wrote %d titles; want 0", got)
	}
	if got := len(fake.userVarsCalls()); got == 0 {
		t.Fatalf("inactive managed pane did not persist its state")
	}

	focused := hookEventWindows(title, []kitty.Window{
		{ID: 42, IsActive: false, UserVars: kitty.UserVars{Profile: "one", Harness: harness.Claude}},
		{ID: 43, IsActive: true, UserVars: kitty.UserVars{Profile: "two", Harness: harness.Codex}},
	})
	h.publishSnapshot(focused, time.Now())
	h.renderSnapshot(focused)
	calls := fake.titleCalls()
	if len(calls) != 1 || palette.ParseTitleParts(calls[0].title).State != "bolt" {
		t.Fatalf("title calls = %+v; want focused pane's bolt", calls)
	}
}

func TestHandler_OwnerlessStampedCodexKeepsCodexIdentity(t *testing.T) {
	h := newTestHandler()
	h.SetKittyClient(&fakeKittyClient{windows: hookEventWindows("scratch", []kitty.Window{{ID: 43, IsActive: true}})})

	if resp := h.Handle(Request{
		Type: TypeHookEvent, WindowID: 43, Event: "UserPromptSubmit", Harness: harness.Codex,
		PayloadJSON: hookPayloadJSON(t, hookevent.HookPayload{SessionID: "ownerless"}),
	}); !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(detail.Events) != 1 || detail.Events[0].SourceKey != "codex:ownerless" ||
		!strings.EqualFold(detail.Events[0].SourceKind, string(SourceHarness)) {
		t.Fatalf("events = %+v; want ownerless Codex source", detail.Events)
	}
	h.stateMu.Lock()
	meta := h.windowMetadata[43]
	h.stateMu.Unlock()
	if meta.Durable.Harness != harness.Codex {
		t.Fatalf("durable harness = %q; want codex", meta.Durable.Harness)
	}
}
