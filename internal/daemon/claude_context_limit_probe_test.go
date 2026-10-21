package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const claudeContextLimitFixtureFile = "claude_context_limit_full.txt"

func loadClaudeContextLimitFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "contextlimit", "testdata", claudeContextLimitFixtureFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return string(data)
}

// newClaudeContextLimitProbeTestHandler builds a Handler whose notifier
// records dispatches via callRecorder so probe tests can assert that
// MaybeNotify fired exactly once per stall episode.
func newClaudeContextLimitProbeTestHandler() (*Handler, *fakeKittyClient, *callRecorder) {
	guard := activity.NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(guard, notifier)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	return h, fake, rec
}

const claudeContextLimitScreen = `earlier line

Context limit reached · /compact or /clear to continue

✻ Cooked for 17m 6s
`

func TestProbeClaudeContextLimit_BoltSwapsToError(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
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

// TestProbeClaudeContextLimit_PublishesReducerErrorAndPreservesItAcrossIdle pins
// the F1 repair: a detected context-limit banner applies a SourceProbe
// stop_failure fact THROUGH the reducer — advancing the window-wide receipt
// generation (ShadowSeq) and publishing the actual reduced result — so the
// rendered error stays consistent with the authoritative reducer and durable
// shadow (DesiredIcon, Effective, and the exported Shadow effective fields all
// agree). A passive idle after detection preserves the probe-owned error, while
// an explicit user acknowledgement (user_clear) and a new prompt both clear it
// through the normal reducer recovery paths.
func TestProbeClaudeContextLimit_PublishesReducerErrorAndPreservesItAcrossIdle(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:      100,
					Title:   palette.BuildTitle("api", "bolt", false),
					Windows: []kitty.Window{{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "claude"}}},
				},
				{
					ID:      101,
					Title:   palette.BuildTitle("web", "bolt", false),
					Windows: []kitty.Window{{ID: 43, UserVars: kitty.UserVars{Profile: "work", Harness: "claude"}}},
				},
			},
		}},
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeClaudeContextLimit(now)

	probeSource := protocol.SourceID{Kind: protocol.SourceProbe, Key: claudeContextLimitFactSource}

	h.stateMu.Lock()
	st := h.store[42]
	seqAfterProbe := st.ShadowSeq
	reduced := st.Reducer.Reduce(now.Unix())
	desired := st.DesiredIcon
	stEffective := st.Effective.Icon
	shadowIcon := h.windowMetadata[42].Shadow.EffectiveIcon
	h.stateMu.Unlock()

	if seqAfterProbe == 0 {
		t.Fatalf("ShadowSeq = 0 after probe; want a bumped receipt generation")
	}
	// The banner is a real reducer fact, not a bare scalar write.
	if reduced.Icon != "error" || reduced.Source != probeSource {
		t.Fatalf("reduced = %+v, want error owned by %+v", reduced, probeSource)
	}
	// The published visible state agrees across every consumer.
	if desired != "error" || stEffective != "error" || shadowIcon != "error" {
		t.Fatalf("inconsistent published state: DesiredIcon=%q Effective=%q Shadow=%q, want all error",
			desired, stEffective, shadowIcon)
	}

	// A passive idle from another source must not withdraw the probe-owned error.
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "idle", Harness: "claude"})
	if !resp.OK {
		t.Fatalf("idle state update failed: %s", resp.Error)
	}
	h.stateMu.Lock()
	afterIdleEffective := h.store[42].Effective.Icon
	afterIdleDesired := h.store[42].DesiredIcon
	h.stateMu.Unlock()
	if afterIdleEffective != "error" || afterIdleDesired != "error" {
		t.Fatalf("after passive idle: Effective=%q DesiredIcon=%q, want error preserved", afterIdleEffective, afterIdleDesired)
	}

	// An explicit user acknowledgement clears the sticky error.
	resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: "claude"})
	if !resp.OK {
		t.Fatalf("user_clear state update failed: %s", resp.Error)
	}
	h.stateMu.Lock()
	afterClear := h.store[42].Effective.Icon
	h.stateMu.Unlock()
	if afterClear != "" {
		t.Fatalf("after user_clear: Effective=%q, want cleared", afterClear)
	}

	// A new prompt on the sibling window is authoritative recovery: it
	// acknowledges the sticky error and the session's ⚡ returns.
	resp = h.Handle(protocol.Request{
		Type:         protocol.TypeStateUpdate,
		WindowID:     43,
		Action:       "active",
		Event:        "UserPromptSubmit",
		IsPrompt:     true,
		Harness:      "claude",
		SessionTitle: "web",
	})
	if !resp.OK {
		t.Fatalf("prompt state update failed: %s", resp.Error)
	}
	h.stateMu.Lock()
	afterPrompt := h.store[43].Effective.Icon
	h.stateMu.Unlock()
	if afterPrompt != "bolt" {
		t.Fatalf("after new prompt: Effective=%q, want bolt (error cleared)", afterPrompt)
	}
}

func TestProbeClaudeContextLimit_EmptyStateSwapsToError(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
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

func TestProbeClaudeContextLimit_SubagentSwapsToError(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "subagent", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("api", "error", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestProbeClaudeContextLimit_PreservesLoopMarkerAndHarnessEmoji(t *testing.T) {
	t.Parallel()

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
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "🦊api",
		State:   "error",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestProbeClaudeContextLimit_SplitTabOwnerStillDetectsContextLimit(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("api", "bolt", false),
			kitty.Window{
				ID:       42,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
			},
			kitty.Window{
				ID:       43,
				UserVars: kitty.UserVars{Profile: "scratch", Harness: "claude"},
			},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

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

	if resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42}); len(resp.Events) != 1 {
		t.Fatalf("owner events = %d, want 1", len(resp.Events))
	}
	if resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 43}); len(resp.Events) != 0 {
		t.Fatalf("non-owner events = %d, want 0", len(resp.Events))
	}
}
