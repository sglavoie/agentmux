package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.Event != screenPromptProbeEvent || ev.Source != screenPromptProbeSource {
		t.Fatalf("event identity = (%q, %q), want (%q, %q)", ev.Event, ev.Source, screenPromptProbeEvent, screenPromptProbeSource)
	}
	if ev.PromptKind != string(activity.CodexPromptEditApproval) || ev.Action != "elicitation" {
		t.Fatalf("event prompt/action = (%q, %q), want (%q, elicitation)", ev.PromptKind, ev.Action, activity.CodexPromptEditApproval)
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

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.PromptKind != string(activity.CodexPromptCommandApproval) || ev.Action != "permission" || ev.ResultIconState != "key" {
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

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.PromptKind != string(activity.CodexPromptRequestUserInput) || ev.Action != "elicitation" {
		t.Fatalf("event prompt/action = (%q, %q), want (%q, elicitation)", ev.PromptKind, ev.Action, activity.CodexPromptRequestUserInput)
	}
	if ev.ResultIconState != "question" || ev.CurrentIconState != "bolt" {
		t.Fatalf("event icons = current %q result %q, want bolt -> question", ev.CurrentIconState, ev.ResultIconState)
	}
}

func TestProbeCodexScreenPrompts_RequestUserInputOnlyWhileVisibleWithoutPostToolUse(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexWorkingTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// Native PreToolUse(request_user_input) leaves the session active while
	// Codex is still rendering the prompt.
	h.ProbeCodexScreenPrompts(base)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("pre-render working screen = %+v, want bolt", eff)
	}

	fake.mu.Lock()
	fake.getText[kitty.MatchWindowByID(42)] = codexRequestUserInputScreen
	fake.mu.Unlock()
	h.ProbeCodexScreenPrompts(base.Add(time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "question" {
		t.Fatalf("visible question = %+v, want question", eff)
	}
	if got := h.guard.Current(42); got != "elicitation" {
		t.Fatalf("visible question guard = %q, want elicitation", got)
	}

	// No PostToolUse arrives. Once the prompt is no longer visible, the probe
	// must withdraw both its fact and its guard, revealing native activity.
	fake.mu.Lock()
	fake.getText[kitty.MatchWindowByID(42)] = codexWorkingTurnScreen
	fake.mu.Unlock()
	h.ProbeCodexScreenPrompts(base.Add(2 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("answered prompt without PostToolUse = %+v, want underlying bolt", eff)
	}
	if got := h.guard.Current(42); got != "" {
		t.Fatalf("answered prompt guard = %q, want cleared", got)
	}
	h.stateMu.Lock()
	probeFact := factOf(h.store[42].Reducer, protocol.SourceID{Kind: protocol.SourceProbe, Key: screenPromptProbeSource})
	lastAction := h.store[42].LastAction
	durableAction := h.windowMetadata[42].Durable.LastAction
	h.stateMu.Unlock()
	if probeFact != nil && probeFact.Icon != "" {
		t.Fatalf("answered prompt retained visible screen contribution: %+v", probeFact)
	}
	if lastAction != "active" || durableAction != "active" {
		t.Fatalf("answered prompt actions = state %q durable %q, want active", lastAction, durableAction)
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
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}
