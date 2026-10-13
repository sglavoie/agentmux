package worktree

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
	"github.com/spf13/cobra"
)

// worktreeOnTmux selects the tmux backend for the worktree commands, on a
// private server, with every Kitty seam failing the test if it is reached.
type worktreeOnTmux struct {
	fixture   *tmuxtest.Fixture
	workspace []protocol.WorkspaceViewRequest
	hosted    bool
	ran       [][]string
	ranIn     []string
}

func useWorktreeTmux(t *testing.T) *worktreeOnTmux {
	t.Helper()
	w := &worktreeOnTmux{fixture: tmuxtest.New(t)}
	t.Setenv("HOME", t.TempDir())
	previousTmux := cli.NewTmuxBackend
	cli.NewTmuxBackend = func() (backend.TargetLauncher, error) { return w.fixture.Backend, nil }
	previousInside, previousWorkspace, previousRun := worktreeCommandsInsideKitty, worktreeCommandsWorkspace, worktreeCommandsRunHere
	worktreeCommandsNewKitty = func() kitty.KittyClient {
		t.Error("a worktree command on tmux built a Kitty client")
		return nil
	}
	worktreeCommandsLiveSessions = func(kitty.KittyClient, *config.Config) ([]session.RunningSession, error) {
		t.Error("a worktree command on tmux listed Kitty sessions")
		return nil, nil
	}
	worktreeCommandsInsideKitty = func() bool { return false }
	worktreeCommandsWorkspace = func(view protocol.WorkspaceViewRequest) error {
		if !w.hosted {
			return errors.New(protocol.WorkspaceViewErrorUnavailable)
		}
		w.workspace = append(w.workspace, view)
		return nil
	}
	worktreeCommandsRunHere = func(_ *cobra.Command, dir string, argv []string) error {
		w.ran, w.ranIn = append(w.ran, argv), append(w.ranIn, dir)
		return nil
	}
	commands := []*cobra.Command{worktreeReopenCmd, worktreeShellCmd, worktreeEditCmd}
	for _, command := range commands {
		if err := command.Flags().Set(cli.BackendFlag, "tmux"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cli.NewTmuxBackend = previousTmux
		worktreeCommandsInsideKitty, worktreeCommandsWorkspace, worktreeCommandsRunHere = previousInside, previousWorkspace, previousRun
		for _, command := range commands {
			_ = command.Flags().Set(cli.BackendFlag, "")
		}
	})
	return w
}

func runWorktree(t *testing.T, command *cobra.Command, jsonOutput bool, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.Flags().Set("json", map[bool]string{true: "true", false: "false"}[jsonOutput]); err != nil {
		t.Fatal(err)
	}
	err := command.RunE(command, args)
	return out.String(), err
}

// TestWorktreeShellAndEditWorkWithoutKitty: on the tmux backend a worktree
// shell or editor opens in the workspace when it hosts the terminal, and
// otherwise runs where the command was typed; --json reports that instead of
// running anything. Kitty is never asked.
func TestWorktreeShellAndEditWorkWithoutKitty(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	root := manager.entries[0].Path
	w := useWorktreeTmux(t)
	t.Setenv("SHELL", "/bin/zsh")

	if out, err := runWorktree(t, worktreeShellCmd, false, "wt-1"); err != nil {
		t.Fatalf("shell: %v\n%s", err, out)
	}
	if out, err := runWorktree(t, worktreeEditCmd, false, "wt-1"); err != nil {
		t.Fatalf("edit: %v\n%s", err, out)
	}
	wantRan := [][]string{{"/bin/zsh"}, {"true", root}}
	if !slices.EqualFunc(w.ran, wantRan, slices.Equal[[]string]) || !slices.Equal(w.ranIn, []string{root, root}) {
		t.Fatalf("ran %v in %v; want %v in the worktree", w.ran, w.ranIn, wantRan)
	}

	w.ran = nil
	out, err := runWorktree(t, worktreeShellCmd, true, "wt-1")
	if err != nil {
		t.Fatalf("shell --json: %v\n%s", err, out)
	}
	var envelope struct {
		Data struct {
			Changed bool           `json:"changed"`
			Data    map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("shell --json = %q: %v", out, err)
	}
	if envelope.Data.Changed || envelope.Data.Data["continuation"] != actions.DataContinuationHere || envelope.Data.Data["path"] != root || len(w.ran) != 0 {
		t.Fatalf("shell --json = %s, ran %v; want the view left to the caller", out, w.ran)
	}

	w.hosted = true
	if out, err := runWorktree(t, worktreeEditCmd, false, "wt-1"); err != nil {
		t.Fatalf("hosted edit: %v\n%s", err, out)
	}
	if len(w.workspace) != 1 || w.workspace[0].Kind != protocol.WorkspaceViewEditor || w.workspace[0].Path != root || len(w.ran) != 0 {
		t.Fatalf("hosted edit asked for %+v and ran %v; want one editor view of the worktree", w.workspace, w.ran)
	}
}

// TestWorktreeLiveSessionsComeFromTmux: what runs on the private tmux server
// is what keeps a worktree in use when tmux is the backend.
func TestWorktreeLiveSessionsComeFromTmux(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	root := manager.entries[0].Path
	useWorktreeTmux(t)
	previous := worktreeCommandsTmuxSessions
	t.Cleanup(func() { worktreeCommandsTmuxSessions = previous })
	asked := 0
	worktreeCommandsTmuxSessions = func(server backend.TargetLauncher, cfg *config.Config) ([]session.RunningSession, error) {
		asked++
		return []session.RunningSession{{Project: "api", Profile: "work", Backend: "tmux", RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Directory: root}}, nil
	}
	if err := worktreeShowCmd.Flags().Set(cli.BackendFlag, "tmux"); err == nil {
		t.Fatal("show grew a --backend flag; it reads the configured backend")
	}
	previousLoad := worktreeCommandsLoadConfig
	worktreeCommandsLoadConfig = func() (*config.Config, error) {
		cfg, err := previousLoad()
		if err == nil {
			cfg.Defaults.LaunchBackend = "tmux"
		}
		return cfg, err
	}
	out, err := runWorktree(t, worktreeShowCmd, true, "wt-1")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	var envelope struct {
		Data struct {
			Worktree actions.WorktreeView `json:"worktree"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("show = %q: %v", out, err)
	}
	if asked == 0 || len(envelope.Data.Worktree.LiveSessions) != 1 {
		t.Fatalf("show asked tmux %d times and lists %+v; want the tmux session in the worktree", asked, envelope.Data.Worktree.LiveSessions)
	}
}

// A tmux that cannot be used stops a reopen and nothing else: the inventory
// is still shown, with what is running reported as unknown rather than as
// nothing.
func TestWorktreeCommandsSurviveAnUnusableTmux(t *testing.T) {
	resetWorktreeCommandsTestState(t)
	useWorktreeTmux(t)
	cli.NewTmuxBackend = func() (backend.TargetLauncher, error) { return nil, errors.New("tmux: not found") }
	previousLoad := worktreeCommandsLoadConfig
	worktreeCommandsLoadConfig = func() (*config.Config, error) {
		cfg, err := previousLoad()
		if err == nil {
			cfg.Defaults.LaunchBackend = "tmux"
		}
		return cfg, err
	}

	out, err := runWorktree(t, worktreeReopenCmd, true, "wt-1")
	if payload := decodeWorktreeJSONError(t, []byte(out), err); payload.Error.Code != "backend_unavailable" {
		t.Fatalf("reopen error = %+v; want backend_unavailable", payload.Error)
	}
	out, err = runWorktree(t, worktreeShowCmd, true, "wt-1")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	var envelope struct {
		Data struct {
			Worktree actions.WorktreeView `json:"worktree"`
			Warnings []string             `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("show = %q: %v", out, err)
	}
	if envelope.Data.Worktree.ID != "wt-1" || len(envelope.Data.Worktree.LiveSessions) != 0 || len(envelope.Data.Warnings) == 0 {
		t.Fatalf("show = %s; want the worktree with a warning that its sessions could not be read", out)
	}
}
