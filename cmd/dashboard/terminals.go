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
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/tui/commands"
)

// BackendFlagUsage documents --backend on the surfaces.
const BackendFlagUsage = cli.BackendFlagUsage

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
// requested is --backend or the configured preference, empty when neither was
// set. New agents default to tmux in every terminal. Inside Kitty its existing
// tabs are still listed beside tmux sessions. An unavailable tmux fails with
// its setup error; Kitty launches require an explicit choice.
func selectTerminals(requested string) (kitty.KittyClient, error) {
	return selectTerminalsWith(requested, newKittyClient)
}

// Every standalone surface resolves flags, configuration, and defaults in the
// same order and reports the source of that choice at the point of launch.
func selectTerminalsForConfig(requested string, cfg *config.Config, kittyClient func() kitty.KittyClient) (kitty.KittyClient, error) {
	configured := strings.TrimSpace(requested) == "" && cfg != nil && strings.TrimSpace(cfg.Defaults.LaunchBackend) != ""
	if configured {
		requested = cfg.Defaults.LaunchBackend
	}
	client, err := selectTerminalsWith(requested, kittyClient)
	if err == nil && configured && desktopViews == nil {
		commands.Portable.LaunchSource = "config.toml"
	}
	return client, err
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
		launch = runtimesession.BackendTmux
	}

	var server backend.TargetLauncher
	if launch == runtimesession.BackendTmux || (hosted && tmuxServerPresent()) {
		opened, err := openTmux()
		switch {
		case err == nil:
			server = opened
		case launch == runtimesession.BackendTmux:
			return nil, fmt.Errorf("tmux backend unavailable: %w", err)
		}
	}
	source := "default"
	if explicit {
		source = "--backend"
	}
	if desktopViews != nil {
		source = "workspace host"
	}
	commands.Portable = commands.PortableTerminals{Tmux: server, Launch: launch, LaunchSource: source, Attach: attachCommandFor(server), Send: sendFor(server), Desktop: desktopViews}
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
