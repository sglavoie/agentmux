package worktree

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// Worktree commands share the retained command envelope. Keep every JSON leaf
// explicit so adding or removing a worktree surface requires a contract audit.
func TestWorktreeJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{
		worktreeListCmd, worktreeShowCmd, worktreeReopenCmd,
		worktreeShellCmd, worktreeEditCmd, worktreeRemoveCmd,
		worktreeForgetCmd, worktreePruneCmd,
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
