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
	nameSessionClear          bool
	nameSessionJSON           bool
)

var nameSessionCmd = &cobra.Command{
	Use:   "name-session <name> | --clear",
	Short: "Name a tmux session, or remove its name",
	Long: `Gives the tmux session named by --runtime-session a name of its own, kept
on its pane (cc_name) beside its color. Agent Mux.app shows the name above the
session's profile in its sidebar; a session with no name looks as it always
did. --clear, or an explicit empty name (""), removes the session's name; a
name is required otherwise, so a forgotten argument never erases one.

A name that is itself a runtime session ID is refused: the session to name is
given with --runtime-session, never as the name.

The name is kept on one line, without control characters, and no longer than
64 characters.

With no --runtime-session, a command that runs in a managed session
(AMX_RUNTIME_SESSION_ID) names that session; elsewhere --runtime-session is
required.

It prints one line saying what it did. With --json it reports
{runtime_session_id, name, changed}: name is the name the session is left
with, and changed is false when it already had it.`,
	Example: `  amx name-session "billing fix"
  amx name-session --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f "billing fix"
  amx name-session --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --clear`,
	Args: cobra.MaximumNArgs(1),
	RunE: runNameSession,
}

func init() {
	nameSessionCmd.Flags().StringVar(&nameSessionRuntimeSession, "runtime-session", "",
		"runtime session ID of the tmux session to name (defaults to $AMX_RUNTIME_SESSION_ID)")
	nameSessionCmd.Flags().BoolVar(&nameSessionClear, "clear", false, "remove the session's name instead of setting one")
	cli.AddJSONFlag(nameSessionCmd, &nameSessionJSON)
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

func runNameSession(cmd *cobra.Command, args []string) error {
	name, err := nameSessionArgument(cmd, args)
	if err != nil {
		return err
	}
	named := strings.TrimSpace(nameSessionRuntimeSession)
	if named == "" {
		// With nothing named, the session this command runs in is the one
		// meant, as with mute-toggle.
		named = currentRuntimeSession()
	}
	if named == "" {
		return cli.MissingParameter(cmd, nameSessionJSON, "--runtime-session", "the runtime session ID of the tmux session to name")
	}
	id, err := cli.ParseRuntimeSessionID(named)
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
	result, err := nameRuntimeSession(term, id, name)
	if errors.Is(err, errNoRuntimeSession) {
		return cli.CommandFailure(cmd, nameSessionJSON, "not_found", err)
	}
	if err != nil {
		return cli.CommandFailure(cmd, nameSessionJSON, "name_session_failed", fmt.Errorf("name session: %w", err))
	}
	if nameSessionJSON {
		return cli.WriteJSON(cmd, result)
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), nameSessionSummary(result))
	return err
}

// nameSessionArgument is the name the invocation asks for, "" to remove it.
// Removing takes --clear or an explicit empty name: a bare name-session used
// to erase the name silently, which a forgotten argument should never do. A
// name that is a runtime session ID is the session typed in the wrong place.
func nameSessionArgument(cmd *cobra.Command, args []string) (string, error) {
	if nameSessionClear {
		if len(args) > 0 && args[0] != "" {
			return "", cli.CommandFailure(cmd, nameSessionJSON, "invalid_arguments", errors.New("--clear removes the name; give a name or --clear, not both"))
		}
		return "", nil
	}
	if len(args) == 0 {
		return "", cli.MissingParameter(cmd, nameSessionJSON, "name", `the name to give the session; pass --clear (or "") to remove its name`)
	}
	if _, err := runtimesession.ParseID(strings.TrimSpace(args[0])); err == nil {
		return "", cli.CommandFailure(cmd, nameSessionJSON, "invalid_arguments",
			fmt.Errorf("%q is a runtime session ID, not a name: name the session with --runtime-session %s and give the name after it, or add --clear to remove its name", strings.TrimSpace(args[0]), strings.TrimSpace(args[0])))
	}
	return args[0], nil
}

// nameSessionSummary is the one line a person reads after name-session.
func nameSessionSummary(result nameSessionResult) string {
	switch {
	case result.Name == "" && result.Changed:
		return "removed name from " + result.RuntimeSessionID
	case result.Name == "":
		return result.RuntimeSessionID + " has no name"
	case result.Changed:
		return fmt.Sprintf("named %s %q", result.RuntimeSessionID, result.Name)
	default:
		return fmt.Sprintf("%s is already named %q", result.RuntimeSessionID, result.Name)
	}
}

// nameRuntimeSession gives the tmux session a runtime session ID names the
// name, cleaned; an empty one removes it. Only a pane that still carries that
// runtime session is touched.
func nameRuntimeSession(term nameTerminal, id runtimesession.ID, name string) (nameSessionResult, error) {
	result := nameSessionResult{RuntimeSessionID: string(id), Name: session.CleanName(name)}
	panes, err := runtimeSessionPanes(term, id)
	if err != nil {
		return result, err
	}
	pane := panes[0]
	if pane.Vars[session.NameVar] == result.Name {
		return result, nil
	}
	result.Changed = true
	return result, term.SetMetadata(pane.Ref, map[string]string{session.NameVar: result.Name})
}
