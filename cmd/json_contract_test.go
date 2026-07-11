package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// Every project-workbench command that supports machine output must expose the
// same local --json contract. Keeping the audit explicit prevents a new leaf
// command from silently printing an unversioned ad-hoc object.
func TestWorkbenchJSONCommandSurfaceIsVersioned(t *testing.T) {
	commands := []*cobra.Command{
		workspaceListCmd, workspaceShowCmd, workspaceDiscoverCmd, workspaceAdoptCmd,
		sessionListCmd, sessionLaunchCmd, sessionFocusCmd, sessionContinueCmd,
		sessionForkCmd, sessionForkWorktreeCmd, sessionHandoffCmd,
		planListCmd, planLinkCmd, planUnlinkCmd, searchCmd, indexStatusCmd, indexRebuildCmd,
	}
	for _, command := range commands {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", JSONSchemaVersion)
	}
}
