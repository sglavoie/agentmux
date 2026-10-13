package dashboard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/spf13/cobra"
)

// In a plain terminal attached to a tmux session, the dashboard and the
// overview are popups over that terminal (the keys in internal/kittykeys.Tmux
// open them, and `amx open-dashboard` does for the terminal used last). A
// terminal shows one popup at a time and tmux ignores a second, so the popup
// is the terminal's one dashboard, as a Kitty session had one dashboard tab.
// It opens on the session the terminal is on, and choosing a row switches the
// terminal to that session and ends the surface, which closes the popup.
//
// A popup is a view. It is not a session, it registers nothing, and whatever
// it starts (a pager, a shell, an editor) runs inside it without the
// variables of the session it is over.

// View is how a surface outside Kitty was asked to open.
type View struct {
	// Select is --select: the runtime session whose row the surface opens
	// on. Empty means the session this process runs in, when it runs in one.
	Select string
	// Client is --client: the tmux client (terminal device) the surface is a
	// popup over. Empty in a popup means the terminal attached to the
	// selected session.
	Client string
	// Popup is --popup: the surface runs in a popup over an attached
	// terminal, so it shows a session by switching that terminal.
	Popup bool
}

// The flags of a surface that can open as a popup.
const (
	selectFlagUsage = "outside Kitty: open on this runtime session's row (default: $" + runtimesession.EnvVar + ")"
	clientFlagUsage = "outside Kitty: the tmux client this surface is a popup over; choosing a session switches it"
	popupFlagUsage  = "outside Kitty: run as a popup over the terminal attached to the selected session"
)

// AddViewFlags adds --select, --client, and --popup to a surface's command.
func AddViewFlags(cmd *cobra.Command, view *View, prefix string) {
	cmd.Flags().StringVar(&view.Select, "select", "", prefix+selectFlagUsage)
	cmd.Flags().StringVar(&view.Client, "client", "", prefix+clientFlagUsage)
	cmd.Flags().BoolVar(&view.Popup, "popup", false, prefix+popupFlagUsage)
}

// The seams the tests replace: the session this process runs in, and the
// registry a switch is recorded in.
var (
	viewCurrentSession = func() string { return strings.TrimSpace(os.Getenv(runtimesession.EnvVar)) }
	viewRegistryPath   = runtimesession.DefaultRegistryPath
)

// terminalView prepares a surface outside Kitty, after its terminals were
// selected: it returns the ID the surface starts selected on, and in a popup
// installs the switch that showing a session becomes. A popup then gives up
// the identity of the session it is over, so nothing it starts speaks as
// that session.
func terminalView(view View) (int, error) {
	named, fromFlag := strings.TrimSpace(view.Select), true
	if named == "" {
		named, fromFlag = viewCurrentSession(), false
	}
	var over runtimesession.ID
	if named != "" {
		id, err := runtimesession.ParseID(named)
		switch {
		case err == nil:
			over = id
		case fromFlag:
			return 0, fmt.Errorf("--select: %q is not a runtime session ID", named)
		}
	}
	client := strings.TrimSpace(view.Client)
	if !view.Popup && client == "" {
		return commands.PortableSelection(over), nil
	}
	server, ok := commands.Portable.Tmux.(*tmux.Backend)
	if !ok {
		return 0, errors.New("a popup is a view over a terminal attached to a tmux session, and tmux is unavailable")
	}
	store, err := runtimesession.NewStore(viewRegistryPath())
	if err != nil {
		return 0, err
	}
	switcher := tmux.Switcher{Store: store, Backend: server, Fingerprint: session.ProcessFingerprint}
	commands.Portable.Switch = func(rs session.RunningSession) error {
		if client == "" {
			if over == "" {
				return errors.New("this popup was not told which terminal it is over (--client)")
			}
			viewer, err := switcher.Viewer(over)
			if err != nil {
				return err
			}
			client = viewer.Name
		}
		if rs.RuntimeSessionID == "" {
			return server.SwitchClient(client, rs.PaneRef())
		}
		return switcher.Switch(client, rs.RuntimeSessionID)
	}
	scrubUtilityEnvironment()
	return commands.PortableSelection(over), nil
}

// tmuxPopups is what opening a popup needs from the tmux backend.
type tmuxPopups interface {
	Survey() (tmux.Survey, error)
	PopupCommand(client string, env map[string]string, argv []string) (*exec.Cmd, error)
}

// errNoAttachedTerminal reports that no terminal is attached to the private
// tmux server, so there is nothing to open a popup over.
var errNoAttachedTerminal = errors.New("no terminal is attached to a tmux session")

// openDashboardPopup opens the dashboard as a popup over the terminal the
// operator used last, selected on the session that terminal is on, and
// returns that terminal's name. The popup's client is started and left
// running: it ends when the popup closes.
func openDashboardPopup(server tmuxPopups, bin string, start func(*exec.Cmd) error) (string, error) {
	survey, err := server.Survey()
	if err != nil {
		return "", fmt.Errorf("reading tmux server: %w", err)
	}
	client, ok := survey.LastActive()
	if !ok {
		return "", errNoAttachedTerminal
	}
	argv := []string{bin, "--popup", "--client", client.Name}
	for _, pane := range survey.Panes {
		if pane.Group != client.Group {
			continue
		}
		if id, err := runtimesession.ParseID(pane.Vars[session.RuntimeSessionVar]); err == nil {
			argv = append(argv, "--select", string(id))
		}
	}
	popup, err := server.PopupCommand(client.Name, nil, argv)
	if err != nil {
		return "", err
	}
	if err := start(popup); err != nil {
		return "", fmt.Errorf("opening a popup over %s: %w", client.Name, err)
	}
	return client.Name, nil
}

// startPopup starts a popup's client without waiting for the popup to close.
func startPopup(popup *exec.Cmd) error {
	if err := popup.Start(); err != nil {
		return err
	}
	return popup.Process.Release()
}
