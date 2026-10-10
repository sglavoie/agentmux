package actions

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

func tmuxDeps(f *tmuxtest.Fixture, cfg *config.Config) Dependencies {
	return Dependencies{Config: cfg, Backend: runtimesession.BackendTmux, Tmux: f.Backend, Runtimes: f.Registry}
}

func tmuxSessions(t *testing.T, f *tmuxtest.Fixture, cfg *config.Config) []session.RunningSession {
	t.Helper()
	panes, err := f.Backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	return session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, cfg)
}

func TestLaunchActionRefusesAnUnusableBackend(t *testing.T) {
	cfg := continuityConfig(harness.Claude)
	project := cfg.Projects[0]
	for _, test := range []struct {
		name string
		deps Dependencies
		code string
	}{
		{name: "tmux without a server", deps: Dependencies{Config: cfg, Backend: runtimesession.BackendTmux, Kitty: &continuityKitty{}}, code: "missing_dependency"},
		{name: "unknown backend", deps: Dependencies{Config: cfg, Backend: "screen", Kitty: &continuityKitty{}}, code: "invalid_backend"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DefaultRegistry().Execute(context.Background(), test.deps, Launch, Request{Project: &project})
			var actionErr *Error
			if !errors.As(err, &actionErr) || actionErr.Code != test.code {
				t.Fatalf("err = %v, want %s", err, test.code)
			}
			if kitty := test.deps.Kitty.(*continuityKitty); len(kitty.launched) != 0 {
				t.Fatal("refused launch fell back to Kitty")
			}
		})
	}
}

func TestLaunchActionOnTmuxReportsRuntimeSession(t *testing.T) {
	f := tmuxtest.New(t)
	cfg := continuityConfig(harness.Claude)
	cfg.Projects[0].Directory = t.TempDir()
	project := cfg.Projects[0]
	result, err := DefaultRegistry().Execute(context.Background(), tmuxDeps(f, cfg), Launch, Request{Project: &project})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.Data["runtime_session_id"].(string)
	if result.Data["backend"] != "tmux" || id == "" {
		t.Fatalf("data = %+v, want tmux backend and runtime session", result.Data)
	}
	if running := tmuxSessions(t, f, cfg); len(running) != 1 || string(running[0].RuntimeSessionID) != id {
		t.Fatalf("running = %+v, want the launched runtime session", running)
	}
}

// TestLaunchAndRestartAnnounceTheRuntimeSession: a surface hosted somewhere
// that shows agents is told which runtime session each launch ended on, with
// the same identity the result reports.
func TestLaunchAndRestartAnnounceTheRuntimeSession(t *testing.T) {
	f := tmuxtest.New(t)
	cfg := continuityConfig(harness.Claude)
	cfg.Projects[0].Directory = t.TempDir()
	project := cfg.Projects[0]
	deps := tmuxDeps(f, cfg)
	var announced []session.LaunchResult
	deps.Launched = func(launched session.LaunchResult) { announced = append(announced, launched) }

	result, err := DefaultRegistry().Execute(context.Background(), deps, Launch, Request{Project: &project})
	if err != nil {
		t.Fatal(err)
	}
	if len(announced) != 1 || string(announced[0].RuntimeSessionID) != result.Data["runtime_session_id"] {
		t.Fatalf("announced %+v; want the launched runtime session %v", announced, result.Data["runtime_session_id"])
	}
	running := tmuxSessions(t, f, cfg)
	restarted, err := DefaultRegistry().Execute(context.Background(), deps, Restart, Request{Session: &running[0], Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(announced) != 2 || string(announced[1].RuntimeSessionID) != restarted.Data["runtime_session_id"] ||
		announced[1].RuntimeSessionID == announced[0].RuntimeSessionID {
		t.Fatalf("announced %+v; want the restart's new runtime session %v", announced, restarted.Data["runtime_session_id"])
	}
}

// TestSessionActionsFollowTheRecordedBackend restarts and then closes a
// tmux-hosted session through dependencies whose launch backend is Kitty:
// both act on tmux because that is where the session runs.
func TestSessionActionsFollowTheRecordedBackend(t *testing.T) {
	f := tmuxtest.New(t)
	cfg := continuityConfig(harness.Claude)
	cfg.Projects[0].Directory = t.TempDir()
	if _, err := session.LaunchOn(session.Runtime{Tmux: f.Backend, Registry: f.Registry}, cfg.Projects[0], cfg.Accounts[0], cfg); err != nil {
		t.Fatal(err)
	}
	original := tmuxSessions(t, f, cfg)[0]
	kitty := &continuityKitty{}

	if _, err := DefaultRegistry().Execute(context.Background(), Dependencies{Config: cfg, Kitty: kitty}, Restart, Request{Session: &original, Confirmed: true}); err == nil {
		t.Fatal("restart without the tmux server succeeded")
	}
	deps := Dependencies{Config: cfg, Kitty: kitty, Tmux: f.Backend, Runtimes: f.Registry}
	result, err := DefaultRegistry().Execute(context.Background(), deps, Restart, Request{Session: &original, Confirmed: true})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if result.Data["backend"] != "tmux" || len(kitty.launched) != 0 {
		t.Fatalf("restart data = %+v kitty launches = %d, want a tmux-only restart", result.Data, len(kitty.launched))
	}
	running := tmuxSessions(t, f, cfg)
	if len(running) != 1 || running[0].RuntimeSessionID == original.RuntimeSessionID {
		t.Fatalf("running = %+v, want only the replacement", running)
	}

	replacement := running[0]
	result, err = DefaultRegistry().Execute(context.Background(), deps, Close, Request{Session: &replacement, Confirmed: true})
	if err != nil || !CloseConfirmed(result) {
		t.Fatalf("close = %+v, %v; want a confirmed close", result, err)
	}
	registry, err := f.Registry.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range registry.List() {
		if record.State != runtimesession.StateEnded {
			t.Fatalf("record %s is %s after restart and close, want ended", record.ID, record.State)
		}
	}
}

// TestForkWorktreeOnTmuxCompensatesOnlyItsOwnWorktree forks into a worktree
// on tmux, then fails a second fork's launch: only the worktree that failed
// request created is removed, and the first fork keeps running.
func TestForkWorktreeOnTmuxCompensatesOnlyItsOwnWorktree(t *testing.T) {
	f := tmuxtest.New(t)
	cfg := continuityConfig(harness.Claude)
	reader := &continuityReader{harness: harness.Claude}
	meta := continuityMeta(t, harness.Claude, "source-1", "work")
	deps := tmuxDeps(f, cfg)
	deps.Readers = map[string]transcript.Reader{harness.Claude: reader}

	good := &continuityWorktree{created: worktree.Result{ProjectLabel: "api", Path: t.TempDir(), Branch: "amx/api/source-1", BaseRef: "HEAD", SourceRoot: t.TempDir()}}
	deps.Worktree = good
	result, err := DefaultRegistry().Execute(context.Background(), deps, ForkWorktree, Request{Conversation: &meta, Confirmed: true})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if result.Data["backend"] != "tmux" || result.Data["runtime_session_id"] == nil || len(good.removed) != 0 {
		t.Fatalf("fork result = %+v removed = %+v", result.Data, good.removed)
	}

	bad := &continuityWorktree{created: worktree.Result{ProjectLabel: "api", Path: filepath.Join(t.TempDir(), "never-created"), Branch: "amx/api/source-2", BaseRef: "HEAD", SourceRoot: t.TempDir()}}
	deps.Worktree = bad
	_, err = DefaultRegistry().Execute(context.Background(), deps, ForkWorktree, Request{Conversation: &meta, Confirmed: true})
	var actionErr *Error
	if !errors.As(err, &actionErr) || actionErr.Code != "launch_failed" {
		t.Fatalf("err = %v, want launch_failed", err)
	}
	if len(bad.removed) != 1 || bad.removed[0] != bad.created || len(good.removed) != 0 {
		t.Fatalf("cleanup good=%+v bad=%+v, want only the failed request's worktree", good.removed, bad.removed)
	}
	running := tmuxSessions(t, f, cfg)
	if len(running) != 1 || running[0].Directory != good.created.Path {
		t.Fatalf("running = %+v, want only the first fork", running)
	}
}
