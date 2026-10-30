package cli

import (
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// BackendFlag names the flag that selects where a launch entrypoint starts
// new agents. Without it a launch goes where `[defaults] launch_backend` says,
// or to tmux when no preference is configured. Actions on an existing
// session never read it: they follow the backend the session records.
const BackendFlag = "backend"

// NewTmuxBackend opens the private tmux server every tmux launch and attach
// goes through, running the tmux the configuration names, the one bundled
// with Agent Mux.app, or a supported one on PATH (tmux.ResolveRuntime). Tests
// replace it with a server under a temporary directory.
var NewTmuxBackend = func() (backend.TargetLauncher, error) {
	configureTmuxOnce.Do(func() {
		// A command that never loaded the configuration (attach, the
		// dashboard) still honours `[defaults.tmux] path`. A configuration
		// that cannot be read is that command's problem to report, not a
		// reason to refuse an attach.
		if cfg, err := config.LoadDefault(); err == nil {
			ConfigureTmux(cfg)
		}
	})
	server, err := tmux.NewServer(tmux.Options{})
	if err != nil {
		return nil, err
	}
	return tmux.NewBackend(server), nil
}

var configureTmuxOnce sync.Once

// ConfigureTmux hands `[defaults.tmux] path` to the tmux backend for the rest
// of this process.
func ConfigureTmux(cfg *config.Config) {
	tmux.SetConfiguredBinary(cfg.TmuxPath())
}

// BackendFlagUsage is --backend's help on a launch entrypoint.
const BackendFlagUsage = "backend new agents launch on: kitty or tmux (default: [defaults] launch_backend; unset, tmux)"

// AddBackendFlag registers --backend on a launch entrypoint.
func AddBackendFlag(cmd *cobra.Command) {
	AddBackendFlagUsage(cmd, BackendFlagUsage)
}

// AddBackendFlagUsage registers --backend with help of its own, for a command
// where the backend decides something other than where an agent launches.
func AddBackendFlagUsage(cmd *cobra.Command, usage string) {
	cmd.Flags().String(BackendFlag, "", usage)
	_ = cmd.RegisterFlagCompletionFunc(BackendFlag, CompleteBackends)
}

// SelectedBackend reads --backend, falling back to the configured launch
// backend when the flag is absent or empty. A command without the flag and a
// nil configuration launch on tmux.
func SelectedBackend(cmd *cobra.Command, cfg *config.Config) (runtimesession.BackendKind, error) {
	value, err := cmd.Flags().GetString(BackendFlag)
	if err != nil {
		value = ""
	}
	switch kind := runtimesession.BackendKind(strings.ToLower(strings.TrimSpace(value))); kind {
	case "":
		return runtimesession.BackendKind(cfg.LaunchBackendOrDefault()), nil
	case runtimesession.BackendKitty, runtimesession.BackendTmux:
		return kind, nil
	default:
		return "", &actions.Error{Code: "invalid_backend", Message: fmt.Sprintf("unknown --backend %q: use kitty or tmux", value)}
	}
}

// SelectBackend points deps at the backend --backend names, or the configured
// one. Selecting tmux opens the private server; a Kitty client already in deps
// is kept for the lookups that read Kitty topology.
func SelectBackend(cmd *cobra.Command, deps *actions.Dependencies) error {
	kind, err := SelectedBackend(cmd, deps.Config)
	if err != nil {
		return err
	}
	deps.Backend = kind
	if kind != runtimesession.BackendTmux {
		return nil
	}
	if deps.Config != nil {
		ConfigureTmux(deps.Config)
	}
	server, err := NewTmuxBackend()
	if err != nil {
		return &actions.Error{Code: "backend_unavailable", Message: fmt.Sprintf("tmux backend unavailable: %v", err), Cause: err}
	}
	deps.Tmux = server
	return nil
}
