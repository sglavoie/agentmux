package daemon

// Codex's half of the stabilization gate. Codex used to settle a synthetic stop
// from a SINGLE completed-turn read, which meant the screen fingerprint the
// probe already carried was never compared against anything — Layer B was inert
// on the harness that needs it most, since Codex installs 10 hooks against
// Claude's 28 and Stop is its only terminal one. Raising the Codex rows to the
// two-sample floor is what turns the generation gate on; these tests pin both
// the count and the behaviour it buys.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// readCodexCapture returns one of internal/screenstate's real pane captures
// verbatim.
//
// The captures are READ ACROSS the package boundary rather than copied here on
// purpose. internal/screenstate/testdata is the repository's one corpus of
// byte-for-byte `kitten @ get-text` output, and every other Codex screen in this
// package is a hand-written const. A copy would drift the moment either side was
// re-captured, and a drifted copy is exactly the failure the corpus exists to
// prevent: the original bug survived a full suite because the committed "is
// working" screens were hand-modelled rather than captured.
func readCodexCapture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "screenstate", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read capture %s: %v", path, err)
	}
	return string(data)
}

// TestCodexIdleProbeSampleTiers pins the Codex rows of the sample table. Like
// its Claude twin the counts are literals rather than derived from the
// constants, so re-tuning one has to be a deliberate edit to a stated number
// — and dropping any Codex row back to a single sample fails here rather than
// silently switching Layer B off again.
func TestCodexIdleProbeSampleTiers(t *testing.T) {
	cases := []struct {
		name string
		tier screenstate.Tier
		want int
	}{
		{"explicit terminal", screenstate.TierTerminalExplicit, 2},
		{"terminal shape", screenstate.TierTerminalShape, 2},
		{"quiescent", screenstate.TierQuiescent, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No Codex evidence buys a title bump, so the row is the whole count.
			got := requiredIdleSamples(harness.Codex, tc.tier, 0)
			if got != tc.want {
				t.Fatalf("requiredIdleSamples(codex, %v) = %d, want %d", tc.tier, got, tc.want)
			}
			// The Layer B invariant: below two samples the fingerprint in
			// AuthoritativeIdle.Generation is never compared against a second
			// observation, so the row would opt out of stabilization entirely.
			if got < idleMinimumSamples {
				t.Fatalf("requiredIdleSamples(codex, %v) = %d, want at least the floor of %d",
					tc.tier, got, idleMinimumSamples)
			}
		})
	}
	// TierTerminalExplicit is the only tier the Codex adapter reaches today, so it
	// is the one the shared probe helper reads. Keep the two in agreement.
	if codexIdleProbeTerminalRun != 2 {
		t.Fatalf("codexIdleProbeTerminalRun = %d, want 2", codexIdleProbeTerminalRun)
	}
}

// codexTickingCompletedScreenFormat is a Codex frame that Layer A grades as a
// finished turn — the latest status line it recognizes is "Worked for 1m 30s"
// and an empty composer follows it — while the pane is still repainting. Only
// the footer moves, which is the shape of the case Layer A cannot defend
// against: a live status line that is off the visible screen (session 2 recorded
// Claude doing exactly this while streaming long tool output) leaves the
// previous turn's marker as the newest thing the classifier can see.
//
// It is synthetic on purpose. The point of Layer B is that it holds whatever the
// classifier concluded, so the fixture only has to satisfy the premise the
// assertions below check explicitly: settleable terminal, and a different
// fingerprint every tick.
const codexTickingCompletedScreenFormat = `• Ran tests
  go test ./internal/daemon

• Worked for 1m 30s

› Use /skills to list available skills

gpt-5.5 xhigh · ~/1_dev_projects/sglavoie_agentmux · Context %d%% used
`

// codexTickingCompletedScreens builds n distinct frames and guards the premise:
// each one must still observe as a settleable terminal (or the gate is no longer
// what is under test) and no two may share a fingerprint (or the gate is being
// handed the agreement it is supposed to demand).
func codexTickingCompletedScreens(t *testing.T, n int) []string {
	t.Helper()
	adapter, ok := screenstate.For(harness.Codex)
	if !ok {
		t.Fatal("screenstate has no Codex adapter")
	}
	screens := make([]string, n)
	for i := range screens {
		screens[i] = fmt.Sprintf(codexTickingCompletedScreenFormat, 11+i)
		if !adapter.Classify(screens[i]).SettleableTerminal() {
			t.Fatalf("frame %d no longer observes as a settleable terminal; the gate is no longer under test:\n%s",
				i, screens[i])
		}
		if i > 0 && screenstate.Fingerprint(screens[i]) == screenstate.Fingerprint(screens[i-1]) {
			t.Fatalf("frames %d and %d share a fingerprint; the frames must differ", i-1, i)
		}
	}
	return screens
}

// TestProbeCodexScreenPrompts_ChangingScreenNeverSettles is the Codex Layer B
// regression, and the whole reason the Codex rows left one sample. Six
// consecutive probes each read a DIFFERENT screen and every one of them
// classifies as a finished turn, so at the old single-sample count the very
// first tick would have painted 🛑 on a pane that is still repainting. The
// screen fingerprint restarts the stabilization streak instead.
func TestProbeCodexScreenPrompts_ChangingScreenNeverSettles(t *testing.T) {
	base := time.Now()
	const ticks = 6
	screens := codexTickingCompletedScreens(t, ticks)

	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harness.Codex,
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): screens[0]},
	}
	h := newTestHandler()
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-ticking", base.Unix())

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
		h.ProbeCodexScreenPrompts(base.Add(time.Duration(2*tick) * time.Second))
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

// TestProbeCodexScreenPrompts_SettlesOnceTheScreenStopsChanging is the other half
// of the invariant, and the reason the flip costs latency rather than recovery:
// the gate suppresses only screens that keep moving. Once the same frame
// repeats, the run completes and the stale ⚡ still settles — one 2s tick later
// than before, and never not at all.
func TestProbeCodexScreenPrompts_SettlesOnceTheScreenStopsChanging(t *testing.T) {
	base := time.Now()
	moving := codexTickingCompletedScreens(t, 1)[0]
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harness.Codex,
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): moving},
	}
	h := newTestHandler()
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-settling", base.Unix())

	// A whole run on a moving screen banks nothing: each read's fingerprint
	// discards the sample before it.
	reads := 0
	fake.beforeGetText = func(match, extent string) {
		if match != kitty.MatchWindowByID(42) || extent != "screen" {
			return
		}
		reads++
		fake.mu.Lock()
		fake.getText[match] = fmt.Sprintf(codexTickingCompletedScreenFormat, 11+reads)
		fake.mu.Unlock()
	}
	probeCodexUntilSettled(t, h, base)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective while the screen moves = %+v, want bolt", eff)
	}

	// The pane goes quiet: the same completed-turn frame repeats and the run
	// settles it.
	fake.beforeGetText = nil
	fake.mu.Lock()
	fake.getText[kitty.MatchWindowByID(42)] = codexCompletedTurnScreen
	fake.mu.Unlock()

	still := base.Add(time.Duration(codexIdleProbeTerminalRun) * time.Second)
	for sample := 1; sample < codexIdleProbeTerminalRun; sample++ {
		h.ProbeCodexScreenPrompts(still.Add(time.Duration(sample-1) * time.Second))
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective after still frame %d = %+v, want bolt", sample, eff)
		}
	}
	h.ProbeCodexScreenPrompts(still.Add(time.Duration(codexIdleProbeTerminalRun-1) * time.Second))
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective after %d agreeing still frames = %+v, want stop", codexIdleProbeTerminalRun, eff)
	}
	if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 1 {
		t.Fatalf("ScreenStopProbe rows = %d, want 1", got)
	}
}

// TestProbeCodexCurrentCompletedScreenSettles drives the stop fallback end to
// end with the screens the INSTALLED Codex actually renders.
//
// Every other Codex probe test in this package serves a hand-written const, so
// the whole probe chain could stay green while being unreachable on a real
// pane — which is precisely what happened: codex-cli 0.149.1's composer
// placeholder was unrecognized, so its captures carried VetoComposerBusy and no
// current screen could settle at any sample count. These subtests are the ones
// that would have caught it, because their bytes come from the pane rather than
// from an author's memory of it.
//
// The stabilization contract is asserted alongside recovery: the bolt must
// still be held after the first agreeing frame and settle only on the last, so
// the fixtures prove Layer B is live on Codex rather than bypassed.
func TestProbeCodexCurrentCompletedScreenSettles(t *testing.T) {
	captures := []struct {
		name    string
		fixture string
	}{
		{name: "framed completed marker", fixture: "codex_idle_completed_t0.txt"},
		{name: "interruption with no background terminal", fixture: "codex_interrupted_no_background.txt"},
	}
	for _, tc := range captures {
		t.Run(tc.name, func(t *testing.T) {
			base := time.Now()
			screen := readCodexCapture(t, tc.fixture)

			fake := &fakeKittyClient{
				windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
					Profile: "work",
					Harness: harness.Codex,
				}),
				getText: map[string]string{kitty.MatchWindowByID(42): screen},
			}
			h := newTestHandler()
			h.SetKittyClient(fake)
			codexSeedActiveBolt(t, h, 42, "sess-current", base.Unix())

			// Held on every pass but the last: the capture is settleable evidence,
			// and the sample gate is what spends the extra tick on agreement.
			for sample := 1; sample < codexIdleProbeTerminalRun; sample++ {
				h.ProbeCodexScreenPrompts(base.Add(time.Duration(sample-1) * time.Second))
				if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
					t.Fatalf("effective after agreeing frame %d = %+v, want the bolt still held", sample, eff)
				}
			}
			h.ProbeCodexScreenPrompts(base.Add(time.Duration(codexIdleProbeTerminalRun-1) * time.Second))

			eff, _ := windowEffective(t, h, 42)
			if eff.Icon != "stop" {
				t.Fatalf("effective after %d agreeing current-version frames = %+v, want stop",
					codexIdleProbeTerminalRun, eff)
			}
			if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 1 {
				t.Fatalf("ScreenStopProbe rows = %d, want 1", got)
			}
		})
	}

	// The control. A real working capture from the same build must NOT settle,
	// however long the probe runs, so the subtests above are not passing because
	// the classifier turned permissive.
	t.Run("live working capture never settles", func(t *testing.T) {
		base := time.Now()
		fake := &fakeKittyClient{
			windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
				Profile: "work",
				Harness: harness.Codex,
			}),
			getText: map[string]string{
				kitty.MatchWindowByID(42): readCodexCapture(t, "codex_working.txt"),
			},
		}
		h := newTestHandler()
		h.SetKittyClient(fake)
		codexSeedActiveBolt(t, h, 42, "sess-working", base.Unix())

		for sample := 0; sample < codexIdleProbeTerminalRun+2; sample++ {
			h.ProbeCodexScreenPrompts(base.Add(time.Duration(sample) * time.Second))
			if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
				t.Fatalf("effective after working frame %d = %+v, want bolt", sample, eff)
			}
		}
		assertNoProbeStopFact(t, h, 42)
		if got := countEventsNamed(t, h, 42, screenStopProbeEvent); got != 0 {
			t.Fatalf("ScreenStopProbe rows = %d, want 0 on a working pane", got)
		}
	})
}

// TestProbeCodexCurrentClearedScreenRecovers is the clear half of the same
// contract. Codex emits no hook for an idle `/clear`, so the clear probe is the
// only thing that takes a stopped pane's icon back to idle — and it, too, was
// unreachable on the installed build for as long as the composer placeholder
// was unknown.
func TestProbeCodexCurrentClearedScreenRecovers(t *testing.T) {
	cleared := readCodexCapture(t, "codex_cleared.txt")
	if !screenstate.CodexScreenLooksCleared(cleared) {
		t.Fatal("CodexScreenLooksCleared() = false: the clear probe's cheap first pass rejects a real cleared pane")
	}
	if !screenstate.ClassifyCodexClearScreen(cleared, cleared) {
		t.Fatal("ClassifyCodexClearScreen() = false on a real cleared 0.149.1 pane")
	}
	for _, name := range []string{"codex_working.txt", "codex_idle_completed_t0.txt"} {
		screen := readCodexCapture(t, name)
		if screenstate.ClassifyCodexClearScreen(screen, screen) {
			t.Errorf("ClassifyCodexClearScreen() = true on %s, want false", name)
		}
	}
}
