package session

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// Restart and profile replacement must start the new agent before they close
// the old one, on either backend: a launch that fails then leaves the agent
// the operator had. These tests watch the order the backend is asked in, not
// only what is left afterwards.

var errLaunchRefused = errors.New("launch refused by the test")

// orderedKitty is a Kitty that records the order of the commands that create
// and end a tab. It lists no tabs, so nothing is deduplicated.
type orderedKitty struct {
	empty      []byte
	failLaunch bool
	order      []string
	closed     []string
}

func (k *orderedKitty) Run(_ string, args ...string) ([]byte, []byte, error) {
	switch {
	case slices.Contains(args, "ls"):
		return k.empty, nil, nil
	case slices.Contains(args, "launch"):
		k.order = append(k.order, "launch")
		if k.failLaunch {
			return nil, []byte("refused"), errLaunchRefused
		}
	case slices.Contains(args, "close-tab"):
		k.order = append(k.order, "close")
		k.closed = append(k.closed, args[len(args)-1])
	}
	return nil, nil, nil
}

// orderedTmux is a real tmux backend that records the same order and counts
// the agents alive at the moment the old one is closed.
type orderedTmux struct {
	backend.TargetLauncher
	failLaunch   bool
	order        []string
	aliveAtClose int
	closed       []backend.Ref
}

func (b *orderedTmux) LaunchTarget(spec backend.LaunchSpec) (backend.Launched, error) {
	b.order = append(b.order, "launch")
	if b.failLaunch {
		return backend.Launched{}, errLaunchRefused
	}
	return b.TargetLauncher.LaunchTarget(spec)
}

func (b *orderedTmux) Launch(spec backend.LaunchSpec) error {
	_, err := b.LaunchTarget(spec)
	return err
}

func (b *orderedTmux) Close(ref backend.Ref) error {
	b.order = append(b.order, "close")
	b.closed = append(b.closed, ref)
	panes, err := b.Discover()
	if err != nil {
		return err
	}
	for _, pane := range panes {
		if pane.Exit == nil {
			b.aliveAtClose++
		}
	}
	return b.TargetLauncher.Close(ref)
}

type replaceFunc func(rt Runtime, running RunningSession, cfg *config.Config) (LaunchResult, error)

func replacements(project config.Session) map[string]replaceFunc {
	return map[string]replaceFunc{
		"restart": RestartOn,
		"profile replacement": func(rt Runtime, running RunningSession, cfg *config.Config) (LaunchResult, error) {
			return RestartWithAccountTemplateOn(rt, running, project, cfg.Accounts[1], cfg)
		},
	}
}

func TestRestartLaunchesBeforeClosingOnKitty(t *testing.T) {
	empty, err := os.ReadFile(filepath.Join("..", "..", "testdata", "kitty_ls_empty.json"))
	if err != nil {
		t.Fatal(err)
	}
	project := config.Session{Title: "api", Directory: t.TempDir(), Profile: "work"}
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha"},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		Accounts: []config.Account{
			{Name: "work", Harness: harness.Claude, Color: "blue"},
			{Name: "home", Harness: harness.Codex, Color: "yellow"},
		},
		Sessions: []config.Session{project},
	}
	running := RunningSession{TabID: 42, WindowID: 43, Title: "api", Base: "api", Profile: "work", Project: "api", Directory: project.Directory}

	for name, replace := range replacements(project) {
		t.Run(name, func(t *testing.T) {
			runner := &orderedKitty{empty: empty}
			if _, err := replace(KittyRuntime(kitty.NewClientWithSocket(runner, "")), running, cfg); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !slices.Equal(runner.order, []string{"launch", "close"}) {
				t.Fatalf("order = %v; want the launch and then the close", runner.order)
			}
			if !slices.Equal(runner.closed, []string{"--match=id:42"}) {
				t.Fatalf("closed %v; want only the replaced tab, id:42", runner.closed)
			}
		})
		t.Run(name+" when the launch fails", func(t *testing.T) {
			runner := &orderedKitty{empty: empty, failLaunch: true}
			if _, err := replace(KittyRuntime(kitty.NewClientWithSocket(runner, "")), running, cfg); err == nil {
				t.Fatalf("%s succeeded with a refused launch", name)
			}
			if !slices.Equal(runner.order, []string{"launch"}) {
				t.Fatalf("order = %v; want a failed launch and the old tab left alone", runner.order)
			}
		})
	}
}

func TestRestartLaunchesBeforeClosingOnTmux(t *testing.T) {
	for name := range replacements(config.Session{}) {
		t.Run(name, func(t *testing.T) {
			f := tmuxtest.New(t)
			cfg, project := tmuxTestConfig(t)
			original, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
			if err != nil {
				t.Fatal(err)
			}
			running := runningOnTmux(t, f, cfg)[0]
			recorder := &orderedTmux{TargetLauncher: f.Backend}
			rt := Runtime{Tmux: recorder, Registry: f.Registry}

			replaced, err := replacements(project)[name](rt, running, cfg)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !slices.Equal(recorder.order, []string{"launch", "close"}) {
				t.Fatalf("order = %v; want the launch and then the close", recorder.order)
			}
			// Both agents were alive when the old one was closed: the new
			// one was already running, not merely asked for.
			if recorder.aliveAtClose != 2 {
				t.Fatalf("%d agents alive at the close; want the old one and its replacement", recorder.aliveAtClose)
			}
			if len(recorder.closed) != 1 || recorder.closed[0] != running.GroupRef() {
				t.Fatalf("closed %v; want only the replaced session %v", recorder.closed, running.GroupRef())
			}
			after := runningOnTmux(t, f, cfg)
			if len(after) != 1 || after[0].RuntimeSessionID != replaced.RuntimeSessionID {
				t.Fatalf("running afterwards = %+v; want only the replacement", after)
			}
			if live := liveRecords(f.Load(t)); len(live) != 1 || live[0].ID != replaced.RuntimeSessionID {
				t.Fatalf("live records = %+v; want only the replacement (original %s)", live, original.RuntimeSessionID)
			}
		})
		t.Run(name+" when the launch fails", func(t *testing.T) {
			f := tmuxtest.New(t)
			cfg, project := tmuxTestConfig(t)
			original, err := LaunchOn(tmuxRuntime(f), project, cfg.Accounts[0], cfg)
			if err != nil {
				t.Fatal(err)
			}
			running := runningOnTmux(t, f, cfg)[0]
			recorder := &orderedTmux{TargetLauncher: f.Backend, failLaunch: true}
			rt := Runtime{Tmux: recorder, Registry: f.Registry}

			if _, err := replacements(project)[name](rt, running, cfg); !errors.Is(err, errLaunchRefused) {
				t.Fatalf("%s: err = %v; want the refused launch", name, err)
			}
			if !slices.Equal(recorder.order, []string{"launch"}) {
				t.Fatalf("order = %v; want a failed launch and the old agent left alone", recorder.order)
			}
			after := runningOnTmux(t, f, cfg)
			if len(after) != 1 || after[0].RuntimeSessionID != original.RuntimeSessionID {
				t.Fatalf("running afterwards = %+v; want the original still running", after)
			}
			live := liveRecords(f.Load(t))
			if len(live) != 1 || live[0].ID != original.RuntimeSessionID || live[0].State != runtimesession.StateLive {
				t.Fatalf("live records = %+v; want only the original, live", live)
			}
		})
	}
}
