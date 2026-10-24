package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// failingLauncher wraps a real tmux backend and fails LaunchTarget or Close
// on request.
type failingLauncher struct {
	backend.TargetLauncher
	launchErr error
	closeErr  error
	closed    int
}

func (f *failingLauncher) LaunchTarget(spec backend.LaunchSpec) (backend.Launched, error) {
	if f.launchErr != nil {
		return backend.Launched{}, f.launchErr
	}
	return f.TargetLauncher.LaunchTarget(spec)
}

func (f *failingLauncher) Close(ref backend.Ref) error {
	f.closed++
	if f.closeErr != nil {
		return f.closeErr
	}
	return f.TargetLauncher.Close(ref)
}

func journalingConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	enabled := true
	cfg.Defaults.Diagnostics = config.DiagnosticsConfig{JournalEnabled: &enabled, JournalKeepDays: 14, JournalMaxMiB: 16}
	return filepath.Join(stateHome, "agent-mux", "journal", "events.jsonl")
}

// A failed tmux launch journals a fixed reason, never the backend's error,
// which can carry the project environment and the initial prompt.
func TestLaunchOnTmuxFailureJournalsNoErrorText(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	journal := journalingConfig(t, cfg)
	launcher := &failingLauncher{TargetLauncher: f.Backend, launchErr: errors.New("tmux new-session -e API_TOKEN=hunter2 'secret prompt'")}

	if _, err := LaunchOn(Runtime{Tmux: launcher, Registry: f.Registry}, project, cfg.Accounts[0], cfg); err == nil {
		t.Fatal("launch succeeded")
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2") || strings.Contains(string(data), "secret prompt") {
		t.Fatalf("journal carries the launch error: %s", data)
	}
	if !strings.Contains(string(data), `"reason":"launch_failed:unreadable"`) {
		t.Fatalf("journal = %s, want a launch_failed reason", data)
	}
}

// When an uncommittable launch cannot be closed, its agent may still run, so
// the registration stays pending for AbandonInterrupted instead of being
// recorded as ended.
func TestLaunchOnTmuxUnclosableUncommittedLaunchStaysPending(t *testing.T) {
	f := tmuxtest.New(t)
	cfg, project := tmuxTestConfig(t)
	journal := journalingConfig(t, cfg)
	previous := processStartToken
	processStartToken = func(int) (string, error) { return "", errors.New("ps failed for /secret/path") }
	t.Cleanup(func() { processStartToken = previous })
	launcher := &failingLauncher{TargetLauncher: f.Backend, closeErr: errors.New("close refused")}

	_, err := LaunchOn(Runtime{Tmux: launcher, Registry: f.Registry}, project, cfg.Accounts[0], cfg)
	if err == nil || !strings.Contains(err.Error(), "committing runtime session") || !strings.Contains(err.Error(), "close refused") {
		t.Fatalf("err = %v, want both the commit and the close failure", err)
	}
	if launcher.closed != 1 {
		t.Fatalf("Close called %d times, want 1", launcher.closed)
	}
	records := f.Load(t).List()
	if len(records) != 1 || records[0].State != runtimesession.StatePending {
		t.Fatalf("records = %+v, want one pending registration", records)
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/secret/path") || !strings.Contains(string(data), `"reason":"commit_failed"`) {
		t.Fatalf("journal = %s, want a fixed commit_failed reason", data)
	}
}
