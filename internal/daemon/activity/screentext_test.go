package activity

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

type countingObserver struct {
	reads map[backend.Ref]int
	fail  map[backend.Ref]error
}

func (o *countingObserver) Observe(pane backend.Ref) (backend.Screen, error) {
	o.reads[pane]++
	if err := o.fail[pane]; err != nil {
		return backend.Screen{}, err
	}
	return backend.Screen{
		Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: "/s", Generation: "1-1"},
		Pane:     pane,
		Text:     "screen of " + pane.ID,
	}, nil
}

// One read per pane per tick, on the failure path too, whatever the backend.
func TestScreenTextCacheReadsEachPaneOnce(t *testing.T) {
	wedged := errors.New("wedged")
	a, b := backend.PaneID("%1"), backend.PaneID("%2")
	observer := &countingObserver{reads: map[backend.Ref]int{}, fail: map[backend.Ref]error{b: wedged}}
	cache := NewScreenObservationCache(observer)
	for range 3 {
		screen, err := cache.Observe(a)
		if err != nil || screen.Text != "screen of %1" || screen.Instance.Generation != "1-1" {
			t.Fatalf("Observe(%s) = %+v, %v", a.ID, screen, err)
		}
		if _, err := cache.Observe(b); !errors.Is(err, wedged) {
			t.Fatalf("Observe(%s) err = %v, want the memoized failure", b.ID, err)
		}
	}
	if observer.reads[a] != 1 || observer.reads[b] != 1 {
		t.Fatalf("reads = %v, want one per pane", observer.reads)
	}
	// A new tick is a new cache and a new read.
	if _, err := NewScreenObservationCache(observer).Observe(a); err != nil || observer.reads[a] != 2 {
		t.Fatalf("a second tick read %d times (%v)", observer.reads[a], err)
	}
}

type recordingTextReader struct {
	calls [][2]string
	err   error
}

func (r *recordingTextReader) GetText(match, extent string) (string, error) {
	r.calls = append(r.calls, [2]string{match, extent})
	return "kitty screen", r.err
}

// The Kitty-backed cache issues the same get-text it always did and reports it
// as a Kitty observation of that window.
func TestScreenTextCacheOverKitty(t *testing.T) {
	reader := &recordingTextReader{}
	cache := NewScreenTextCache(reader)
	screen, err := cache.Observe(kitty.PaneRef(42))
	if err != nil || screen.Text != "kitty screen" || screen.Pane != kitty.PaneRef(42) ||
		screen.Instance.Kind != runtimesession.BackendKitty || screen.CapturedAt.IsZero() {
		t.Fatalf("Observe = %+v, %v", screen, err)
	}
	if _, err := cache.Observe(kitty.PaneRef(42)); err != nil {
		t.Fatal(err)
	}
	if len(reader.calls) != 1 || reader.calls[0] != [2]string{kitty.MatchWindowByID(42), "screen"} {
		t.Fatalf("get-text calls = %v", reader.calls)
	}

	// A reference that is not a Kitty window ID never becomes a match expression.
	for _, ref := range []backend.Ref{
		backend.PaneID("%3"), backend.PaneID("0"), backend.PaneID("042"), backend.PaneID("1 or title:x"),
		kitty.GroupRef(42),
	} {
		if _, err := cache.Observe(ref); !errors.Is(err, backend.ErrInvalidRef) {
			t.Errorf("Observe(%v) err = %v, want ErrInvalidRef", ref, err)
		}
	}
	if len(reader.calls) != 1 {
		t.Fatalf("an invalid reference reached Kitty: %v", reader.calls)
	}
}

func TestPaneForegroundHarness(t *testing.T) {
	cases := []struct {
		name     string
		procs    []backend.Process
		harness  string
		pipeMode bool
	}{
		{"no processes", nil, "", false},
		{"pid only", []backend.Process{{PID: 7}}, "", false},
		{"shell", []backend.Process{{PID: 7, Argv: []string{"/bin/zsh", "-l"}}}, "", false},
		{"claude", []backend.Process{{PID: 7, Argv: []string{"/usr/local/bin/claude"}}}, "claude", false},
		{"claude pipe mode", []backend.Process{{PID: 7, Argv: []string{"claude", "-p", "hi"}}}, "claude", true},
		{"harness behind a tool", []backend.Process{
			{PID: 9, Argv: []string{"/bin/sleep", "5"}}, {PID: 7, Argv: []string{"codex"}},
		}, "codex", false},
	}
	for _, tc := range cases {
		got, pipeMode := PaneForegroundHarness(backend.Pane{Foreground: tc.procs})
		if got != tc.harness || pipeMode != tc.pipeMode {
			t.Errorf("%s: got %q pipe=%v, want %q pipe=%v", tc.name, got, pipeMode, tc.harness, tc.pipeMode)
		}
	}
}
