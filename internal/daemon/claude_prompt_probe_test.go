package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
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

// TestProbeClaudeScreenStop_BusyPaneTitleKeepsBolt covers Claude builds that
// leave an empty composer in the screen body while publishing the only active
// work marker through Kitty's pane title. This is the live Pulse regression:
// the false stop used to paint until the next tool hook restored the bolt.
func TestProbeClaudeScreenStop_BusyPaneTitleKeepsBolt(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "\u2733 Fix drawer drag resistance"
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeIdleScreen,
		},
	}
	notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
	h := NewHandler(NewStateGuard(time.Hour), notifier)
	notifier.SetIsMuted(h.IsMuted)
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-title-busy", base.Unix())

	reads := 0
	fake.beforeGetText = func(match, extent string) {
		if match == kitty.MatchWindowByID(42) && extent == "screen" {
			reads++
		}
	}
	for _, elapsed := range []time.Duration{0, 2 * time.Second, 4 * time.Second} {
		h.ProbeClaudeScreenStop(base.Add(elapsed))
	}

	if reads != 0 {
		t.Fatalf("screen reads with busy pane title = %d, want 0", reads)
	}
	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls with busy pane title = %d, want 0", got)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective with busy pane title = %+v, want bolt", eff)
	}
	if ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenStopProbeEvent); ok {
		t.Fatalf("ScreenStopProbe row recorded for busy pane title: %+v", ev)
	}
	if ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenActiveProbeEvent); ok {
		t.Fatalf("ScreenActiveProbe row repeated for an existing bolt: %+v", ev)
	}
	if got := rec.len(); got != 0 {
		t.Fatalf("notifications with busy pane title = %d, want 0 (%+v)", got, rec.snapshot())
	}

	// Once the pane title itself becomes idle, the existing two-sample screen
	// fallback remains authoritative and converges the stuck bolt to stop.
	fake.mu.Lock()
	fake.windows[0].Tabs[0].Windows[0].Title = "Fix drawer drag resistance"
	fake.mu.Unlock()
	h.ProbeClaudeScreenStop(base.Add(6 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after first fully-idle sample = %+v, want bolt", eff)
	}
	h.ProbeClaudeScreenStop(base.Add(8 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after second fully-idle sample = %+v, want stop", eff)
	}
}

// TestProbeClaudeScreenStop_BusyPaneTitleRestoresEmptyState covers the missing
// positive half of the pane-title probe. A managed Claude owner can have no
// reducer state after restore or missed hooks; the current busy marker alone is
// sufficient to recover ⚡ without scraping the empty composer.
func TestProbeClaudeScreenStop_BusyPaneTitleRestoresEmptyState(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "\u2733 Repair advisor calibration"
	fake := &fakeKittyClient{
		windows: windows,
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeIdleScreen,
		},
	}
	reads := 0
	fake.beforeGetText = func(match, extent string) {
		if match == kitty.MatchWindowByID(42) && extent == "screen" {
			reads++
		}
	}
	h := newTestHandler()
	h.SetKittyClient(fake)

	h.ProbeClaudeScreenStop(base)

	if reads != 0 {
		t.Fatalf("screen reads for busy-title recovery = %d, want 0", reads)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || eff.Looping || visible != "bolt" {
		t.Fatalf("recovered effective = %+v visible=%q, want non-looping bolt", eff, visible)
	}
	if calls := fake.titleCalls(); len(calls) != 1 || calls[0].title != palette.BuildTitle("W:api", "bolt", false) {
		t.Fatalf("SetTabTitle calls = %+v, want one recovered bolt title", calls)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	ev, ok := lastEventNamed(detail.Events, screenActiveProbeEvent)
	if !ok {
		t.Fatalf("no %q row in %+v", screenActiveProbeEvent, detail.Events)
	}
	if ev.Action != "bolt" || ev.Source != string(SourceProbe)+":"+claudeScreenStopProbeSource {
		t.Fatalf("ScreenActiveProbe = %+v, want actual bolt from Claude screen source", ev)
	}

	// Repeated busy snapshots keep the recovered bolt without fact or event churn.
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))
	detail = h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	count := 0
	for _, event := range detail.Events {
		if event.Event == screenActiveProbeEvent {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("ScreenActiveProbe rows after repeated busy snapshot = %d, want 1", count)
	}

	// Once the marker disappears, the same probe fact follows the established
	// two-sample idle path from active to stop.
	fake.mu.Lock()
	fake.windows[0].Tabs[0].Windows[0].Title = "Repair advisor calibration"
	fake.mu.Unlock()
	h.ProbeClaudeScreenStop(base.Add(4 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after first idle sample = %+v, want recovered bolt retained", eff)
	}
	h.ProbeClaudeScreenStop(base.Add(6 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after second idle sample = %+v, want stop", eff)
	}
}

// TestProbeClaudeScreenStop_BusyPaneTitleRecoversAfterUserClearPreservingLoop
// pins the reported sequence. F8 acknowledges the icon contribution but keeps
// the live ASL lease; the next busy-title probe restores ⚡ without inventing or
// withdrawing the independent 🔁 marker.
func TestProbeClaudeScreenStop_BusyPaneTitleRecoversAfterUserClearPreservingLoop(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", true), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "\u2733 Repair advisor calibration"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-clear", base.Unix())

	h.stateMu.Lock()
	st := h.store[42]
	st.ShadowSeq++
	if decision := st.Reducer.Apply(ReducerEvent{
		Source: SourceID{Kind: SourceASL, Key: "run-1"},
		RunID:  "run-1", Phase: ASLPhaseBetween, Seq: 1, At: base.Unix(),
	}); decision != DecisionApplied {
		h.stateMu.Unlock()
		t.Fatalf("seed ASL lease decision = %q, want applied", decision)
	}
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(base.Unix()))
	h.stateMu.Unlock()

	if resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: harness.Claude}); !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "" || !eff.Looping {
		t.Fatalf("after user_clear effective = %+v, want bare live loop", eff)
	}

	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" || !eff.Looping {
		t.Fatalf("after busy-title recovery effective = %+v, want bolt + preserved loop", eff)
	}
	if ev, ok := lastEventNamed(h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}).Events, screenActiveProbeEvent); !ok || ev.Action != "bolt" {
		t.Fatalf("ScreenActiveProbe row = %+v ok=%v, want bolt", ev, ok)
	}
}

// TestProbeClaudeScreenStop_BusyPaneTitleDoesNotOverrideStop proves the
// positive title signal is recovery for an empty state only, not a general
// authority over explicit terminal outcomes.
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
