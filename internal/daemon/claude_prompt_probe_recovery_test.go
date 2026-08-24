package daemon

// What the Claude screen probe must never overrule. The probe is a synthetic
// observer, so every fact with a stronger claim wins over it: an explicit stop,
// a terminal event that lands mid-read, a blocking or protected screen, a
// concurrent prompt, a tab owned by another harness, and a tab nobody manages.
// Shared seeding and event-counting helpers live here too. The stale-title
// promotion rules are in claude_prompt_probe_stale_title_test.go and the
// multi-sample settle gate in claude_prompt_probe_marker_run_test.go.

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeClaudeScreenStop_BusyPaneTitleDoesNotOverrideStop(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "stop", false), kitty.UserVars{
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "\u2733 Repair advisor calibration"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	stop := NormalizedEvent{
		Source: SourceID{Kind: SourceHarness, Key: "claude:sess-stop"},
		Action: "stop", SessionID: "sess-stop", Event: "Stop", At: base.Unix(),
	}
	if resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop", normalized: &stop}); !resp.OK {
		t.Fatalf("seed stop response = %+v", resp)
	}

	h.ProbeClaudeScreenStop(base.Add(time.Second))

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective with busy title over stop = %+v, want stop preserved", eff)
	}
	if ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenActiveProbeEvent); ok {
		t.Fatalf("ScreenActiveProbe row recorded over explicit stop: %+v", ev)
	}
}

// TestProbeClaudeScreenStop_ConcurrentTerminalInvalidatesBusyRecovery proves the
// receipt captured before ListWindows prevents a busy snapshot from replacing a
// terminal reducer event that lands while Kitty is producing that snapshot.
func TestProbeClaudeScreenStop_ConcurrentTerminalInvalidatesBusyRecovery(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "\u2733 Repair advisor calibration"
	fake := &fakeKittyClient{windows: windows}
	h := newTestHandler()
	h.SetKittyClient(fake)

	injected := false
	fake.beforeListWindows = func() {
		if injected {
			return
		}
		injected = true
		stop := NormalizedEvent{
			Source: SourceID{Kind: SourceHarness, Key: "claude:sess-race"},
			Action: "stop", SessionID: "sess-race", Event: "Stop", At: base.Unix(),
		}
		if resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop", normalized: &stop}); !resp.OK {
			t.Fatalf("concurrent stop response = %+v", resp)
		}
	}

	h.ProbeClaudeScreenStop(base.Add(time.Second))

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after concurrent stop = %+v, want stop", eff)
	}
	if ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenActiveProbeEvent); ok {
		t.Fatalf("ScreenActiveProbe row recorded from stale busy snapshot: %+v", ev)
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

	// Two ticks: the interrupted screen would otherwise complete its stabilization
	// run here, so the receipt guard — not the sample count — is what holds the
	// bolt.
	h.ProbeClaudeScreenStop(base)
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))

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

	// Enough ticks to complete the longest stabilization run: a Claude-owned tab
	// showing this screen would have settled by now.
	for sample := 0; sample < claudeIdleProbeLongestRun; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(2*sample) * time.Second))
	}

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

// TestProbeClaudeScreenStop_ConfirmsHeldStopInsteadOfSettling proves the probe
// keeps running while a native stop is withheld — the target is still ⚡, so the
// bolt eligibility gate passes — and that its settle is consumed as confirmation
// of that held stop instead of painting a synthetic one. The sample requirement
// is unchanged by the hold: Claude still needs two agreeing screen reads.
func TestProbeClaudeScreenStop_ConfirmsHeldStopInsteadOfSettling(t *testing.T) {
	h, _ := newTerminalHoldProbeHandler(t, harness.Claude, claudeIdleScreen)
	releaseHoldAtTestEnd(t, h, 42)
	base := time.Now()
	armTerminalHold(t, h)

	h.ProbeClaudeScreenStop(base)
	if pending, ok := terminalHoldPending(t, h, 42); !ok || pending.confirmed {
		t.Fatalf("hold after one agreeing screen = %+v/%v, want an unconfirmed hold", pending, ok)
	}

	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))
	pending, ok := terminalHoldPending(t, h, 42)
	if !ok || !pending.confirmed || pending.confirmedBy != StopConfirmedByClaudeScreen {
		t.Fatalf("hold after two agreeing screens = %+v/%v, want confirmed by %s", pending, ok, StopConfirmedByClaudeScreen)
	}

	// The held stop still owns the transition: the probe settled nothing of its
	// own and the tab stays ⚡ until the hold commits.
	assertNoProbeStopFact(t, h, 42)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("icon while the confirmed hold waits = %q, want bolt", eff.Icon)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent); ok {
		t.Fatalf("probe recorded %+v; a confirmation is not a synthetic stop", ev)
	}
}

func countEventsNamed(t *testing.T, h *Handler, windowID int, name string) int {
	t.Helper()
	count := 0
	for _, event := range h.Handle(Request{Type: TypeSessionDetail, WindowID: windowID}).Events {
		if event.Event == name {
			count++
		}
	}
	return count
}

func windowBusySince(t *testing.T, h *Handler, windowID int) time.Time {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("no window state for %d", windowID)
	}
	return st.BusySince
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
