package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestProbeCodexScreenPrompts_CompletedTurnStopsStuckBolt drives the completed-
// turn recovery through the authoritative-idle path. It seeds a real
// session-scoped UserPromptSubmit fact (not just a legacy guard) so the stuck ⚡
// is backed by a live interactive reducer fact — the reducer precedence the old
// legacy-handler stop could not defeat. The synthetic stop must retire that
// stale bolt, converge the tab to 🛑, and record a ScreenStopProbe row carrying
// the ACTUAL reduced state.
func TestProbeCodexScreenPrompts_CompletedTurnStopsStuckBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	boltTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟AiBroker",
		State:   "bolt",
		Looping: true,
	})
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, boltTitle, kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexCompletedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	probeCodexUntilSettled(t, h, base)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(42) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(42))
	}
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟AiBroker",
		State:   "stop",
		Looping: true,
	})
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}

	if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("effective = %+v visible %q, want stop/stop", eff, visible)
	}

	// The stale interactive bolt fact was retired, so it can no longer mask 🛑.
	h.stateMu.Lock()
	old := h.store[42].Reducer.facts[SourceID{Kind: SourceHarness, Key: "codex:sess-1"}]
	h.stateMu.Unlock()
	if old == nil || !old.acked {
		t.Fatalf("stale interactive bolt was not retired: %+v", old)
	}

	// The ScreenStopProbe row is attributed to the codex_screen probe source and
	// reports the ACTUAL reduced result (stop), not the requested action.
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent)
	if !ok {
		t.Fatalf("no %q row in %+v", screenStopProbeEvent, resp.Events)
	}
	if ev.Action != "stop" {
		t.Fatalf("ScreenStopProbe action = %q, want stop", ev.Action)
	}
	if ev.Source != string(SourceProbe)+":"+screenPromptProbeSource {
		t.Fatalf("ScreenStopProbe source = %q, want %q", ev.Source, string(SourceProbe)+":"+screenPromptProbeSource)
	}
}

// TestProbeCodexScreenPrompts_ConfirmsHeldStopInsteadOfSettling is the Codex
// twin of the Claude confirmation test, and pins the sample contract that now
// governs both harnesses: a completed-turn read is not by itself the whole
// sample requirement. Codex owning its own screen makes the CLASSIFICATION
// unambiguous, but one frame cannot answer whether the pane is still
// repainting, so the withheld stop is confirmed only once a full stabilization
// run of agreeing screens has landed — never on the first probe pass.
func TestProbeCodexScreenPrompts_ConfirmsHeldStopInsteadOfSettling(t *testing.T) {
	h, _ := newTerminalHoldProbeHandler(t, harness.Codex, codexCompletedTurnScreen)
	releaseHoldAtTestEnd(t, h, 42)
	base := time.Now()
	armTerminalHold(t, h)

	// Confirmation sits behind the same sample gate a synthetic settle does, so
	// every pass but the last leaves the hold unconfirmed.
	for sample := 1; sample < codexIdleProbeTerminalRun; sample++ {
		h.ProbeCodexScreenPrompts(base.Add(time.Duration(sample-1) * time.Second))
		if pending, ok := terminalHoldPending(t, h, 42); !ok || pending.confirmed {
			t.Fatalf("hold after completed-turn screen %d = confirmed %v/%v, want still unconfirmed",
				sample, pending.confirmed, ok)
		}
	}

	h.ProbeCodexScreenPrompts(base.Add(time.Duration(codexIdleProbeTerminalRun-1) * time.Second))

	pending, ok := terminalHoldPending(t, h, 42)
	if !ok || !pending.confirmed || pending.confirmedBy != StopConfirmedByCodexScreen {
		t.Fatalf("hold after a full run of %d completed-turn screens = %+v/%v, want confirmed by %s",
			codexIdleProbeTerminalRun, pending, ok, StopConfirmedByCodexScreen)
	}
	assertNoProbeStopFact(t, h, 42)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("icon while the confirmed hold waits = %q, want bolt", eff.Icon)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent); ok {
		t.Fatalf("probe recorded %+v; a confirmation is not a synthetic stop", ev)
	}
}

// TestProbeCodexScreenPrompts_InterruptedTurnStopsStuckBolt is the interruption
// twin of the completed-turn recovery: a user-interrupted Codex turn that left
// no Stop hook settles the same way through the authoritative-idle path.
func TestProbeCodexScreenPrompts_InterruptedTurnStopsStuckBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexInterruptedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	probeCodexUntilSettled(t, h, base)

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

func TestProbeCodexScreenPrompts_ActiveWorkingTurnKeepsBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexWorkingTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// A whole run rather than a single pass: a negative test has to tick past the
	// longest stabilization run, or a held ⚡ proves only that the run had not
	// finished accumulating yet.
	probeCodexUntilSettled(t, h, base)

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

func TestProbeCodexScreenPrompts_CompletedTurnForcesCodexForForeground(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("W:api", "bolt", false),
			[]string{"/opt/homebrew/bin/codex", "--yolo"},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexCompletedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	// The tab is detected as Codex only by its foreground process, but the stuck
	// ⚡ is still backed by a real interactive fact that the authoritative-idle
	// path settles.
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	probeCodexUntilSettled(t, h, base)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "stop", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop", eff)
	}
}

func TestProbeCodexScreenPrompts_ManagedOwnerSuppressesUnmanagedCodexSiblingStopProbe(t *testing.T) {
	h := newTestHandler()
	boltTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟AiBroker",
		State:   "bolt",
		Looping: true,
	})
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			boltTitle,
			kitty.Window{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
			},
			kitty.Window{
				ID:       43,
				IsActive: true,
				ForegroundProcesses: []kitty.ForegroundProcess{
					{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
				},
			},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(43): codexCompletedTurnScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(43, "active", false)

	probeCodexUntilSettled(t, h, time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(resp.Events) != 0 {
		t.Fatalf("sibling events = %d, want 0", len(resp.Events))
	}
	if got := h.guard.Current(43); got != "active" {
		t.Fatalf("sibling guard.Current = %q, want active", got)
	}
}

func TestProbeCodexScreenPrompts_ManagedNonCodexOwnerSuppressesUnmanagedCodexSibling(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("W:api", "bolt", false),
			kitty.Window{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
			},
			kitty.Window{
				ID:       43,
				IsActive: true,
				ForegroundProcesses: []kitty.ForegroundProcess{
					{Cmdline: []string{"/opt/homebrew/bin/codex", "--yolo"}},
				},
			},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(43): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)
	h.guard.TryUpdate(43, "active", false)

	probeCodexUntilSettled(t, h, time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43})
	if len(resp.Events) != 0 {
		t.Fatalf("sibling events = %d, want 0", len(resp.Events))
	}
}
