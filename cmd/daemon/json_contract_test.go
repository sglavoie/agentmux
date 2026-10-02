package daemon

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

// `daemon status` shares the retained command envelope. `export-events` is
// deliberately absent: its export carries its own schema version and is not
// wrapped. Keep the enveloped leaves explicit so adding one is a decision.
func TestDaemonJSONCommandSurfaceIsVersioned(t *testing.T) {
	for _, command := range []*cobra.Command{daemonStatusCmd} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
}

func TestDaemonStatusJSON(t *testing.T) {
	cases := []struct {
		name   string
		health ipc.Health
		want   string
	}{
		{
			name:   "not running",
			health: ipc.Health{},
			want:   `{"schema_version":1,"data":{"alive":false,"socket":"/tmp/amx.sock","ready":false}}` + "\n",
		},
		{
			name: "running degraded",
			health: ipc.Health{
				Alive: true, PID: 42, Uptime: 90 * time.Second, DegradedReason: "initial snapshot timed out",
				Notification: &protocol.NotificationCapability{Enabled: true, Backend: "alerter", Reason: "not_found"},
			},
			want: `{"schema_version":1,"data":{"alive":true,"socket":"/tmp/amx.sock","pid":42,"uptime_seconds":90,"ready":false,` +
				`"degraded_reason":"initial snapshot timed out","notification":{"enabled":true,"available":false,"interactive":false,"backend":"alerter","reason":"not_found"}}}` + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDaemonStatusTestState(t)
			health := tc.health
			newDaemonExportClient = func() cli.DaemonExportClient { return &fakeDaemonExportClient{health: health} }
			daemonSocketPath = func() string { return "/tmp/amx.sock" }
			if err := daemonStatusCmd.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = daemonStatusCmd.Flags().Set("json", "false") })

			var out bytes.Buffer
			daemonStatusCmd.SetOut(&out)
			if err := daemonStatusCmd.RunE(daemonStatusCmd, nil); err != nil {
				t.Fatalf("RunE: %v", err)
			}
			if got := out.String(); got != tc.want {
				t.Fatalf("output =\n%s\nwant\n%s", got, tc.want)
			}
			if !json.Valid(out.Bytes()) {
				t.Fatal("output is not valid JSON")
			}
		})
	}
}
