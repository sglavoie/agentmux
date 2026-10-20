package worktree

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	agentmuxerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// Launches go to Kitty, but the command was typed outside it: Kitty cannot be
// asked, and the tmux sessions are still counted. The warning says, in plain
// words, which sessions are missing, never the kitten command line.
func TestWorktreeListCountsTmuxWhenKittyIsUnreachable(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	root := manager.entries[0].Path
	fixture := tmuxtest.New(t)
	previousObserved, previousTmux := worktreeCommandsObservedTmux, worktreeCommandsTmuxSessions
	t.Cleanup(func() { worktreeCommandsObservedTmux, worktreeCommandsTmuxSessions = previousObserved, previousTmux })
	worktreeCommandsObservedTmux = func() (backend.TargetLauncher, error) { return fixture.Backend, nil }
	worktreeCommandsTmuxSessions = func(backend.TargetLauncher, *config.Config) ([]session.RunningSession, error) {
		return []session.RunningSession{{Project: "api", Profile: "work", Backend: "tmux", RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Directory: root}}, nil
	}
	worktreeCommandsLiveSessions = func(kitty.KittyClient, *config.Config) ([]session.RunningSession, error) {
		return nil, &agentmuxerrors.KittyError{Command: []string{"kitten", "@", "ls"}, Err: kitty.ErrNoKittyTerminal}
	}

	out, err := runWorktree(t, worktreeListCmd, true)
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	var envelope struct {
		Data struct {
			Worktrees []actions.WorktreeView `json:"worktrees"`
			Warnings  []string               `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("list = %q: %v", out, err)
	}
	if len(envelope.Data.Worktrees) != 1 || len(envelope.Data.Worktrees[0].LiveSessions) != 1 {
		t.Fatalf("list = %s; want the tmux session counted", out)
	}
	warnings := strings.Join(envelope.Data.Warnings, "\n")
	if strings.Contains(warnings, "kitten") || !strings.Contains(warnings, "sessions in Kitty are not counted: Kitty is not reachable from this terminal") {
		t.Fatalf("warnings = %q; want the plain Kitty wording", warnings)
	}
}
