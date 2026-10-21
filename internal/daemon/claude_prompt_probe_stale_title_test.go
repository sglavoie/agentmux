package daemon

// Promotion from a stale pane title. Claude leaves a task description — and
// sometimes a frozen spinner frame — in its pane title long after the turn that
// produced it ended, so the probe may recover ⚡ from such a title only once per
// genuine turn. The one-shot is keyed on the description body, not the raw
// title, because the marker frame keeps rotating on its own; a real hook event
// re-arms it. An operator clear holds promotion until such an event. Claude's generic resting body is not a description at all and
// never promotes an empty tab.

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestProbeClaudeScreenStop_StaleBusyTitlePromotesOnce pins the sawtooth fix. A
// stale description title that outlives its turn promotes ⚡ once; when the
// watchdog expires that promoted fact the same unchanged title must not promote
// again, or the window cycles through ⚡ forever with a restarting busy anchor.
// A real hook event re-arms promotion even when the title text is identical.
func TestProbeClaudeScreenStop_StaleBusyTitlePromotesOnce(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	base := time.Now()
	const restingTitle = "\u2802 " + activity.ClaudeRestingTitleText
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = restingTitle
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	// Seeded with "clear" rather than "user_clear": an operator clear holds
	// busy-title promotion outright, which would pass this test without ever
	// consulting the resting-body rule it pins.
	if resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "clear", Harness: harness.Claude}); !resp.OK {
		t.Fatalf("seed empty state response = %+v", resp)
	}
	h.SetKittyClient(fake)

	for sample := 1; sample <= claudeIdleProbeLongestRun+2; sample++ {
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

// TestProbeClaudeScreenStop_StaleDescriptionSurvivesMarkerRotation pins the
// body-keyed one-shot. Claude freezes an arbitrary spinner frame in front of a
// stale description, so a raw-title key would re-arm promotion on every frame
// change and cycle the window through ⚡ forever.
func TestProbeClaudeScreenStop_StaleDescriptionSurvivesMarkerRotation(t *testing.T) {
	t.Parallel()

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

// TestProbeClaudeScreenStop_UserClearHoldsStaleTitlePromotion pins the
// cleared-icon regression. The operator clears a settled 🛑 whose pane title shows
// the finished turn's description; that clear acknowledges the stale title, so
// the probe must neither re-promote ⚡ from it nor let the idle probe settle that
// ⚡ back into the 🛑 just cleared. The hold lasts until a real hook event. Both
// clear entry points are covered: the direct state update the dashboard sends
// and the F8 hook event that routes through handleUserClearHookEvent.
func TestProbeClaudeScreenStop_UserClearHoldsStaleTitlePromotion(t *testing.T) {
	t.Parallel()

	clears := map[string]protocol.Request{
		"state_update": {Type: protocol.TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: harness.Claude},
		"hook_event":   {Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"},
	}
	for name, clearReq := range clears {
		t.Run(name, func(t *testing.T) {
			base := time.Now()
			windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
				Profile: "work",
				Harness: harness.Claude,
			})
			windows[0].Tabs[0].Windows[0].Title = "\u2733 File refactoring for code size reduction"
			fake := &fakeKittyClient{windows: windows, getText: map[string]string{
				kitty.MatchWindowByID(42): claudeIdleScreen,
			}}
			h := newTestHandler()
			h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Minute}, nil)
			h.SetKittyClient(fake)

			claudeSeedActiveBolt(t, h, 42, "sess-done", base.Unix())
			stop := protocol.NormalizedEvent{
				Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:sess-done"},
				Action: "stop", SessionID: "sess-done", Event: "Stop", At: base.Add(time.Second).Unix(),
			}
			if resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop", Normalized: &stop}); !resp.OK {
				t.Fatalf("seed stop response = %+v", resp)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
				t.Fatalf("effective after seeding stop = %+v, want stop", eff)
			}

			if resp := h.Handle(clearReq); !resp.OK {
				t.Fatalf("user_clear response = %+v", resp)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
				t.Fatalf("effective after user_clear = %+v, want empty", eff)
			}

			cleared := base.Add(2 * time.Second)
			for sample := 1; sample <= claudeIdleProbeLongestRun+2; sample++ {
				h.ProbeClaudeScreenStop(cleared.Add(time.Duration(sample) * time.Second))
				if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
					t.Fatalf("effective after sample %d = %+v, want the clear to hold", sample, eff)
				}
			}
			if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 0 {
				t.Fatalf("ScreenActiveProbe rows after user_clear = %d, want 0", got)
			}
			if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 0 {
				t.Fatalf("ScreenStopProbe rows after user_clear = %d, want 0", got)
			}

			// A genuine turn releases the hold: once the watchdog expires its
			// hook-backed fact, the unchanged title may recover ⚡ again.
			resumed := cleared.Add(time.Minute)
			claudeSeedActiveBolt(t, h, 42, "sess-resumed", resumed.Unix())
			h.SweepStaleWithSnapshot(resumed.Add(11*time.Minute), nil)
			if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
				t.Fatalf("effective after expiring the hook fact = %+v, want empty", eff)
			}
			h.ProbeClaudeScreenStop(resumed.Add(12 * time.Minute))
			if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
				t.Fatalf("effective after the hold was released = %+v, want bolt", eff)
			}
			if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 1 {
				t.Fatalf("ScreenActiveProbe rows after the hold was released = %d, want 1", got)
			}
		})
	}
}

// TestProbeClaudeScreenStop_UserClearOfPromotedBoltStaysSpent pins the other
// half of the clear contract. Clearing ⚡ does not hold promotion (a live turn
// may recover from its title), but it must not re-arm it either: a ⚡ the probe
// promoted from a stale title and the operator then cleared stays cleared.
func TestProbeClaudeScreenStop_UserClearOfPromotedBoltStaysSpent(t *testing.T) {
	t.Parallel()

	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "✳ File refactoring for code size reduction"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeIdleScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)

	h.ProbeClaudeScreenStop(base)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after stale-title promotion = %+v, want bolt", eff)
	}
	if resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: harness.Claude}); !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}

	for sample := 1; sample <= claudeIdleProbeLongestRun+2; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "" {
			t.Fatalf("effective after sample %d = %+v, want the clear to hold", sample, eff)
		}
	}
	if got := countEventsNamed(t, h, 42, screenActiveProbeEvent); got != 1 {
		t.Fatalf("ScreenActiveProbe rows = %d, want only the original promotion", got)
	}
}
