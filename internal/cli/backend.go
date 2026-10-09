package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// BackendFlag names the flag that selects where a launch entrypoint starts
// new agents. Kitty stays the default so existing launches keep working;
// desktop launches pass tmux explicitly. Actions on an existing session never
// read it: they follow the backend the session records.
const BackendFlag = "backend"

// NewTmuxBackend opens the private tmux server every tmux launch and attach
// goes through. Tests replace it with a server under a temporary directory.
var NewTmuxBackend = func() (backend.TargetLauncher, error) {
	server, err := tmux.NewServer(tmux.Options{})
	if err != nil {
		return nil, err
	}
	return tmux.NewBackend(server), nil
}

// AddBackendFlag registers --backend on a launch entrypoint.
func AddBackendFlag(cmd *cobra.Command) {
	cmd.Flags().String(BackendFlag, string(runtimesession.BackendKitty), "backend new agents launch on: kitty or tmux")
	_ = cmd.RegisterFlagCompletionFunc(BackendFlag, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{string(runtimesession.BackendKitty), string(runtimesession.BackendTmux)}, cobra.ShellCompDirectiveNoFileComp
	})
}

// SelectedBackend reads --backend. A command without the flag launches on
// Kitty.
func SelectedBackend(cmd *cobra.Command) (runtimesession.BackendKind, error) {
	value, err := cmd.Flags().GetString(BackendFlag)
	if err != nil {
		return runtimesession.BackendKitty, nil
	}
	switch kind := runtimesession.BackendKind(strings.ToLower(strings.TrimSpace(value))); kind {
	case "", runtimesession.BackendKitty:
		return runtimesession.BackendKitty, nil
	case runtimesession.BackendTmux:
		return kind, nil
	default:
		return "", &actions.Error{Code: "invalid_backend", Message: fmt.Sprintf("unknown --backend %q: use kitty or tmux", value)}
	}
}

// SelectBackend points deps at the backend --backend names. Selecting tmux
// opens the private server; a Kitty client already in deps is kept for the
// lookups that read Kitty topology.
func SelectBackend(cmd *cobra.Command, deps *actions.Dependencies) error {
	kind, err := SelectedBackend(cmd)
	if err != nil {
		return err
	}
	deps.Backend = kind
	if kind != runtimesession.BackendTmux {
		return nil
	}
	server, err := NewTmuxBackend()
	if err != nil {
		return &actions.Error{Code: "backend_unavailable", Message: fmt.Sprintf("tmux backend unavailable: %v", err), Cause: err}
	}
	deps.Tmux = server
	return nil
}
