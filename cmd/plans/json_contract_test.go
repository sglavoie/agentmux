package plans

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// Plan commands share the retained command envelope. Keep every JSON leaf
// explicit so adding or removing a plan surface requires a contract audit.
func TestPlanJSONCommandSurfaceIsVersioned(t *testing.T) {
	status, _, err := planCmd.Find([]string{"status"})
	if err != nil || status == planCmd {
		t.Fatalf("plans status is not registered: %v", err)
	}
	for _, command := range []*cobra.Command{planRunCmd, status} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
}
