package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeClaudeContextLimit_NoMarker_NoOp(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): "ordinary shell output\n",
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
	h.stateMu.Lock()
	st, ok := h.store[42]
	notified := ok && st.ContextLimitNotified
	h.stateMu.Unlock()
	if notified {
		t.Fatalf("contextLimitNotified[42] = true, want false")
	}
}

func TestProbeClaudeContextLimit_RealScreen_BoltToError(t *testing.T) {
	fixture := loadClaudeContextLimitFixture(t)

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): fixture,
		},
	}
	h.SetKittyClient(fake)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeClaudeContextLimit(now)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(42) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(42))
	}
	if want := palette.BuildTitle("api", "error", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.Event != claudeContextLimitProbeEvent || ev.Source != claudeContextLimitProbeSource {
		t.Fatalf("event identity = (%q, %q), want (%q, %q)", ev.Event, ev.Source, claudeContextLimitProbeEvent, claudeContextLimitProbeSource)
	}
	if ev.Action != "stop_failure" || ev.ResultIconState != "error" || ev.CurrentIconState != "bolt" {
		t.Fatalf("event = %+v, want stop_failure bolt->error", ev)
	}
	if !ev.ContextLimit {
		t.Fatalf("event.ContextLimit = false, want true")
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	notified := h.store[42].ContextLimitNotified
	lastAction := h.store[42].LastAction
	h.stateMu.Unlock()
	if meta.Durable.Harness != "claude" || meta.Durable.Profile != "work" || meta.Durable.LastAction != "stop_failure" {
		t.Fatalf("durable metadata = %+v, want claude/work/stop_failure", meta.Durable)
	}
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false, want true")
	}
	if lastAction != "stop_failure" {
		t.Fatalf("lastAction[42] = %q, want stop_failure", lastAction)
	}
}

func TestProbeClaudeContextLimit_RealScreen_EmptyToError(t *testing.T) {
	fixture := loadClaudeContextLimitFixture(t)

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): fixture,
		},
	}
	h.SetKittyClient(fake)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeClaudeContextLimit(now)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("api", "error", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.Event != claudeContextLimitProbeEvent || ev.Source != claudeContextLimitProbeSource {
		t.Fatalf("event identity = (%q, %q), want (%q, %q)", ev.Event, ev.Source, claudeContextLimitProbeEvent, claudeContextLimitProbeSource)
	}
	if ev.Action != "stop_failure" || ev.ResultIconState != "error" || ev.CurrentIconState != "" {
		t.Fatalf("event = %+v, want stop_failure (empty)->error", ev)
	}
	if !ev.ContextLimit {
		t.Fatalf("event.ContextLimit = false, want true")
	}

	h.stateMu.Lock()
	notified := h.store[42].ContextLimitNotified
	h.stateMu.Unlock()
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false, want true")
	}
}
