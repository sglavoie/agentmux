package cmd

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// Retained session commands expose the same local --json contract. Keeping the
// audit explicit prevents a new leaf command from silently printing an
// unversioned ad-hoc object.
func TestSessionJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{
		sessionListCmd, sessionContinueCmd, sessionForkCmd,
		sessionForkWorktreeCmd, sessionHandoffCmd,
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

// Worktree commands share the retained command envelope. Keep every JSON leaf
// explicit so adding or removing a worktree surface requires a contract audit.
func TestWorktreeJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{
		worktreeListCmd, worktreeShowCmd, worktreeReopenCmd,
		worktreeShellCmd, worktreeEditCmd, worktreeRemoveCmd,
		worktreeForgetCmd,
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
