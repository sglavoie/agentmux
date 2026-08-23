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
)

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
	// no longer ⚡, so the eligibility gate stops re-reading, and the description
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
