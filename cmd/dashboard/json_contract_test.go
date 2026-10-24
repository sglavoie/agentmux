package dashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// open-dashboard is the one dashboard-family leaf with --json; it shares the
// retained command envelope. The surfaces themselves (the dashboard, overview,
// past-sessions, the pickers) are interactive and have no JSON answer. Keep
// the enveloped leaves explicit so adding one is a decision.
func TestDashboardJSONCommandSurfaceIsVersioned(t *testing.T) {
	for _, command := range []*cobra.Command{openDashboardCmd} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	for _, command := range []*cobra.Command{overviewCmd, pastSessionsCmd} {
		if command.Flags().Lookup("json") != nil {
			t.Errorf("%s is interactive yet declares --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
	// The payload's field names are the contract the app and scripts read.
	encoded, err := json.Marshal(openDashboardData{WindowID: 4, TabID: 2, Opened: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"window_id":4,"tab_id":2,"opened":true}`; got != want {
		t.Fatalf("open-dashboard data = %s; want %s", got, want)
	}
	if encoded, _ = json.Marshal(openDashboardData{Client: "/dev/ttys004"}); !strings.Contains(string(encoded), `"client":"/dev/ttys004"`) {
		t.Fatalf("open-dashboard data lacks client: %s", encoded)
	}
}
