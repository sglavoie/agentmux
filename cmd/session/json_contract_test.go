package session

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// Retained session commands expose the same local --json contract. Keeping the
// audit explicit prevents a new leaf command from silently printing an
// unversioned ad-hoc object.
func TestSessionJSONCommandSurfaceIsVersioned(t *testing.T) {
	t.Parallel()

	commands := []*cobra.Command{
		sessionListCmd, sessionSearchCmd, sessionShowCmd, sessionContinueCmd, sessionForkCmd,
		sessionForkWorktreeCmd, sessionHandoffCmd, muteToggleCmd, nameSessionCmd, backendStatusCmd, backendStopCmd, closeCmd, sendCmd,
		attachCmd,
	}
	// activity and changes are built by constructors, so they are found by
	// name under the family they were registered in.
	for _, name := range []string{"activity", "changes"} {
		command, _, err := sessionCmd.Find([]string{name})
		if err != nil || command == sessionCmd {
			t.Fatalf("session %s is not registered: %v", name, err)
		}
		commands = append(commands, command)
	}
	for _, command := range commands {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
}
