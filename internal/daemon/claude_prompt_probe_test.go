package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestProbeClaudeScreenStop_CompletedIdleStopsStuckBoltAfterTwoSamples drives
// the normal-completion recovery through the authoritative-idle path. A stuck ⚡
// backed by a real interactive UserPromptSubmit fact must only settle to 🛑 once
// the composer-idle screen has agreed across two consecutive samples: a single
// transient idle frame must not settle live work.
func TestProbeClaudeScreenStop_CompletedIdleStopsStuckBoltAfterTwoSamples(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeIdleScreen,
		},
	}
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// First sample: not enough evidence yet, the bolt is preserved.
	h.ProbeClaudeScreenStop(base)
	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("first-sample SetTabTitle calls = %d, want 0", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("after one sample effective = %+v, want bolt", eff)
	}

	// Second sample agrees: the stale bolt is retired and the tab converges to 🛑.
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))
	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("second-sample SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "stop", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("effective = %+v visible %q, want stop/stop", eff, visible)
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent)
	if !ok {
		t.Fatalf("no %q row in %+v", screenStopProbeEvent, resp.Events)
	}
	if ev.Action != "stop" {
		t.Fatalf("ScreenStopProbe action = %q, want stop", ev.Action)
	}
	if ev.Source != string(SourceProbe)+":"+claudeScreenStopProbeSource {
		t.Fatalf("ScreenStopProbe source = %q, want %q", ev.Source, string(SourceProbe)+":"+claudeScreenStopProbeSource)
	}
}

// TestProbeClaudeScreenStop_InterruptedStopsStuckBoltImmediately proves the
// interruption path is treated as immediate idle evidence: a single probe with
// the interruption marker on screen settles the stale bolt, no second sample
// required.
func TestProbeClaudeScreenStop_InterruptedStopsStuckBoltImmediately(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeInterruptedScreen,
		},
	}
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeClaudeScreenStop(base)

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

// TestProbeClaudeScreenStop_ActiveSpinnerKeepsBolt confirms a still-working
// Claude turn (esc-to-interrupt hint on screen) never settles, even across
// several samples.
func TestProbeClaudeScreenStop_ActiveSpinnerKeepsBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeWorkingScreen,
		},
	}
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeClaudeScreenStop(base)
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))
	h.ProbeClaudeScreenStop(base.Add(4 * time.Second))

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

// TestProbeClaudeScreenStop_ProtectedScreensKeepBolt walks the blocking/protected
// screen shapes the idle probe must never settle: a visible permission prompt,
// queued composer input, and the context-limit recovery banner (which the
// dedicated context-limit probe owns).
func TestProbeClaudeScreenStop_ProtectedScreensKeepBolt(t *testing.T) {
	cases := []struct {
		name   string
		screen string
	}{
		{name: "permission prompt", screen: claudePermissionScreen},
		{name: "queued input", screen: claudeQueuedInputScreen},
		{name: "context limit banner", screen: claudeContextLimitStopScreen},
		{name: "background task", screen: claudeBackgroundTaskScreen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler()
			base := time.Now()
			fake := &fakeKittyClient{
				windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
					Harness: "claude",
				}),
				getText: map[string]string{
					kitty.MatchWindowByID(42): tc.screen,
				},
			}
			h.SetKittyClient(fake)
			claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

			h.ProbeClaudeScreenStop(base)
			h.ProbeClaudeScreenStop(base.Add(2 * time.Second))

			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
				t.Fatalf("effective = %+v, want bolt preserved", eff)
			}
			resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
			if ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent); ok {
				t.Fatalf("ScreenStopProbe row recorded for a protected screen: %+v", ev)
			}
		})
	}
}

// TestProbeClaudeScreenStop_ConcurrentPromptInvalidatesStopProbe proves the
// captured-sequence guard: a fresh prompt landing during the (slow) screen read
// advances the reducer receipt past the value the probe captured, so the stale
// interruption read is discarded and the newer interactive bolt survives.
func TestProbeClaudeScreenStop_ConcurrentPromptInvalidatesStopProbe(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeInterruptedScreen,
		},
	}
	claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// The idle probe captures the receipt sequence under stateMu before its
	// screen read. Seeding a fresh prompt during that read models a concurrent
	// UserPromptSubmit landing after the capture.
	fake.beforeGetText = func(match, extent string) {
		if match != kitty.MatchWindowByID(42) || extent != "screen" {
			return
		}
		claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix()+1)
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeScreenStop(base)

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the newer bolt preserved against a stale stop probe", eff)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent); ok {
		t.Fatalf("ScreenStopProbe row recorded despite a concurrent prompt: %+v", ev)
	}
}

// TestProbeClaudeScreenStop_IgnoresNonClaudeManagedOwner confirms the probe only
// settles Claude-owned tabs: a Codex-managed owner showing the same idle screen
// is left alone.
func TestProbeClaudeScreenStop_IgnoresNonClaudeManagedOwner(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeInterruptedScreen,
		},
	}
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeClaudeScreenStop(base)

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want bolt preserved for non-claude owner", eff)
	}
}

// TestProbeClaudeScreenStop_UnmanagedForegroundNotProbed confirms Claude — unlike
// the Codex prompt probe — has no unmanaged-foreground fallback: a tab with no
// managed title owner is never screen-read.
func TestProbeClaudeScreenStop_UnmanagedForegroundNotProbed(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("W:api", "bolt", false),
			[]string{"/opt/homebrew/bin/claude"},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeInterruptedScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeScreenStop(base)
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0 for an unmanaged foreground tab", len(resp.Events))
	}
}

// claudeSeedActiveBolt folds a real session-scoped Claude UserPromptSubmit fact
// into a window so its stuck ⚡ is backed by a live interactive reducer fact —
// the reducer precedence a synthetic screen stop must defeat. It mirrors
// codexSeedActiveBolt: the dirty mark left by seeding is cleared so the probe's
// own render is the only title write the fake observes.
func claudeSeedActiveBolt(t *testing.T, h *Handler, windowID int, session string, at int64) uint64 {
	t.Helper()
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "claude:" + session},
		Action:    "active",
		SessionID: session,
		At:        at,
		Event:     "UserPromptSubmit",
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "active", Event: "UserPromptSubmit", normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed claude active %q rejected: %+v", session, resp)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[windowID]
	st.Dirty = false
	st.DirtySince = time.Time{}
	return st.ShadowSeq
}
