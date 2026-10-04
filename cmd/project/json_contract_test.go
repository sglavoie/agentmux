package project

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// Project commands share the retained command envelope. Keep every JSON leaf
// explicit so adding or removing a project surface requires a contract audit.
func TestProjectJSONCommandSurfaceIsVersioned(t *testing.T) {
	for _, command := range []*cobra.Command{projectListCmd, projectLaunchCmd} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
}
