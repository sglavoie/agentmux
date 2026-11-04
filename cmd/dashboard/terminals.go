package dashboard

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/tui/commands"
)

// BackendFlagUsage documents --backend on the surfaces.
const BackendFlagUsage = cli.BackendFlagUsage

var (
	openTmux     = func() (backend.TargetLauncher, error) { return cli.NewTmuxBackend() }
	attachBinary = cli.AmxBinary
)

// tmuxAttacher is the part of the tmux backend that attaches a terminal.
type tmuxAttacher interface {
	AttachCommand(pane backend.Ref, view tmux.AttachView) (*exec.Cmd, error)
}

// selectTerminals installs the tmux backend for an interactive surface.
func selectTerminals(requested string) error {
	return selectTerminalsWith(requested)
}

// Every standalone surface resolves flags, configuration, and defaults in the
// same order and reports the source of that choice at the point of launch.
func selectTerminalsForConfig(requested string, cfg *config.Config) error {
	configured := strings.TrimSpace(requested) == "" && cfg != nil && strings.TrimSpace(cfg.Defaults.LaunchBackend) != ""
	if configured {
		requested = cfg.Defaults.LaunchBackend
	}
	err := selectTerminalsWith(requested)
	if err == nil && configured && desktopViews == nil {
		commands.Portable.LaunchSource = "config.toml"
	}
	return err
}

func selectTerminalsWith(requested string) error {
	commands.Portable = commands.PortableTerminals{}
	launch := runtimesession.BackendKind(strings.ToLower(strings.TrimSpace(requested)))
	switch launch {
	case "", runtimesession.BackendTmux:
	default:
		return fmt.Errorf("unsupported --backend %q: use tmux", requested)
	}
	explicit := launch != ""
	if !explicit {
		launch = runtimesession.BackendTmux
	}

	server, err := openTmux()
	if err != nil {
		return fmt.Errorf("tmux backend unavailable: %w", err)
	}
	source := "default"
	if explicit {
		source = "--backend"
	}
	if desktopViews != nil {
		source = "workspace host"
	}
	commands.Portable = commands.PortableTerminals{Tmux: server, Launch: launch, LaunchSource: source, Attach: attachCommandFor(server), Send: sendFor(server), Desktop: desktopViews}
	return nil
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
