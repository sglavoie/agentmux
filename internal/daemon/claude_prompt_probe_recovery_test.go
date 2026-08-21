package daemon

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

// TestProbeClaudeScreenStop_StaleBusyTitlePromotesOnce pins the sawtooth fix. A
// stale description title that outlives its turn promotes ⚡ once; when the
// watchdog expires that promoted fact the same unchanged title must not promote
// again, or the window cycles through ⚡ forever with a restarting busy anchor.
// A real hook event re-arms promotion even when the title text is identical.
func TestProbeClaudeScreenStop_StaleBusyTitlePromotesOnce(t *testing.T) {
	base := time.Now()
	const staleTitle = "⠐ Repair advisor calibration"
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = staleTitle
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
	h.SetKittyClient(fake)

	h.ProbeClaudeScreenStop(base)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after first busy-title probe = %+v, want bolt", eff)
	}
	if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 1 {
		t.Fatalf("ScreenActiveProbe rows after first promotion = %d, want 1", got)
	}

	expired := base.Add(11 * time.Minute)
	h.SweepStaleWithSnapshot(expired, nil)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
		t.Fatalf("effective after watchdog expiry = %+v, want empty", eff)
	}
	if anchor := windowBusySince(t, h, 42); !anchor.IsZero() {
		t.Fatalf("BusySince after watchdog expiry = %v, want zero", anchor)
	}

	h.ProbeClaudeScreenStop(expired.Add(2 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
		t.Fatalf("effective after re-probing the same stale title = %+v, want no second promotion", eff)
	}
	if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 1 {
		t.Fatalf("ScreenActiveProbe rows after re-probing the same title = %d, want 1", got)
	}
	if anchor := windowBusySince(t, h, 42); !anchor.IsZero() {
		t.Fatalf("BusySince after re-probing the same title = %v, want zero", anchor)
	}

	// A genuine turn re-arms promotion: the same title recovers once more after
	// the watchdog expires the hook-backed fact.
	resumed := expired.Add(time.Minute)
	claudeSeedActiveBolt(t, h, 42, "sess-resumed", resumed.Unix())
	h.SweepStaleWithSnapshot(resumed.Add(11*time.Minute), nil)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
		t.Fatalf("effective after expiring the hook fact = %+v, want empty", eff)
	}

	h.ProbeClaudeScreenStop(resumed.Add(12 * time.Minute))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after a re-armed busy-title probe = %+v, want bolt", eff)
	}
	if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 2 {
		t.Fatalf("ScreenActiveProbe rows after re-armed promotion = %d, want 2", got)
	}
}

// TestProbeClaudeScreenStop_RestingBrailleDoesNotPromoteEmptyWindow pins the
// phantom's origin: Claude's generic resting title can retain a Braille marker
// after the turn ends, but that stale frame must never recover an empty tab to ⚡.
func TestProbeClaudeScreenStop_RestingBrailleDoesNotPromoteEmptyWindow(t *testing.T) {
	base := time.Now()
	const restingTitle = "\u2802 " + claudeRestingTitleText
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = restingTitle
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	if resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: harness.Claude}); !resp.OK {
		t.Fatalf("seed empty state response = %+v", resp)
	}
	h.SetKittyClient(fake)

	for sample := 1; sample <= claudeIdleProbeMarkerSamples+2; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
		if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 0 {
			t.Fatalf("ScreenActiveProbe rows after sample %d = %d, want 0", sample, got)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
			t.Fatalf("effective after sample %d = %+v, want empty", sample, eff)
		}
		if anchor := windowBusySince(t, h, 42); !anchor.IsZero() {
			t.Fatalf("BusySince after sample %d = %v, want zero", sample, anchor)
		}
	}
}

// TestProbeClaudeScreenStop_RestingBrailleRequiresFourIdleSamples pins the
// phantom's exit: a stale marker over Claude's resting body must enter the idle
// path, but it uses the longer stabilization gate before settling a stuck ⚡.
func TestProbeClaudeScreenStop_RestingBrailleRequiresFourIdleSamples(t *testing.T) {
	if claudeIdleProbeMarkerSamples != 4 {
		t.Fatalf("marker sample requirement = %d, want 4", claudeIdleProbeMarkerSamples)
	}
	base := time.Now()
	const restingTitle = "\u2802 " + claudeRestingTitleText
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = restingTitle
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-resting", base.Unix())

	h.ProbeClaudeScreenStop(base.Add(time.Second))
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after sample 2 = %+v, want bolt", eff)
	}
	assertNoProbeStopFact(t, h, 42)

	h.ProbeClaudeScreenStop(base.Add(3 * time.Second))
	h.ProbeClaudeScreenStop(base.Add(4 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after sample 4 = %+v, want stop", eff)
	}
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 1 {
		t.Fatalf("ScreenStopProbe rows after sample 4 = %d, want 1", got)
	}
}

// TestProbeClaudeScreenStop_BusyBrailleDescriptionSettlesAfterMarkerRun pins the
// reported defect. Claude keeps a task description in its pane title long after
// the turn that produced it ended, so a description title cannot veto the screen
// read: an idle pane holding one would never settle. The screen is authoritative
// and the title only lengthens stabilization to the marker run.
func TestProbeClaudeScreenStop_BusyBrailleDescriptionSettlesAfterMarkerRun(t *testing.T) {
	base := time.Now()
	const staleTitle = "\u2810 Fix cursor corruption"
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = staleTitle
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-stale-description", base.Unix())

	for sample := 1; sample < claudeIdleProbeMarkerSamples; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective after sample %d = %+v, want bolt still held", sample, eff)
		}
		assertNoProbeStopFact(t, h, 42)
	}

	// The settle lands on the marker run and survives every later tick: the tab is
	// no longer \u26a1, so the eligibility gate stops re-reading, and the description
	// title cannot re-promote a pane that is not empty.
	for sample := claudeIdleProbeMarkerSamples; sample <= claudeIdleProbeMarkerSamples*2; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective after sample %d = %+v, want stop", sample, eff)
		}
	}
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 1 {
		t.Fatalf("ScreenStopProbe rows = %d, want 1", got)
	}
	if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 0 {
		t.Fatalf("ScreenActiveProbe rows = %d, want 0", got)
	}
}

// TestProbeClaudeScreenStop_StaleDescriptionSurvivesMarkerRotation pins the
// body-keyed one-shot. Claude freezes an arbitrary spinner frame in front of a
// stale description, so a raw-title key would re-arm promotion on every frame
// change and cycle the window through \u26a1 forever.
func TestProbeClaudeScreenStop_StaleDescriptionSurvivesMarkerRotation(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "\u2810 Fix cursor corruption"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
	h.SetKittyClient(fake)

	h.ProbeClaudeScreenStop(base)
	if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 1 {
		t.Fatalf("ScreenActiveProbe rows after first promotion = %d, want 1", got)
	}

	expired := base.Add(11 * time.Minute)
	h.SweepStaleWithSnapshot(expired, nil)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
		t.Fatalf("effective after watchdog expiry = %+v, want empty", eff)
	}

	// Same description, a different frozen frame: not new work.
	fake.mu.Lock()
	fake.windows[0].Tabs[0].Windows[0].Title = "\u2802 Fix cursor corruption"
	fake.mu.Unlock()
	h.ProbeClaudeScreenStop(expired.Add(2 * time.Second))

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
		t.Fatalf("effective after a rotated marker frame = %+v, want no second promotion", eff)
	}
	if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 1 {
		t.Fatalf("ScreenActiveProbe rows after a rotated marker frame = %d, want 1", got)
	}
}

// TestProbeClaudeScreenStop_BlockingPromptWithDescriptionTitleKeepsBolt proves
// the screen classifiers, not the title, are what defend an open permission
// prompt now that a description title no longer vetoes the read.
func TestProbeClaudeScreenStop_BlockingPromptWithDescriptionTitleKeepsBolt(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "\u2733 Fix cursor corruption"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudePermissionScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-blocked", base.Unix())

	for sample := 1; sample <= claudeIdleProbeMarkerSamples*3; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
	}

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective over an open permission prompt = %+v, want bolt", eff)
	}
	assertNoProbeStopFact(t, h, 42)
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 0 {
		t.Fatalf("ScreenStopProbe rows = %d, want 0", got)
	}
}

// TestProbeClaudeScreenStop_HookEventResetsDescriptionMarkerRun proves the
// decisive safety property behind dropping the title veto: any applied reducer
// event restarts the streak, so a settle really does mean an uninterrupted run
// of idle screens with zero hook traffic.
func TestProbeClaudeScreenStop_HookEventResetsDescriptionMarkerRun(t *testing.T) {
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "\u2733 Fix cursor corruption"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-hooked", base.Unix())

	for sample := 1; sample < claudeIdleProbeMarkerSamples; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
	}
	claudeSeedActiveBolt(t, h, 42, "sess-hooked", base.Add(3*time.Second).Unix())

	h.ProbeClaudeScreenStop(base.Add(time.Duration(claudeIdleProbeMarkerSamples) * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after a hook reset the streak = %+v, want bolt", eff)
	}
	assertNoProbeStopFact(t, h, 42)
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
