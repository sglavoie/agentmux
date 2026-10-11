package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

// NameSessionCommand returns the root-level name-session leaf so the root
// command can register it.
func NameSessionCommand() *cobra.Command { return nameSessionCmd }

var (
	nameSessionRuntimeSession string
	nameSessionJSON           bool
)

var nameSessionCmd = &cobra.Command{
	Use:   "name-session [name]",
	Short: "Name a tmux session, or remove its name",
	Long: `Gives the tmux session named by --runtime-session a name of its own, kept
on its pane (cc_name) beside its color. Agent Mux.app shows the name above the
session's profile in its sidebar; a session with no name looks as it always
did. With no name, or an empty one, the session's name is removed.

The name is kept on one line, without control characters, and no longer than
64 characters.

With --json it reports {runtime_session_id, name, changed}: name is the name
the session is left with, and changed is false when it already had it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runNameSession,
}

func init() {
	nameSessionCmd.Flags().StringVar(&nameSessionRuntimeSession, "runtime-session", "",
		"runtime session ID of the tmux session to name")
	nameSessionCmd.Flags().BoolVar(&nameSessionJSON, "json", false, "emit schema-v1 JSON")
}

// nameSessionResult is the `name-session --json` data. Name is the name the
// session is left with, empty for none.
type nameSessionResult struct {
	RuntimeSessionID string `json:"runtime_session_id"`
	Name             string `json:"name"`
	Changed          bool   `json:"changed"`
}

// nameTerminal is what naming a tmux session needs from its backend.
type nameTerminal interface {
	backend.Discoverer
	backend.MetadataWriter
}

// errNameNoSession reports that the runtime session named is not a running
// tmux session.
var errNameNoSession = errors.New("no running tmux session")

func runNameSession(cmd *cobra.Command, args []string) error {
	named := strings.TrimSpace(nameSessionRuntimeSession)
	if named == "" {
		return cli.MissingParameter(cmd, nameSessionJSON, "--runtime-session", "the runtime session ID of the tmux session to name")
	}
	id, err := runtimesession.ParseID(named)
	if err != nil {
		return cli.CommandFailure(cmd, nameSessionJSON, "invalid_arguments", err)
	}
	opened, err := attachNewTmux()
	if err != nil {
		return cli.CommandFailure(cmd, nameSessionJSON, "backend_unavailable", fmt.Errorf("tmux backend unavailable: %w", err))
	}
	term, ok := opened.(nameTerminal)
	if !ok {
		return cli.CommandFailure(cmd, nameSessionJSON, "backend_unavailable", errors.New("tmux backend cannot name sessions"))
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	result, err := nameRuntimeSession(term, id, name)
	if errors.Is(err, errNameNoSession) {
		return cli.CommandFailure(cmd, nameSessionJSON, "not_found", err)
	}
	if err != nil {
		return cli.CommandFailure(cmd, nameSessionJSON, "name_session_failed", fmt.Errorf("name session: %w", err))
	}
	if nameSessionJSON {
		return cli.WriteJSON(cmd, result)
	}
	return nil
}

// nameRuntimeSession gives the tmux session a runtime session ID names the
// name, cleaned; an empty one removes it. Only a pane that still carries that
// runtime session is touched.
func nameRuntimeSession(term nameTerminal, id runtimesession.ID, name string) (nameSessionResult, error) {
	result := nameSessionResult{RuntimeSessionID: string(id), Name: session.CleanName(name)}
	panes, err := term.Discover()
	if err != nil {
		return result, err
	}
	for _, pane := range panes {
		if pane.Vars[session.RuntimeSessionVar] != string(id) {
			continue
		}
		if pane.Vars[session.NameVar] == result.Name {
			return result, nil
		}
		result.Changed = true
		return result, term.SetMetadata(pane.Ref, map[string]string{session.NameVar: result.Name})
	}
	return result, fmt.Errorf("%w is %s", errNameNoSession, id)
}
