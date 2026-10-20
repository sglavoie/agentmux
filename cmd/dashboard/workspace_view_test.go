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
	"github.com/sglavoie/agentmux/internal/conversation"
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
	history := workspaceViewHistory
	t.Cleanup(func() { workspaceViewHistory = history })
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
	for _, kind := range []string{"dashboard", "overview", "history"} {
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
			args := []string{kind}
			if kind == "history" {
				args = append(args, " Claude:0b6f1c2e-session ")
				workspaceViewHistory = func(wanted conversation.Identity) error {
					if want := (conversation.Identity{Harness: harness.Claude, SessionID: "0b6f1c2e-session"}); wanted != want {
						t.Errorf("history view opened on %+v, want %+v", wanted, want)
					}
					return surface()
				}
			}

			if err := runWorkspaceView(t, args...); err != nil {
				t.Fatalf("workspace-view %s: %v", kind, err)
			}
			if ran != 1 || len(asked) != 1 {
				t.Fatalf("surface ran %d time(s) and asked the workspace %d time(s); want once each", ran, len(asked))
			}
		})
	}
}

// A history view is opened on an exact conversation or not at all: a selector
// that names no harness or no session draws nothing.
func TestWorkspaceHistoryRefusesAMalformedConversation(t *testing.T) {
	for _, selector := range []string{"0b6f1c2e-session", "claude:", ":0b6f1c2e-session", " "} {
		restoreWorkspaceViewSeams(t)
		workspaceViewHistory = func(conversation.Identity) error {
			t.Errorf("history view ran for %q", selector)
			return nil
		}
		if err := runWorkspaceView(t, "history", selector); err == nil {
			t.Errorf("workspace-view history %q was accepted", selector)
		}
	}
}

// With no conversation named, the history view is Past Sessions over every
// conversation, the workspace's ⌘Y: hosted like any surface, selecting nothing.
func TestWorkspaceHistoryWithNoConversationOpensAllPastSessions(t *testing.T) {
	restoreWorkspaceViewSeams(t)
	insideKitty = func() bool { return false }
	tmuxServerPresent = func() bool { return false }
	openTmux = func() (backend.TargetLauncher, error) { return stubTmux{}, nil }
	ran := 0
	workspaceViewHistory = func(wanted conversation.Identity) error {
		ran++
		if wanted != (conversation.Identity{}) {
			t.Errorf("history view opened on %+v, want no conversation", wanted)
		}
		if desktopViews == nil {
			t.Error("the surface does not know it is hosted by the workspace")
		}
		return nil
	}
	if err := runWorkspaceView(t, "history"); err != nil {
		t.Fatalf("workspace-view history: %v", err)
	}
	if ran != 1 {
		t.Fatalf("history view ran %d time(s), want once", ran)
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

	// A shell beside the session starts where its pane is, as a login shell.
	var program string
	workspaceViewExec = func(path string, _, environ []string) error {
		program = path
		requireNoBorrowedIdentity(t, environ)
		return nil
	}
	t.Chdir(t.TempDir())
	if err := runWorkspaceView(t, "session-shell", string(launched.RuntimeSessionID)); err != nil || program == "" {
		t.Fatalf("shell beside a live session: err=%v ran=%q", err, program)
	}
	want, err := filepath.EvalSymlinks(project.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if cwd, _ := os.Getwd(); cwd != want {
		t.Fatalf("the session's shell starts in %q; want %q", cwd, want)
	}
	program = ""
	if err := runWorkspaceView(t, "session-shell", string(unknown)); err == nil || program != "" {
		t.Fatalf("a shell was opened beside an unknown session: err=%v ran=%q", err, program)
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

// The workspace's F5 and F6 open a picker on the session it names, even when
// several are running and the app inherited another session's identity: a
// picker hosted there never falls back to "the session this is typed in".
func TestWorkspacePickersEditOnlyTheNamedSession(t *testing.T) {
	w := newPortableWorld(t, "api", "web")
	borrowedIdentity(t)

	if err := runWorkspaceView(t, "color-picker", string(w.ids["web"])); err != nil || len(w.colors) != 1 {
		t.Fatalf("color picker: err=%v opened=%d", err, len(w.colors))
	}
	requireNoBorrowedIdentity(t, os.Environ())
	if err := w.colors[0].Apply("red"); err != nil {
		t.Fatal(err)
	}
	// api keeps the color its launch recorded: its profile's.
	if got := w.sessions(); got["web"].Color != "red" || got["api"].Color != "blue" {
		t.Fatalf("colors after picking red for web: web=%q api=%q", got["web"].Color, got["api"].Color)
	}

	if err := runWorkspaceView(t, "icon-picker", string(w.ids["api"])); err != nil || len(w.icons) != 1 {
		t.Fatalf("icon picker: err=%v opened=%d", err, len(w.icons))
	}

	unknown, err := runtimesession.NewID()
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"an unknown session": string(unknown), "a malformed id": "%0"} {
		if err := runWorkspaceView(t, "icon-picker", id); err == nil || len(w.icons) != 1 {
			t.Errorf("icon picker opened for %s: err=%v opened=%d", name, err, len(w.icons))
		}
	}
}

// F1 in the workspace shows the workspace's own keys, not Kitty's.
func TestWorkspaceKeybindingsViewListsTheWorkspaceKeys(t *testing.T) {
	previous := workspaceViewKeys
	t.Cleanup(func() { workspaceViewKeys = previous })
	borrowedIdentity(t)
	shown := 0
	workspaceViewKeys = func() error {
		shown++
		requireNoBorrowedIdentity(t, os.Environ())
		return nil
	}
	if err := runWorkspaceView(t, "keybindings"); err != nil || shown != 1 {
		t.Fatalf("keybindings view: err=%v shown=%d", err, shown)
	}
}
