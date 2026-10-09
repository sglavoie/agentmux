package daemon

// The screen probes read backend-neutral observations, and an observation that
// is missing, stale, about another pane or server generation, or left behind
// by a dead program must never count as a finished turn. These tests feed the
// stop probes REAL finished-turn screens — tmux captures of Claude Code
// 2.1.296 and codex-cli 0.162.0 from internal/screenstate/testdata/tmux —
// through an injected observer, and vary only the observation around the text.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

func readTmuxScreenFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "screenstate", "testdata", "tmux", name))
	if err != nil {
		t.Fatalf("read tmux fixture %s: %v", name, err)
	}
	return string(data)
}

// observerFunc adapts a function to backend.Observer and counts its reads.
type observerFunc struct {
	reads   int
	observe func(backend.Ref) (backend.Screen, error)
}

func (o *observerFunc) Observe(pane backend.Ref) (backend.Screen, error) {
	o.reads++
	return o.observe(pane)
}

func TestScreenProbesNeverSettleOnWithheldObservations(t *testing.T) {
	const windowID = 42
	pane := kitty.PaneRef(windowID)
	kittyInstance := runtimesession.BackendInstance{Kind: runtimesession.BackendKitty}

	harnesses := []struct {
		name    string
		fixture string
		seed    func(*testing.T, *Handler, int, string, int64) uint64
		probe   func(*Handler, time.Time)
	}{
		{harness.Claude, "claude_idle_completed_t0.txt", claudeSeedActiveBolt, (*Handler).ProbeClaudeScreenStop},
		{harness.Codex, "codex_idle_completed_t0.txt", codexSeedActiveBolt, (*Handler).ProbeCodexScreenPrompts},
	}
	// Each case is the same finished screen wrapped in a different observation.
	cases := []struct {
		name    string
		settles bool
		observe func(text string) (backend.Screen, error)
	}{
		{"fresh", true, func(text string) (backend.Screen, error) {
			return backend.Screen{Instance: kittyInstance, Pane: pane, Text: text, CapturedAt: time.Now()}, nil
		}},
		{"read failed", false, func(string) (backend.Screen, error) {
			return backend.Screen{}, errors.New("capture unavailable")
		}},
		{"read failed but returned text", false, func(text string) (backend.Screen, error) {
			return backend.Screen{Instance: kittyInstance, Pane: pane, Text: text, CapturedAt: time.Now()},
				errors.New("capture unavailable")
		}},
		{"no capture instant", false, func(text string) (backend.Screen, error) {
			return backend.Screen{Instance: kittyInstance, Pane: pane, Text: text}, nil
		}},
		{"stale", false, func(text string) (backend.Screen, error) {
			return backend.Screen{
				Instance: kittyInstance, Pane: pane, Text: text,
				CapturedAt: time.Now().Add(-time.Minute),
			}, nil
		}},
		{"another pane", false, func(text string) (backend.Screen, error) {
			return backend.Screen{
				Instance: kittyInstance, Pane: kitty.PaneRef(windowID + 1), Text: text, CapturedAt: time.Now(),
			}, nil
		}},
		{"another backend", false, func(text string) (backend.Screen, error) {
			return backend.Screen{
				Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: "/tmp/s", Generation: "1-1"},
				Pane:     pane, Text: text, CapturedAt: time.Now(),
			}, nil
		}},
		{"exited program", false, func(text string) (backend.Screen, error) {
			return backend.Screen{
				Instance: kittyInstance, Pane: pane, Text: text, Exited: true, CapturedAt: time.Now(),
			}, nil
		}},
	}

	for _, hc := range harnesses {
		text := readTmuxScreenFixture(t, hc.fixture)
		adapter, _ := screenstate.For(hc.name)
		// Guard the premise: the text alone is a finished turn.
		if !adapter.Classify(text).SettleableTerminal() {
			t.Fatalf("%s no longer reads as a finished turn; the gate is no longer under test", hc.fixture)
		}
		for _, tc := range cases {
			t.Run(hc.name+"/"+tc.name, func(t *testing.T) {
				base := time.Now()
				// The Kitty client serves the same finished screen, so a probe that
				// bypassed the observer would settle too.
				fake := &fakeKittyClient{
					windows: codexProbeWindows(windowID, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
						Profile: "work",
						Harness: hc.name,
					}),
					getText: map[string]string{kitty.MatchWindowByID(windowID): text},
				}
				h := newTestHandler()
				h.SetKittyClient(fake)
				observer := &observerFunc{observe: func(backend.Ref) (backend.Screen, error) { return tc.observe(text) }}
				h.SetScreenObserver(observer)
				hc.seed(t, h, windowID, "sess-observed", base.Unix())

				const ticks = 8
				settledAt := -1
				for tick := 0; tick < ticks && settledAt < 0; tick++ {
					hc.probe(h, base.Add(time.Duration(tick)*time.Second))
					if eff, _ := windowEffective(t, h, windowID); eff.Icon == "stop" {
						settledAt = tick
					} else if eff.Icon != "bolt" {
						t.Fatalf("effective after tick %d = %+v, want bolt or stop", tick, eff)
					}
				}
				if observer.reads == 0 {
					t.Fatal("the probe never read through the observer")
				}
				if tc.settles {
					if settledAt < 1 {
						t.Fatalf("a fresh finished screen settled at tick %d; want a stabilized stop after the first", settledAt)
					}
					return
				}
				if settledAt >= 0 {
					t.Fatalf("a %s observation settled a stop at tick %d", tc.name, settledAt)
				}
				assertNoProbeStopFact(t, h, windowID)
				if got := countEventsNamed(t, h, windowID, screenStopProbeEvent); got != 0 {
					t.Fatalf("ScreenStopProbe rows = %d, want 0", got)
				}
			})
		}
	}
}
