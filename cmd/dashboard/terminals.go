package dashboard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/tui/commands"
)

// BackendFlagUsage documents --backend on the surfaces. Unlike the launch
// commands' flag it has no fixed default: the surface launches where it can
// show the result.
const BackendFlagUsage = "backend new agents launch on: kitty or tmux (default: kitty inside Kitty, tmux in any other terminal)"

// The seams the tests replace: a test must not read the operator's Kitty
// environment or open their tmux server.
var (
	insideKitty = func() bool {
		return os.Getenv("KITTY_LISTEN_ON") != "" || cli.WindowIDFromEnv() > 0
	}
	openTmux          = func() (backend.TargetLauncher, error) { return cli.NewTmuxBackend() }
	tmuxServerPresent = func() bool {
		_, err := os.Stat(filepath.Join(tmux.DefaultDir(), "tmux.sock"))
		return err == nil
	}
	attachBinary   = cli.AmxBinary
	newKittyClient = func() kitty.KittyClient { return cli.NewKittyClient() }
)

// tmuxAttacher is the part of the tmux backend that attaches a terminal.
type tmuxAttacher interface {
	AttachCommand(pane backend.Ref, view tmux.AttachView) (*exec.Cmd, error)
}

// selectTerminals decides which terminals a surface works with and installs
// the portable half for the TUI. It returns the Kitty client, which is nil
// for a surface running outside Kitty on the tmux backend: such a surface
// never asks Kitty anything.
//
// requested is --backend, empty when the operator did not choose. Inside
// Kitty the default stays Kitty, and tmux sessions are listed beside the tabs
// whenever the private server exists. In any other terminal the default is
// tmux; when tmux cannot be used there, the surface falls back to the Kitty
// client and fails the way it always has outside Kitty.
func selectTerminals(requested string) (kitty.KittyClient, error) {
	return selectTerminalsWith(requested, newKittyClient)
}

func selectTerminalsWith(requested string, kittyClient func() kitty.KittyClient) (kitty.KittyClient, error) {
	commands.Portable = commands.PortableTerminals{}
	hosted := insideKitty()
	launch := runtimesession.BackendKind(strings.ToLower(strings.TrimSpace(requested)))
	switch launch {
	case "", runtimesession.BackendKitty, runtimesession.BackendTmux:
	default:
		return nil, fmt.Errorf("unknown --backend %q: use kitty or tmux", requested)
	}
	explicit := launch != ""
	if !explicit {
		launch = runtimesession.BackendKitty
		if !hosted {
			launch = runtimesession.BackendTmux
		}
	}

	var server backend.TargetLauncher
	if launch == runtimesession.BackendTmux || (hosted && tmuxServerPresent()) {
		opened, err := openTmux()
		switch {
		case err == nil:
			server = opened
		case explicit && launch == runtimesession.BackendTmux:
			return nil, fmt.Errorf("tmux backend unavailable: %w", err)
		case launch == runtimesession.BackendTmux:
			// Nothing was asked for and tmux cannot be used: behave as before.
			launch = runtimesession.BackendKitty
		}
	}
	commands.Portable = commands.PortableTerminals{Tmux: server, Launch: launch, Attach: attachCommandFor(server), Send: sendFor(server), Desktop: desktopViews}
	if !hosted && launch == runtimesession.BackendTmux {
		return nil, nil
	}
	return kittyClient(), nil
}

// attachCommandFor builds the command that shows a tmux session in this
// terminal. A registered session goes through `amx attach`, which validates
// the target and records the attachment; one with no runtime session is
// attached directly.
func attachCommandFor(server backend.TargetLauncher) func(session.RunningSession) (*exec.Cmd, error) {
	if server == nil {
		return nil
	}
	return func(rs session.RunningSession) (*exec.Cmd, error) {
		if rs.RuntimeSessionID != "" {
			bin, err := attachBinary()
			if err != nil {
				return nil, err
			}
			// #nosec G204 -- this binary, with a validated runtime-session ID.
			return exec.Command(bin, "attach", string(rs.RuntimeSessionID)), nil
		}
		attacher, ok := server.(tmuxAttacher)
		if !ok {
			return nil, errors.New("tmux backend cannot attach terminals")
		}
		return attacher.AttachCommand(rs.PaneRef(), tmux.AttachView{StatusLine: true})
	}
}
