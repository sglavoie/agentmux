package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
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
	guard := NewStateGuard(time.Hour)
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

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

func TestProbeClaudeContextLimit_EmptyStateSwapsToError(t *testing.T) {
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

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}); len(resp.Events) != 1 {
		t.Fatalf("owner events = %d, want 1", len(resp.Events))
	}
	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43}); len(resp.Events) != 0 {
		t.Fatalf("non-owner events = %d, want 0", len(resp.Events))
	}
}

func TestProbeClaudeContextLimit_SplitTabNonOwnerCannotOverwriteManagedOwner(t *testing.T) {
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
			kitty.MatchWindowByID(42): "ordinary shell output\n",
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}); len(resp.Events) != 0 {
		t.Fatalf("owner events = %d, want 0", len(resp.Events))
	}
	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43}); len(resp.Events) != 0 {
		t.Fatalf("non-owner events = %d, want 0", len(resp.Events))
	}
}

func TestProbeClaudeContextLimit_UnmanagedClaudeForegroundNoOp(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("api", "bolt", false),
			[]string{"/opt/homebrew/bin/claude"},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

func TestProbeClaudeContextLimit_DedupesWithinEpisode(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeClaudeContextLimit(now)
	// Simulate the title actually changing on the next tab listing so the
	// probe's title-overwrite guard reflects reality.
	fake.mu.Lock()
	fake.windows[0].Tabs[0].Title = palette.BuildTitle("api", "error", false)
	fake.mu.Unlock()
	h.ProbeClaudeContextLimit(now.Add(2 * time.Second))

	if got := len(fake.titleCalls()); got != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (dedup should skip the second call)", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1 (dedup should skip the second event)", len(resp.Events))
	}
}

// TestProbeClaudeContextLimit_PreRecordedPipeModeReachesNotifier pins the Bug 5
// fix: the probe must capture lastPipeMode BEFORE recordClaudeContextLimitProbeState
// resets it to false, so MaybeNotify sees the value hooks last reported. A
// pre-recorded pipe-mode window therefore gets the ❗ icon swap but no dispatch.
func TestProbeClaudeContextLimit_PreRecordedPipeModeReachesNotifier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newClaudeContextLimitProbeTestHandler()
		fake.windows = codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		})
		fake.getText = map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		}
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).LastPipeMode = true
		h.stateMu.Unlock()

		h.ProbeClaudeContextLimit(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("pipe-mode window dispatches = %d; want 0 (pre-recorded pipeMode must reach MaybeNotify)", got)
		}
		if got := len(fake.titleCalls()); got != 1 {
			t.Fatalf("SetTabTitle calls = %d, want 1 (icon swap is independent of notification)", got)
		}
	})
}

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
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
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
