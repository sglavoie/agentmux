package dashboard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kittykeys"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/customization"
)

// `amx workspace-view` is what the desktop workspace runs inside the terminals
// it embeds beside the agents: its Dashboard, its Overview, and the utility
// views — a shell, an editor, a pager — those two or the operator ask it for,
// Past Sessions opened on one conversation, for an agent that stopped,
// and the keybinding reference and the icon and color pickers its function
// keys open. The workspace names a kind and a target and nothing else; what each kind runs is decided
// here, so the request that travels through the daemon never carries a
// command line.
//
// Two things hold for every kind.
//
// The view is not an agent. Before anything runs, the variables that would
// tie this process to a managed session or to a Kitty window are removed from
// its environment, so nothing started from a utility view — a harness the
// operator runs by hand in the shell, say — reports as the session whose
// environment the app happened to inherit. A view never registers a runtime
// session and never appears in the session list.
//
// The view never needs Kitty. With the Kitty variables gone the dashboard and
// the overview select the tmux backend with no Kitty client, exactly as they
// do in any other terminal, and --backend tmux is explicit so a missing tmux
// is an error instead of a fall back to a Kitty that is not there.

// The seams the tests replace: a test must not page, exec a shell, send to
// the operator's daemon, or draw a full-screen surface.
var (
	workspaceViewRequest = func(view protocol.WorkspaceViewRequest) error {
		_, err := ipc.NewClient().WorkspaceView(view)
		return err
	}
	workspaceViewDashboard = func() error { return Run(0, string(runtimesession.BackendTmux), View{}) }
	workspaceViewOverview  = func() error {
		overviewBackend = string(runtimesession.BackendTmux)
		return runOverviewCommand(0)
	}
	workspaceViewHistory  = runWorkspaceHistory
	workspaceViewExec     = syscall.Exec
	workspaceViewEdit     = session.EditPath
	workspaceViewConfig   = cli.LoadRunnableConfig
	workspaceViewRegistry = runtimesession.DefaultRegistryPath
	workspaceViewPage     = pageScrollback
	workspaceViewKeys     = func() error { return customization.RunKeybindingsReference(kittykeys.Workspace) }
	workspaceViewEnviron  = os.Environ
	workspaceViewUnsetenv = os.Unsetenv
)

// desktopViews is the workspace hosting this process's surface, or nil. It is
// set by `amx workspace-view` before the surface selects its terminals.
var desktopViews func(protocol.WorkspaceViewRequest) error

// WorkspaceViewCommand returns the hidden `workspace-view` command so the
// root command can register it. Root assembly is the only consumer.
func WorkspaceViewCommand() *cobra.Command { return workspaceViewCmd }

var workspaceViewCmd = &cobra.Command{
	Use:    "workspace-view",
	Short:  "Run one view of the desktop workspace in this terminal",
	Hidden: true,
	Long: `Runs what the desktop workspace shows in a terminal of its own: the
dashboard, the overview, Past Sessions on one conversation, or a utility view.
Agent Mux.app starts it; it is not
meant to be typed.

A view is not a managed session. It runs with the session and Kitty variables
removed from its environment, registers nothing, and is never listed or
counted as waiting.`,
}

func init() {
	workspaceViewCmd.AddCommand(
		&cobra.Command{
			Use:   "dashboard",
			Short: "The dashboard, launching on tmux and showing sessions in the workspace",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				hostedByWorkspace()
				return workspaceViewDashboard()
			},
		},
		&cobra.Command{
			Use:   "overview",
			Short: "The live session overview, showing sessions in the workspace",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				hostedByWorkspace()
				return workspaceViewOverview()
			},
		},
		&cobra.Command{
			Use:   "history [HARNESS:SESSION_ID]",
			Short: "Past Sessions, with one conversation selected when named, to continue or fork it",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				if len(args) == 0 {
					hostedByWorkspace()
					return workspaceViewHistory(conversation.Identity{})
				}
				harnessName, sessionID, _ := strings.Cut(strings.TrimSpace(args[0]), ":")
				wanted, ok := conversation.New(harnessName, sessionID)
				if !ok {
					return fmt.Errorf("history view: %q is not HARNESS:SESSION_ID", args[0])
				}
				hostedByWorkspace()
				return workspaceViewHistory(wanted)
			},
		},
		&cobra.Command{
			Use:   "shell DIRECTORY",
			Short: "A login shell in a directory",
			Args:  cobra.ExactArgs(1),
			RunE:  func(_ *cobra.Command, args []string) error { return runWorkspaceShell(args[0]) },
		},
		&cobra.Command{
			Use:   "session-shell RUNTIME_SESSION_ID",
			Short: "A login shell where a session's agent is working",
			Args:  cobra.ExactArgs(1),
			RunE:  func(_ *cobra.Command, args []string) error { return runWorkspaceSessionShell(args[0]) },
		},
		&cobra.Command{
			Use:   "edit PATH",
			Short: "The configured editor on a file or directory",
			Args:  cobra.ExactArgs(1),
			RunE:  func(_ *cobra.Command, args []string) error { return runWorkspaceEditor(args[0]) },
		},
		&cobra.Command{
			Use:   "pager RUNTIME_SESSION_ID",
			Short: "A session's scrollback in a pager",
			Args:  cobra.ExactArgs(1),
			RunE:  func(_ *cobra.Command, args []string) error { return runWorkspacePager(args[0]) },
		},
		&cobra.Command{
			Use:   "keybindings",
			Short: "The workspace's keybinding reference",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				scrubUtilityEnvironment()
				return workspaceViewKeys()
			},
		},
		&cobra.Command{
			Use:   "icon-picker RUNTIME_SESSION_ID",
			Short: "The harness icon picker for a session",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				return runWorkspacePicker("icon-picker", args[0], loadIconPickerConfig, pickIcon)
			},
		},
		&cobra.Command{
			Use:   "color-picker RUNTIME_SESSION_ID",
			Short: "The color picker for a session",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				return runWorkspacePicker("color-picker", args[0], loadPickerConfig, pickColor)
			},
		},
	)
}

// utilityIdentityVariable reports a variable that names a managed session or
// the terminal one runs in. The list is the tmux backend's own, for the same
// reason: a process that is not that session must not be able to speak as it.
func utilityIdentityVariable(key string) bool {
	switch key {
	case runtimesession.EnvVar, "TMUX", "TMUX_PANE", "WINDOWID", "CLAUDECODE":
		return true
	}
	return strings.HasPrefix(key, "KITTY_")
}

// scrubUtilityEnvironment removes every identity variable from this process,
// and so from whatever it starts.
func scrubUtilityEnvironment() {
	for _, kv := range workspaceViewEnviron() {
		if key, _, _ := strings.Cut(kv, "="); utilityIdentityVariable(key) {
			_ = workspaceViewUnsetenv(key)
		}
	}
}

// hostedByWorkspace prepares a surface the workspace embeds: no borrowed
// identity, and the workspace as the place its sessions and utilities show.
func hostedByWorkspace() {
	scrubUtilityEnvironment()
	desktopViews = workspaceViewRequest
}

// runWorkspaceHistory runs the TUI in Past Sessions on one conversation, on
// the tmux backend as the hosted dashboard does. It is not the Dashboard:
// leaving Past Sessions ends it, so the workspace never holds a second one.
func runWorkspaceHistory(wanted conversation.Identity) error {
	cfg, err := loadRunnableConfig()
	if err != nil {
		return err
	}
	client, err := selectTerminals(string(runtimesession.BackendTmux))
	if err != nil {
		return err
	}
	return tui.RunHistory(cfg, client, wanted)
}

// runWorkspaceShell replaces this process with the operator's login shell in
// dir. A login shell, because the app that started this view was not started
// from one and the operator's PATH and prompt live in their profile.
func runWorkspaceShell(dir string) error {
	scrubUtilityEnvironment()
	dir = config.ExpandTilde(dir)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("shell view: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("shell view: %s is not a directory", dir)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("shell view: %w", err)
	}
	shell := os.Getenv("SHELL")
	if shell == "" || !filepath.IsAbs(shell) {
		shell = "/bin/sh"
	}
	// A leading dash on argv[0] is how a shell is told it is a login shell.
	env := append(workspaceViewEnviron(), "PWD="+dir)
	// #nosec G204 -- the operator's own login shell.
	return workspaceViewExec(shell, []string{"-" + filepath.Base(shell)}, env)
}

// runWorkspaceEditor runs the configured editor on path and waits for it. An
// unreadable configuration costs only defaults.editor: $VISUAL, $EDITOR and
// vi still apply, and an editor view is no place to report a config error.
func runWorkspaceEditor(path string) error {
	scrubUtilityEnvironment()
	path = config.ExpandTilde(path)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("editor view: %s is not an absolute path", path)
	}
	dir := path
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		dir = filepath.Dir(path)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("editor view: %w", err)
	}
	cfg, err := workspaceViewConfig()
	if err != nil || cfg == nil {
		cfg = &config.Config{}
	}
	return workspaceViewEdit(path, cfg)
}

// runWorkspaceSessionShell is a shell view in the directory a session's agent
// is working in. The workspace knows the session, not where it is; the pane
// says, and only while it is that very session's.
func runWorkspaceSessionShell(raw string) error {
	scrubUtilityEnvironment()
	_, pane, err := heldSessionPane(raw)
	if err != nil {
		return fmt.Errorf("shell view: %w", err)
	}
	if pane.CWD == "" {
		return errors.New("shell view: the session's pane reports no directory")
	}
	return runWorkspaceShell(pane.CWD)
}

// runWorkspacePager pages the scrollback of the session a runtime-session ID
// names.
func runWorkspacePager(raw string) error {
	scrubUtilityEnvironment()
	server, pane, err := heldSessionPane(raw)
	if err != nil {
		return fmt.Errorf("pager view: %w", err)
	}
	return workspaceViewPage(func() (string, error) { return server.Capture(pane.Ref, backend.ExtentAll) })
}

// runWorkspacePicker runs a picker on the session a runtime-session ID names.
// The session is always named: with the identity variables gone there is no
// "session this is typed in" to fall back to, and a picker never guesses.
func runWorkspacePicker(name, raw string, load func() (*config.Config, error), pick func(pickerTarget, *config.Config) error) error {
	scrubUtilityEnvironment()
	id, err := runtimesession.ParseID(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s view: %w", name, err)
	}
	cfg, err := load()
	if err != nil {
		return fmt.Errorf("load Agent Mux config: %w", err)
	}
	target, err := resolveNamedPickerTarget(name, string(id), "the workspace", cfg)
	if err != nil {
		return err
	}
	return pick(target, cfg)
}

// heldSessionPane finds the tmux pane of the session a runtime-session ID
// names. The pane is returned only when the private server still holds it for
// that very session: a reference alone could name another agent's pane after
// a server restart.
func heldSessionPane(raw string) (backend.TargetLauncher, backend.Pane, error) {
	id, err := runtimesession.ParseID(strings.TrimSpace(raw))
	if err != nil {
		return nil, backend.Pane{}, err
	}
	store, err := runtimesession.NewStore(workspaceViewRegistry())
	if err != nil {
		return nil, backend.Pane{}, err
	}
	registry, err := store.Load()
	if err != nil {
		return nil, backend.Pane{}, err
	}
	record, err := registry.Get(id)
	if err != nil {
		return nil, backend.Pane{}, fmt.Errorf("no runtime session %s", id)
	}
	if record.State != runtimesession.StateLive || record.Target == nil {
		return nil, backend.Pane{}, fmt.Errorf("runtime session %s is %s", id, record.State)
	}
	if record.Target.Instance.Kind != runtimesession.BackendTmux {
		return nil, backend.Pane{}, fmt.Errorf("runtime session %s runs on %s, not in the workspace", id, record.Target.Instance.Kind)
	}
	server, err := openTmux()
	if err != nil {
		return nil, backend.Pane{}, fmt.Errorf("tmux backend unavailable: %w", err)
	}
	pane := backend.PaneID(record.Target.Ref)
	panes, err := server.Discover()
	if err != nil {
		return nil, backend.Pane{}, err
	}
	for _, listed := range panes {
		if listed.Ref == pane && listed.Vars[session.RuntimeSessionVar] == string(id) {
			return server, listed, nil
		}
	}
	return nil, backend.Pane{}, fmt.Errorf("runtime session %s is no longer at pane %s", id, pane.ID)
}

// pageScrollback captures and pages on this terminal.
func pageScrollback(capture func() (string, error)) error {
	view := commands.NewScrollbackView(capture)
	if view == nil {
		return errors.New("pager view: no pager")
	}
	view.SetStdin(os.Stdin)
	view.SetStdout(os.Stdout)
	view.SetStderr(os.Stderr)
	return view.Run()
}
