package dashboard

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
	"github.com/sglavoie/agentmux/tui/commands"
)

// runWorkspaceView runs `workspace-view` with args through its real command
// tree, so argument rules are exercised too.
func runWorkspaceView(t *testing.T, args ...string) error {
	t.Helper()
	workspaceViewCmd.SetArgs(args)
	workspaceViewCmd.SilenceUsage, workspaceViewCmd.SilenceErrors = true, true
	t.Cleanup(func() { workspaceViewCmd.SetArgs(nil) })
	return workspaceViewCmd.Execute()
}

// borrowedIdentity puts in this process's environment what an app started
// from inside a managed Kitty or tmux session would hand its views.
func borrowedIdentity(t *testing.T) {
	t.Helper()
	t.Setenv(runtimesession.EnvVar, "rs-0123456789abcdef0123456789abcdef")
	t.Setenv("KITTY_WINDOW_ID", "42")
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty-operator")
	t.Setenv("KITTY_PID", "4242")
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%9")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("AMX_VIEW_TEST_KEEP", "kept")
}

func requireNoBorrowedIdentity(t *testing.T, env []string) {
	t.Helper()
	kept := false
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if utilityIdentityVariable(key) {
			t.Errorf("the view's environment still carries %s", kv)
		}
		kept = kept || kv == "AMX_VIEW_TEST_KEEP=kept"
	}
	if !kept {
		t.Errorf("the view's environment lost an ordinary variable")
	}
}

func restoreWorkspaceViewSeams(t *testing.T) {
	t.Helper()
	request, dash, over, execFn, edit, cfg, registry, page := workspaceViewRequest, workspaceViewDashboard, workspaceViewOverview,
		workspaceViewExec, workspaceViewEdit, workspaceViewConfig, workspaceViewRegistry, workspaceViewPage
	hosted, open, present, portable, desktop, overviewWas := insideKitty, openTmux, tmuxServerPresent, commands.Portable, desktopViews, overviewBackend
	t.Cleanup(func() {
		workspaceViewRequest, workspaceViewDashboard, workspaceViewOverview = request, dash, over
		workspaceViewExec, workspaceViewEdit, workspaceViewConfig, workspaceViewRegistry, workspaceViewPage = execFn, edit, cfg, registry, page
		insideKitty, openTmux, tmuxServerPresent, commands.Portable, desktopViews, overviewBackend = hosted, open, present, portable, desktop, overviewWas
	})
}

// The Dashboard and Overview views run as surfaces hosted by the workspace:
// on tmux, with no Kitty client even when the app inherited a Kitty
// environment, and with the workspace as where their sessions are shown.
func TestWorkspaceSurfacesRunOnTmuxWithoutKittyAndRouteToTheWorkspace(t *testing.T) {
	for _, kind := range []string{"dashboard", "overview"} {
		t.Run(kind, func(t *testing.T) {
			restoreWorkspaceViewSeams(t)
			borrowedIdentity(t)
			// The production probe, reading the environment the command left.
			insideKitty = func() bool { return os.Getenv("KITTY_LISTEN_ON") != "" || os.Getenv("KITTY_WINDOW_ID") != "" }
			tmuxServerPresent = func() bool { return false }
			openTmux = func() (backend.TargetLauncher, error) { return stubTmux{}, nil }
			var asked []protocol.WorkspaceViewRequest
			workspaceViewRequest = func(view protocol.WorkspaceViewRequest) error {
				asked = append(asked, view)
				return nil
			}
			ran := 0
			surface := func() error {
				ran++
				requireNoBorrowedIdentity(t, os.Environ())
				client, err := selectTerminalsWith(string(runtimesession.BackendTmux), func() kitty.KittyClient {
					t.Error("a workspace surface built a Kitty client")
					return stubKitty{}
				})
				if err != nil || client != nil {
					t.Fatalf("selectTerminals = %v, %v; want no Kitty client and no error", client, err)
				}
				if commands.Portable.Tmux == nil || commands.Portable.Launch != runtimesession.BackendTmux || commands.LaunchesInKitty() {
					t.Fatalf("surface terminals = %+v; want launches on tmux", commands.Portable)
				}
				if commands.Portable.Desktop == nil {
					t.Fatal("the surface does not know it is hosted by the workspace")
				}
				return commands.Portable.Desktop(protocol.WorkspaceViewRequest{Kind: protocol.WorkspaceViewDashboard})
			}
			workspaceViewDashboard, workspaceViewOverview = surface, surface

			if err := runWorkspaceView(t, kind); err != nil {
				t.Fatalf("workspace-view %s: %v", kind, err)
			}
			if ran != 1 || len(asked) != 1 {
				t.Fatalf("surface ran %d time(s) and asked the workspace %d time(s); want once each", ran, len(asked))
			}
		})
	}
}

// A surface started any other way is not hosted: nothing is asked of a
// workspace it does not have.
func TestOrdinarySurfacesAreNotHosted(t *testing.T) {
	restoreWorkspaceViewSeams(t)
	desktopViews = nil
	insideKitty = func() bool { return false }
	openTmux = func() (backend.TargetLauncher, error) { return stubTmux{}, nil }
	if _, err := selectTerminalsWith("", func() kitty.KittyClient { return stubKitty{} }); err != nil {
		t.Fatal(err)
	}
	if commands.Portable.Desktop != nil {
		t.Fatal("a surface in an ordinary terminal believes a workspace hosts it")
	}
}

// The shell view is the operator's login shell in the directory, with no
// managed-session or Kitty identity in its environment.
func TestWorkspaceShellIsALoginShellWithNoBorrowedIdentity(t *testing.T) {
	restoreWorkspaceViewSeams(t)
	borrowedIdentity(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Setenv("SHELL", "/bin/zsh")
	var program string
	var argv, env []string
	workspaceViewExec = func(path string, args, environ []string) error {
		program, argv, env = path, args, environ
		return nil
	}
	if err := runWorkspaceView(t, "shell", dir); err != nil {
		t.Fatalf("workspace-view shell: %v", err)
	}
	if program != "/bin/zsh" || !slices.Equal(argv, []string{"-zsh"}) {
		t.Fatalf("ran %q %q; want the login form of $SHELL", program, argv)
	}
	if cwd, _ := os.Getwd(); cwd != dir {
		t.Fatalf("shell starts in %q; want %q", cwd, dir)
	}
	requireNoBorrowedIdentity(t, env)

	for name, args := range map[string][]string{
		"a missing directory": {"shell", filepath.Join(dir, "absent")},
		"no directory":        {"shell"},
	} {
		program = ""
		if err := runWorkspaceView(t, args...); err == nil || program != "" {
			t.Errorf("%s: err=%v ran=%q; want a refusal and no shell", name, err, program)
		}
	}
}

// The editor view runs the configured editor on the path; a configuration
// that does not load costs only its own editor setting.
func TestWorkspaceEditorRunsTheConfiguredEditorWithNoBorrowedIdentity(t *testing.T) {
	restoreWorkspaceViewSeams(t)
	borrowedIdentity(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	var edited []string
	var editors []string
	workspaceViewEdit = func(path string, cfg *config.Config) error {
		edited = append(edited, path)
		editors = append(editors, cfg.Defaults.Editor)
		requireNoBorrowedIdentity(t, os.Environ())
		return nil
	}
	workspaceViewConfig = func() (*config.Config, error) {
		return &config.Config{Defaults: config.Defaults{Editor: "hx"}}, nil
	}
	if err := runWorkspaceView(t, "edit", file); err != nil {
		t.Fatalf("workspace-view edit: %v", err)
	}
	if cwd, _ := os.Getwd(); cwd != dir {
		t.Fatalf("editor on a file starts in %q; want its directory %q", cwd, dir)
	}
	workspaceViewConfig = func() (*config.Config, error) { return nil, errors.New("config.toml: invalid") }
	if err := runWorkspaceView(t, "edit", dir); err != nil {
		t.Fatalf("workspace-view edit with a broken config: %v", err)
	}
	if !slices.Equal(edited, []string{file, dir}) || !slices.Equal(editors, []string{"hx", ""}) {
		t.Fatalf("edited %q with defaults.editor %q", edited, editors)
	}
	if err := runWorkspaceView(t, "edit", "relative/notes.md"); err == nil || len(edited) != 2 {
		t.Fatalf("a relative path was edited: err=%v edited=%q", err, edited)
	}
}

// The pager view reads the pane of the session it names on a real private
// tmux server, and refuses anything that is not that live session's pane.
func TestWorkspacePagerReadsOnlyTheNamedLiveSession(t *testing.T) {
	restoreWorkspaceViewSeams(t)
	f := tmuxtest.New(t)
	project := config.Session{Title: "api", Directory: t.TempDir(), Profile: "work"}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}}, Sessions: []config.Session{project}}
	launched, err := session.LaunchOn(session.Runtime{Tmux: f.Backend, Registry: f.Registry}, project, cfg.Accounts[0], cfg)
	if err != nil {
		t.Fatal(err)
	}
	borrowedIdentity(t)
	workspaceViewRegistry = f.Registry.Path
	openTmux = func() (backend.TargetLauncher, error) { return f.Backend, nil }
	pages := 0
	workspaceViewPage = func(capture func() (string, error)) error {
		pages++
		requireNoBorrowedIdentity(t, os.Environ())
		_, err := capture()
		return err
	}

	if err := runWorkspaceView(t, "pager", string(launched.RuntimeSessionID)); err != nil || pages != 1 {
		t.Fatalf("pager of a live session: err=%v pages=%d", err, pages)
	}

	unknown, err := runtimesession.NewID()
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"an unknown session": string(unknown), "a malformed id": "%0"} {
		if err := runWorkspaceView(t, "pager", id); err == nil || pages != 1 {
			t.Errorf("%s was paged: err=%v pages=%d", name, err, pages)
		}
	}

	// The registry still says live, but the pane is gone: a reference alone
	// is not enough to read a pane.
	if err := f.Backend.Close(launched.Group); err != nil {
		t.Fatal(err)
	}
	if err := runWorkspaceView(t, "pager", string(launched.RuntimeSessionID)); err == nil || pages != 1 {
		t.Fatalf("a session whose pane is gone was paged: err=%v pages=%d", err, pages)
	}

	// Paging registered nothing and changed no record.
	registry := f.Load(t)
	if record, err := registry.Get(launched.RuntimeSessionID); err != nil || record.State != runtimesession.StateLive {
		t.Fatalf("the pager changed the session's record: %+v, %v", record, err)
	}
}
