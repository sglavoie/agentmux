package daemon

// The stabilization gate in front of a synthetic settle. A pane title carrying
// a spinner marker cannot veto the screen read — an idle pane holding a stale
// description would then never settle — so the screen stays authoritative and
// the title only lengthens the run of agreeing idle samples the probe needs.
// The run is what makes that safe: any applied reducer event restarts it, and a
// blocking screen never contributes a sample at all, so a settle really does
// mean an uninterrupted stretch of idle screens with zero hook traffic.

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// claudeIdleProbeLongestRun is the most samples any Claude tier can require:
// the quiescent tier plus the pane-title marker bump. Negative tests tick past
// it so a held ⚡ proves the guard under test held it, not a stabilization
// streak that simply had not finished accumulating.
const claudeIdleProbeLongestRun = claudeIdleProbeQuiescentSamples + claudeIdleProbeMarkerSampleBump

// TestClaudeIdleProbeSampleTiers pins the tier table itself. The counts are
// written as literals rather than derived from the constants, so re-tuning a
// constant fails here and has to be a deliberate edit to a stated number.
func TestClaudeIdleProbeSampleTiers(t *testing.T) {
	cases := []struct {
		name      string
		tier      screenstate.Tier
		hasMarker bool
		want      int
	}{
		{"explicit terminal", screenstate.TierTerminalExplicit, false, 2},
		{"terminal shape", screenstate.TierTerminalShape, false, 2},
		{"quiescent", screenstate.TierQuiescent, false, 3},
		{"explicit terminal with marker title", screenstate.TierTerminalExplicit, true, 3},
		{"terminal shape with marker title", screenstate.TierTerminalShape, true, 3},
		{"quiescent with marker title", screenstate.TierQuiescent, true, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeIdleProbeSamples(tc.tier, tc.hasMarker)
			if got != tc.want {
				t.Fatalf("claudeIdleProbeSamples(%v, %v) = %d, want %d",
					tc.tier, tc.hasMarker, got, tc.want)
			}
			// The Layer B invariant: at one sample the screen fingerprint in
			// AuthoritativeIdle.Generation is never compared against a second
			// observation, so a tier below the floor would opt out of
			// stabilization entirely — the exact hole this plan closes.
			if got < 2 {
				t.Fatalf("claudeIdleProbeSamples(%v, %v) = %d, want the floor of 2 or more",
					tc.tier, tc.hasMarker, got)
			}
		})
	}
}

// assertClaudeScreenTier pins which tier a probe fixture actually classifies as,
// so a sample-count expectation below cannot silently start testing a different
// row of the tier table when a fixture or a classifier changes.
func assertClaudeScreenTier(t *testing.T, screen string, want screenstate.Tier) {
	t.Helper()
	observation := classifyClaudeScreen(screen)
	if !observation.SettleableTerminal() {
		t.Fatalf("fixture is not settleable terminal: state=%v vetoes=%b", observation.State, observation.Vetoes)
	}
	if observation.Tier != want {
		t.Fatalf("fixture tier = %v, want %v", observation.Tier, want)
	}
}

// TestProbeClaudeScreenStop_QuiescentScreenWithMarkerRunsLongest pins the top of
// the tier table. A bare composer with no completion line at all is the weakest
// terminal evidence and the shape that produced the reported false stop, so it
// pays the extra sample the flat marker override used to charge every screen —
// and it must still settle, since Claude shows exactly this after /clear.
func TestProbeClaudeScreenStop_QuiescentScreenWithMarkerRunsLongest(t *testing.T) {
	assertClaudeScreenTier(t, claudeQuiescentIdleScreen, screenstate.TierQuiescent)
	// Three for the tier plus one for the marker title, written out so the
	// end-to-end run pins the number rather than tracking whatever the constants
	// happen to say.
	const wantSamples = 4
	if wantSamples != claudeIdleProbeLongestRun {
		t.Fatalf("longest Claude run = %d, want %d", claudeIdleProbeLongestRun, wantSamples)
	}
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = "⠐ Fix cursor corruption"
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeQuiescentIdleScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-quiescent", base.Unix())

	for sample := 1; sample < wantSamples; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective after sample %d = %+v, want bolt still held", sample, eff)
		}
		assertNoProbeStopFact(t, h, 42)
	}

	h.ProbeClaudeScreenStop(base.Add(time.Duration(wantSamples) * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after sample %d = %+v, want stop", wantSamples, eff)
	}
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 1 {
		t.Fatalf("ScreenStopProbe rows after sample %d = %d, want 1", wantSamples, got)
	}
}

// TestProbeClaudeScreenStop_InterruptedScreenSettlesOnTheFloor is the other end
// of the table: an explicit interruption marker is the strongest evidence
// Claude emits, so with no marker title it settles at the two-sample floor —
// never at one, where the fingerprint generation would go uncompared.
func TestProbeClaudeScreenStop_InterruptedScreenSettlesOnTheFloor(t *testing.T) {
	assertClaudeScreenTier(t, claudeInterruptedScreen, screenstate.TierTerminalExplicit)
	const wantSamples = claudeIdleProbeRequiredSamples
	base := time.Now()
	windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
		Profile: "work",
		Harness: harness.Claude,
	})
	windows[0].Tabs[0].Windows[0].Title = claudeRestingTitleText
	fake := &fakeKittyClient{windows: windows, getText: map[string]string{
		kitty.MatchWindowByID(42): claudeInterruptedScreen,
	}}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-interrupted", base.Unix())

	h.ProbeClaudeScreenStop(base.Add(time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after sample 1 = %+v, want bolt still held", eff)
	}
	assertNoProbeStopFact(t, h, 42)

	h.ProbeClaudeScreenStop(base.Add(time.Duration(wantSamples) * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after sample %d = %+v, want stop", wantSamples, eff)
	}
}

// TestProbeClaudeScreenStop_RestingBrailleRequiresMarkerBumpedRun pins the
// phantom's exit: a stale marker over Claude's resting body must enter the idle
// path, but it settles one sample later than the same screen under a bare title.
func TestProbeClaudeScreenStop_RestingBrailleRequiresMarkerBumpedRun(t *testing.T) {
	// claudeIdleScreen carries "✻ Cooked for 17m 6s", so the screen is
	// TierTerminalShape: two samples for the tier, plus one for the marker title.
	assertClaudeScreenTier(t, claudeIdleScreen, screenstate.TierTerminalShape)
	const wantSamples = claudeIdleProbeRequiredSamples + claudeIdleProbeMarkerSampleBump
	if wantSamples != 3 {
		t.Fatalf("marker-bumped terminal-shape run = %d, want 3", wantSamples)
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

	for sample := 1; sample < wantSamples; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective after sample %d = %+v, want bolt still held", sample, eff)
		}
		assertNoProbeStopFact(t, h, 42)
	}

	h.ProbeClaudeScreenStop(base.Add(time.Duration(wantSamples) * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after sample %d = %+v, want stop", wantSamples, eff)
	}
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 1 {
		t.Fatalf("ScreenStopProbe rows after sample %d = %d, want 1", wantSamples, got)
	}
}

// TestProbeClaudeScreenStop_BusyBrailleDescriptionSettlesAfterMarkerRun pins the
// reported defect. Claude keeps a task description in its pane title long after
// the turn that produced it ended, so a description title cannot veto the screen
// read: an idle pane holding one would never settle. The screen is authoritative
// and the title only lengthens stabilization by the marker bump.
func TestProbeClaudeScreenStop_BusyBrailleDescriptionSettlesAfterMarkerRun(t *testing.T) {
	assertClaudeScreenTier(t, claudeIdleScreen, screenstate.TierTerminalShape)
	const wantSamples = claudeIdleProbeRequiredSamples + claudeIdleProbeMarkerSampleBump
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

	for sample := 1; sample < wantSamples; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective after sample %d = %+v, want bolt still held", sample, eff)
		}
		assertNoProbeStopFact(t, h, 42)
	}

	// The settle lands on the bumped run and survives every later tick: the tab is
	// no longer ⚡, so the eligibility gate stops re-reading, and the description
	// title cannot re-promote a pane that is not empty.
	for sample := wantSamples; sample <= wantSamples*2; sample++ {
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

	for sample := 1; sample <= claudeIdleProbeLongestRun*3; sample++ {
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
	const wantSamples = claudeIdleProbeRequiredSamples + claudeIdleProbeMarkerSampleBump
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

	for sample := 1; sample < wantSamples; sample++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(sample) * time.Second))
	}
	claudeSeedActiveBolt(t, h, 42, "sess-hooked", base.Add(time.Duration(wantSamples)*time.Second).Unix())

	h.ProbeClaudeScreenStop(base.Add(time.Duration(wantSamples+1) * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after a hook reset the streak = %+v, want bolt", eff)
	}
	assertNoProbeStopFact(t, h, 42)
}
