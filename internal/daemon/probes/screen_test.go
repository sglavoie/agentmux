package probes

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

var (
	screenNow    = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	tmuxInstance = runtimesession.BackendInstance{
		Kind: runtimesession.BackendTmux, Address: "/tmp/agent-mux-501/tmux.sock", Generation: "4242-1791588322",
	}
	tmuxTarget = runtimesession.Target{Instance: tmuxInstance, Ref: "%3"}
)

func freshTmuxScreen(text string) backend.Screen {
	return backend.Screen{
		Instance:        tmuxInstance,
		Pane:            backend.PaneID("%3"),
		Text:            text,
		Cols:            120,
		Rows:            36,
		AlternateScreen: true,
		CapturedAt:      screenNow.Add(-time.Second),
	}
}

func TestCheckScreen(t *testing.T) {
	want := ExpectScreenOf(tmuxTarget)
	if want.Pane != backend.PaneID("%3") || want.Instance != tmuxInstance {
		t.Fatalf("ExpectScreenOf = %+v", want)
	}
	cases := []struct {
		name    string
		mutate  func(*backend.Screen, *ScreenExpectation) error
		verdict ScreenVerdict
	}{
		{"fresh", func(*backend.Screen, *ScreenExpectation) error { return nil }, ScreenFresh},
		{"read error", func(*backend.Screen, *ScreenExpectation) error { return errors.New("no server") }, ScreenUnavailable},
		{"zero observation", func(s *backend.Screen, _ *ScreenExpectation) error {
			*s = backend.Screen{}
			return nil
		}, ScreenUnavailable},
		{"no capture instant", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.CapturedAt = time.Time{}
			return nil
		}, ScreenUnavailable},
		{"another pane", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.Pane = backend.PaneID("%4")
			return nil
		}, ScreenWrongPane},
		// A restarted tmux server hands out %3 again under a new generation.
		{"restarted server", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.Instance.Generation = "5151-1791588400"
			return nil
		}, ScreenWrongInstance},
		{"generation not reported", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.Instance.Generation = ""
			return nil
		}, ScreenWrongInstance},
		{"another server", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.Instance.Address = "/tmp/other/tmux.sock"
			return nil
		}, ScreenWrongInstance},
		{"another backend", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.Instance.Kind = runtimesession.BackendKitty
			return nil
		}, ScreenWrongInstance},
		{"exited program", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.Exited = true
			return nil
		}, ScreenExited},
		{"older than the default bound", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.CapturedAt = screenNow.Add(-ScreenMaxAge - time.Millisecond)
			return nil
		}, ScreenStale},
		{"at the default bound", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.CapturedAt = screenNow.Add(-ScreenMaxAge)
			return nil
		}, ScreenFresh},
		{"older than the caller's bound", func(_ *backend.Screen, w *ScreenExpectation) error {
			w.MaxAge = 500 * time.Millisecond
			return nil
		}, ScreenStale},
		{"captured before the caller's evidence", func(_ *backend.Screen, w *ScreenExpectation) error {
			w.NotBefore = screenNow.Add(-500 * time.Millisecond)
			return nil
		}, ScreenStale},
		{"captured after the caller's evidence", func(_ *backend.Screen, w *ScreenExpectation) error {
			w.NotBefore = screenNow.Add(-2 * time.Second)
			return nil
		}, ScreenFresh},
		// The most basic reason wins: a dead pane of a restarted server is the
		// wrong instance before it is an exited one.
		{"exited on a restarted server", func(s *backend.Screen, _ *ScreenExpectation) error {
			s.Exited = true
			s.Instance.Generation = "5151-1791588400"
			return nil
		}, ScreenWrongInstance},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			screen, expect := freshTmuxScreen("text"), want
			err := tc.mutate(&screen, &expect)
			if got := CheckScreen(screen, err, expect, screenNow); got != tc.verdict {
				t.Fatalf("CheckScreen = %s, want %s", got, tc.verdict)
			}
			text, textErr := FreshScreenText(screen, err, expect, screenNow)
			if tc.verdict == ScreenFresh {
				if textErr != nil || text != "text" {
					t.Fatalf("FreshScreenText = %q, %v", text, textErr)
				}
				return
			}
			var withheld *ScreenError
			if text != "" || !errors.As(textErr, &withheld) || withheld.Verdict != tc.verdict ||
				!errors.Is(textErr, ErrScreenWithheld) {
				t.Fatalf("FreshScreenText = %q, %v; want a withheld %s", text, textErr, tc.verdict)
			}
			if err != nil && !errors.Is(textErr, err) {
				t.Fatalf("the read error %v is not reachable from %v", err, textErr)
			}
		})
	}
}

// A Kitty binding names no address or generation: its epochs already tie it to
// one window tree, and Kitty's get-text reports neither.
func TestBindingScreenExpectation(t *testing.T) {
	binding := Binding{SourceWindowID: 42, TargetWindowID: 7}
	want := binding.ScreenExpectation()
	if binding.SourcePane() != backend.PaneID("42") || want.Pane != binding.SourcePane() ||
		want.Instance != (runtimesession.BackendInstance{Kind: runtimesession.BackendKitty}) {
		t.Fatalf("expectation = %+v", want)
	}
	screen := backend.Screen{
		Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendKitty},
		Pane:     backend.PaneID("42"), Text: "x", CapturedAt: screenNow,
	}
	if got := CheckScreen(screen, nil, want, screenNow); got != ScreenFresh {
		t.Fatalf("a Kitty read of the source pane is %s", got)
	}
	screen.Pane = backend.PaneID("7")
	if got := CheckScreen(screen, nil, want, screenNow); got != ScreenWrongPane {
		t.Fatalf("a read of the target pane is %s, want wrong_pane", got)
	}
}

// TestReadScreenNeverSettlesAWithheldCapture is the rule in one place: the
// same real finished-turn capture reads as finished only when the observation
// around it is fresh, for every way an observation can fail to be.
func TestReadScreenNeverSettlesAWithheldCapture(t *testing.T) {
	want := ExpectScreenOf(tmuxTarget)
	fixtures := map[string]string{
		"claude": "claude_idle_completed_t0.txt",
		"codex":  "codex_idle_completed_t0.txt",
	}
	withheld := map[string]func(*backend.Screen) error{
		"unavailable":     func(*backend.Screen) error { return errors.New("no server running") },
		"missing":         func(s *backend.Screen) error { *s = backend.Screen{}; return nil },
		"stale":           func(s *backend.Screen) error { s.CapturedAt = screenNow.Add(-time.Hour); return nil },
		"another pane":    func(s *backend.Screen) error { s.Pane = backend.PaneID("%9"); return nil },
		"restarted":       func(s *backend.Screen) error { s.Instance.Generation = "9-9"; return nil },
		"exited":          func(s *backend.Screen) error { s.Exited = true; return nil },
		"another backend": func(s *backend.Screen) error { s.Instance.Kind = runtimesession.BackendKitty; return nil },
	}
	for harnessName, fixture := range fixtures {
		data, err := os.ReadFile(filepath.Join("..", "..", "screenstate", "testdata", "tmux", fixture))
		if err != nil {
			t.Fatal(err)
		}
		adapter, ok := screenstate.For(harnessName)
		if !ok {
			t.Fatalf("no %s adapter", harnessName)
		}
		got, verdict := ReadScreen(adapter, freshTmuxScreen(string(data)), nil, want, screenNow)
		if verdict != ScreenFresh || !got.SettleableTerminal() {
			t.Fatalf("%s: fresh capture read %s/%s (%s); the gate is no longer under test",
				fixture, got.State, got.Vetoes, verdict)
		}
		for name, mutate := range withheld {
			screen := freshTmuxScreen(string(data))
			readErr := mutate(&screen)
			got, verdict := ReadScreen(adapter, screen, readErr, want, screenNow)
			if verdict == ScreenFresh || got != (screenstate.Observation{}) || got.SettleableTerminal() {
				t.Errorf("%s/%s: read %+v with verdict %s; want nothing", fixture, name, got, verdict)
			}
		}
	}
}

// A styled capture reads like a plain one once it is fresh.
func TestReadScreenStripsStyling(t *testing.T) {
	want := ExpectScreenOf(tmuxTarget)
	dir := filepath.Join("..", "..", "screenstate", "testdata", "tmux")
	plain, err := os.ReadFile(filepath.Join(dir, "codex_permission_prompt.txt"))
	if err != nil {
		t.Fatal(err)
	}
	styled, err := os.ReadFile(filepath.Join(dir, "codex_permission_prompt.ansi"))
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := screenstate.For("codex")
	a, _ := ReadScreen(adapter, freshTmuxScreen(string(plain)), nil, want, screenNow)
	b, _ := ReadScreen(adapter, freshTmuxScreen(string(styled)), nil, want, screenNow)
	if a.State != screenstate.Blocked || a != b {
		t.Fatalf("plain reads %+v, styled reads %+v", a, b)
	}
}
