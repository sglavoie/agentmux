package session

// `amx close` ends one running agent by its runtime-session ID. It is what
// the desktop workspace runs when a session is closed from its sidebar, and
// the single-session counterpart of `amx backend stop`.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
)

var (
	closeJSON bool
	closeYes  bool
)

var closeCmd = &cobra.Command{
	Use:   "close RUNTIME_SESSION_ID",
	Short: "End a running agent and its tmux session",
	Long: `Ends the agent a runtime session runs in AgentMux's private tmux server,
and the tmux session that holds it. It asks for --yes because the agent's
work in progress is lost; its conversation stays on disk and can be resumed.

Only the pane the launch tagged with this runtime session is ever killed. A
session whose agent already stopped has what is left of it cleaned up, and
closing one that is already gone succeeds without doing anything. A Kitty
runtime session is closed by closing its tab.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runClose,
}

func init() {
	closeCmd.Flags().BoolVar(&closeJSON, "json", false, "emit schema-v1 JSON")
	closeCmd.Flags().BoolVar(&closeYes, "yes", false, "confirm ending the agent")
}

// CloseCommand returns the top-level `amx close` command.
func CloseCommand() *cobra.Command { return closeCmd }

// closeData is the `close --json` payload.
type closeData struct {
	RuntimeSessionID string `json:"runtime_session_id"`
	Backend          string `json:"backend"`
	Project          string `json:"project"`
	// Closed is whether a tmux session was killed by this command; false
	// when there was nothing left to kill.
	Closed bool `json:"closed"`
	// Confirmed is whether the session was seen to be gone afterwards.
	Confirmed bool   `json:"confirmed"`
	State     string `json:"state"`
}

// tmuxCloser is what closing needs from the tmux backend.
type tmuxCloser interface {
	session.CloseBackend
	Survey() (tmux.Survey, error)
}

func runClose(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return cli.MissingParameter(cmd, closeJSON, "runtime-session-id", "the runtime session to end (rs-...)")
	}
	id, err := runtimesession.ParseID(strings.TrimSpace(args[0]))
	if err != nil {
		return cli.CommandFailure(cmd, closeJSON, "invalid_arguments", err)
	}
	store, err := runtimesession.NewStore(attachRegistryPath())
	if err != nil {
		return cli.CommandFailure(cmd, closeJSON, "registry_unavailable", err)
	}
	registry, err := store.Load()
	if err != nil {
		return cli.CommandFailure(cmd, closeJSON, "registry_unavailable", err)
	}
	record, err := registry.Get(id)
	if err != nil {
		return cli.CommandFailure(cmd, closeJSON, "not_found", fmt.Errorf("no runtime session %s", id))
	}
	if record.Target == nil {
		return cli.CommandFailure(cmd, closeJSON, notLiveCode(record), notLiveError(record))
	}
	if record.Target.Instance.Kind != runtimesession.BackendTmux {
		return cli.CommandFailure(cmd, closeJSON, "unsupported_backend", fmt.Errorf(
			"runtime session %s runs on %s; close its tab there", id, record.Target.Instance.Kind))
	}
	if !closeYes {
		return cli.CommandFailure(cmd, closeJSON, "confirmation_required", fmt.Errorf(
			"this ends the agent of %s (%s); pass --yes to confirm", id, record.Project))
	}
	opened, err := attachNewTmux()
	if err != nil {
		return cli.CommandFailure(cmd, closeJSON, "backend_unavailable", fmt.Errorf("tmux backend unavailable: %w", err))
	}
	server, ok := opened.(tmuxCloser)
	if !ok {
		return cli.CommandFailure(cmd, closeJSON, "backend_unavailable", errors.New("tmux backend cannot close sessions"))
	}
	survey, err := server.Survey()
	if err != nil {
		return cli.CommandFailure(cmd, closeJSON, "backend_unavailable", fmt.Errorf("reading tmux server: %w", err))
	}
	if survey.Instance.Address != record.Target.Instance.Address {
		return cli.CommandFailure(cmd, closeJSON, "backend_unavailable", fmt.Errorf(
			"runtime session %s belongs to tmux server %s, not %s", id, record.Target.Instance.Address, survey.Instance.Address))
	}
	data := closeData{
		RuntimeSessionID: string(id), Backend: string(record.Target.Instance.Kind), Project: record.Project, Confirmed: true,
	}
	// A restarted server reuses pane IDs, so the registered pane is killed
	// only on the generation it was registered on and only while it still
	// carries this runtime session's tag.
	if pane, ok := backend.FindPane(survey.Panes, backend.PaneID(record.Target.Ref)); ok &&
		survey.Instance.Generation == record.Target.Instance.Generation &&
		pane.Vars[session.RuntimeSessionVar] == string(id) {
		observation, err := session.CloseObserved(server, pane.Group)
		if err != nil && !errors.Is(err, session.ErrCloseTimeout) {
			return cli.CommandFailure(cmd, closeJSON, "backend_unavailable", fmt.Errorf("closing tmux session %s: %w", pane.Group.ID, err))
		}
		data.Closed, data.Confirmed = true, observation.Confirmed()
	}
	// A session the reconciler already marked stale stays as it was recorded:
	// how its agent stopped is not rewritten by cleaning up after it.
	if err := store.Update(func(registry *runtimesession.Registry) error {
		_, err := registry.End(id, attachNow())
		if errors.Is(err, runtimesession.ErrNotFound) || errors.Is(err, runtimesession.ErrInvalidState) {
			err = nil
		}
		if current, getErr := registry.Get(id); getErr == nil {
			data.State = string(current.State)
		}
		return err
	}); err != nil {
		return cli.CommandFailure(cmd, closeJSON, "registry_unavailable", fmt.Errorf("ending %s: %w", id, err))
	}
	if closeJSON {
		return cli.WriteJSON(cmd, data)
	}
	message := fmt.Sprintf("Ended session %s.", record.Project)
	switch {
	case !data.Closed:
		message = fmt.Sprintf("%s was not running; there was nothing to end.", record.Project)
	case !data.Confirmed:
		message = fmt.Sprintf("Asked tmux to end session %s; its exit was not confirmed.", record.Project)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), message)
	return err
}
