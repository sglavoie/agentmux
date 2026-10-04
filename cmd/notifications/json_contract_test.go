package notifications

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// Every notifications leaf that answers a machine caller declares --json and
// shares the retained schema-v1 envelope. `test` is deliberately absent: it
// drives a visible notification for a human and has no machine answer.
func TestNotificationsJSONCommandSurfaceIsVersioned(t *testing.T) {
	for _, command := range []*cobra.Command{notificationsSnoozeCmd} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
}
