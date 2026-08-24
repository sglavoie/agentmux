package daemon

// The fingerprint gate, exercised on its own. Every other Claude probe test
// serves one fixed screen per window, so consecutive samples always agree and
// the generation carried on the observation is never actually compared — no
// test here exercised the gate at all until this one. The invariant it pins is
// harness-independent and survives any change to Claude's UI copy: a pane whose
// screen text changes between consecutive ticks can never settle a synthetic
// stop, however confidently the classifiers read it as idle.

import (
	"fmt"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// claudeTickingWorkScreenFormat is the reported bug in fixture form: a Claude
// 2.1.x pane that is genuinely working, in the one live shape Layer A still
// cannot read. Below roughly 20 columns Claude truncates its status line to a
// bare gerund with no parenthesized timer (session 2 measured the thresholds
// against 2.1.231), so claudeWorkingStatusLineRE matches nothing, the composer
// is empty, and the screenstate adapter observes a settleable Terminal. The
// only thing
// separating it from a finished pane is that its token count advances every
// tick.
//
// It was previously a wide `✳ Pouncing… (3m 40s · …)` frame, which the
// working-line veto now correctly rejects; the shape was replaced rather than
// the premise assertion below deleted, exactly as that assertion asks.
const claudeTickingWorkScreenFormat = `✢ Zigzagging… 
     %d tokens
──────────────────
❯  
──────────────────
  27.3 max 5h 22…
`

// TestProbeClaudeScreenStop_ChangingScreenNeverSettles is the Layer B
// regression. Six consecutive probes each read a DIFFERENT screen, and every one
// of those screens classifies as idle, so the classifiers alone would settle a
// synthetic stop on the second tick. The screen fingerprint restarts the
// stabilization streak each time instead, and the working pane keeps its ⚡ for
// as long as it keeps repainting.
func TestProbeClaudeScreenStop_ChangingScreenNeverSettles(t *testing.T) {
	base := time.Now()
	claudeAdapter, ok := screenstate.For(harness.Claude)
	if !ok {
		t.Fatal("screenstate has no Claude adapter")
	}
	const ticks = 6
	screens := make([]string, ticks)
	for i := range screens {
		screens[i] = fmt.Sprintf(claudeTickingWorkScreenFormat, 47565+i)
		// Guard the premise. If a later session teaches the classifier to read a
		// narrow truncated status line, this fixture stops observing as terminal
		// and the test silently stops proving anything — replace the frames with a
		// shape the classifier still accepts rather than deleting this assertion.
		//
		// It calls the adapter rather than the ClassifyClaudeIdleScreen shim on
		// purpose: the shims exist only for production callers and are deleted
		// later, and this assertion must survive that.
		if !claudeAdapter.Classify(screens[i]).SettleableTerminal() {
			t.Fatalf("frame %d no longer observes as a settleable terminal; the gate is no longer under test:\n%s", i, screens[i])
		}
		if i > 0 && screenstate.Fingerprint(screens[i]) == screenstate.Fingerprint(screens[i-1]) {
			t.Fatalf("frames %d and %d share a fingerprint; the frames must differ", i-1, i)
		}
	}

	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harness.Claude,
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): screens[0]},
	}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-ticking", base.Unix())

	// GetText copies beforeGetText under the fake's mutex and calls it unlocked
	// (handler_kitty_fake_test.go), so the hook may re-take that mutex and swap in
	// the next frame before the read it precedes returns.
	reads := 0
	fake.beforeGetText = func(match, extent string) {
		if match != kitty.MatchWindowByID(42) || extent != "screen" {
			return
		}
		if reads >= len(screens) {
			t.Errorf("probe read the screen %d times, want at most %d", reads+1, len(screens))
			return
		}
		fake.mu.Lock()
		fake.getText[match] = screens[reads]
		fake.mu.Unlock()
		reads++
	}

	for tick := 0; tick < ticks; tick++ {
		h.ProbeClaudeScreenStop(base.Add(time.Duration(2*tick) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective after tick %d = %+v, want the working bolt held", tick, eff)
		}
	}

	if reads != ticks {
		t.Fatalf("screen reads = %d, want %d (one distinct frame per tick)", reads, ticks)
	}
	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls across %d changing screens = %d, want 0", ticks, got)
	}
	assertNoProbeStopFact(t, h, 42)
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 0 {
		t.Fatalf("ScreenStopProbe rows = %d, want 0 for a pane that never repeated a screen", got)
	}
}

// TestProbeClaudeScreenStop_SettlesOnceTheScreenStopsChanging is the other half
// of the invariant: the gate suppresses only screens that keep moving. Once the
// same frame repeats, the ordinary two-sample run completes and the stale bolt
// still settles — a false negative that never ended would be its own bug.
func TestProbeClaudeScreenStop_SettlesOnceTheScreenStopsChanging(t *testing.T) {
	base := time.Now()
	moving := fmt.Sprintf(claudeTickingWorkScreenFormat, 47565)
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harness.Claude,
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): moving},
	}
	h := newTestHandler()
	h.SetKittyClient(fake)
	claudeSeedActiveBolt(t, h, 42, "sess-settling", base.Unix())

	// Two ticks on a moving screen bank nothing: the second read's fingerprint
	// discards the first sample.
	reads := 0
	fake.beforeGetText = func(match, extent string) {
		if match != kitty.MatchWindowByID(42) || extent != "screen" {
			return
		}
		reads++
		fake.mu.Lock()
		fake.getText[match] = fmt.Sprintf(claudeTickingWorkScreenFormat, 47565+reads)
		fake.mu.Unlock()
	}
	h.ProbeClaudeScreenStop(base)
	h.ProbeClaudeScreenStop(base.Add(2 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective while the screen moves = %+v, want bolt", eff)
	}

	// The turn ends: the pane stops repainting and the next two agreeing reads
	// settle it.
	fake.beforeGetText = nil
	fake.mu.Lock()
	fake.getText[kitty.MatchWindowByID(42)] = claudeIdleScreen
	fake.mu.Unlock()

	h.ProbeClaudeScreenStop(base.Add(4 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective after the first still frame = %+v, want bolt", eff)
	}
	h.ProbeClaudeScreenStop(base.Add(6 * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after two agreeing still frames = %+v, want stop", eff)
	}
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 1 {
		t.Fatalf("ScreenStopProbe rows = %d, want 1", got)
	}
}
