package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
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
			assertDaemonStatusExit(t, daemonStatusCmd.RunE(daemonStatusCmd, nil), tc.health.Alive)
			if got := out.String(); got != tc.want {
				t.Fatalf("output =\n%s\nwant\n%s", got, tc.want)
			}
			if !json.Valid(out.Bytes()) {
				t.Fatal("output is not valid JSON")
			}
		})
	}
}

// `daemon stop` and `daemon restart` answer a script with the envelope too;
// `daemon start` is absent because it blocks until shutdown and has no answer.
func TestDaemonLifecycleJSON(t *testing.T) {
	for _, command := range []*cobra.Command{daemonStopCmd, daemonRestartCmd} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	run := func(t *testing.T, command *cobra.Command) (string, error) {
		t.Helper()
		if err := command.Flags().Set("json", "true"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Flags().Set("json", "false") })
		var out bytes.Buffer
		command.SetOut(&out)
		err := command.RunE(command, nil)
		return out.String(), err
	}

	t.Run("stop", func(t *testing.T) {
		resetDaemonLifecycleTestState(t)
		newDaemonLifecycleClient = func() daemonLifecycleClient {
			return &fakeDaemonLifecycleClient{sendResp: protocol.Response{OK: true}}
		}
		out, err := run(t, daemonStopCmd)
		if err != nil || out != `{"schema_version":1,"data":{"stopped":true}}`+"\n" {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})
	t.Run("stop with no daemon", func(t *testing.T) {
		resetDaemonLifecycleTestState(t)
		newDaemonLifecycleClient = func() daemonLifecycleClient {
			return &fakeDaemonLifecycleClient{sendErr: errors.New("dial unix: no such file")}
		}
		out, err := run(t, daemonStopCmd)
		var exit *cli.ExitError
		if !errors.As(err, &exit) || !strings.Contains(out, `"code":"daemon_not_running"`) {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})
	t.Run("restart", func(t *testing.T) {
		resetDaemonLifecycleTestState(t)
		newDaemonLifecycleClient = func() daemonLifecycleClient {
			return &fakeDaemonLifecycleClient{healths: []ipc.Health{{Alive: true, PID: 77, DegradedReason: "initial snapshot timed out"}}}
		}
		out, err := run(t, daemonRestartCmd)
		if err != nil || out != `{"schema_version":1,"data":{"pid":77,"ready":false,"degraded_reason":"initial snapshot timed out"}}`+"\n" {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})
	t.Run("restart that never answers", func(t *testing.T) {
		resetDaemonLifecycleTestState(t)
		newDaemonLifecycleClient = func() daemonLifecycleClient {
			return &fakeDaemonLifecycleClient{healths: []ipc.Health{{}}}
		}
		out, err := run(t, daemonRestartCmd)
		var exit *cli.ExitError
		if !errors.As(err, &exit) || !strings.Contains(out, `"code":"restart_timeout"`) {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})
}
