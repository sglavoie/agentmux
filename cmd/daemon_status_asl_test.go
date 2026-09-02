package cmd

import (
	"bytes"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

func TestDaemonStatusRendersASLCapability(t *testing.T) {
	age := int64(12)
	cases := []struct {
		name string
		asl  *aslbridge.Status
		want string
	}{
		{
			name: "omitted by a daemon without the integration",
			asl:  nil,
			want: "daemon: running (/tmp/amx.sock)\nready: true\nnotifications: unknown (restart older daemon)\n",
		},
		{
			name: "explicitly disabled",
			asl:  &aslbridge.Status{Mode: string(aslbridge.ModeDisabled), Reason: "disabled by defaults.asl.enabled"},
			want: "daemon: running (/tmp/amx.sock)\nready: true\nnotifications: unknown (restart older daemon)\n" +
				"asl: disabled (mode disabled; disabled by defaults.asl.enabled)\n",
		},
		{
			name: "connected with fresh data",
			asl: &aslbridge.Status{
				Mode: string(aslbridge.ModeAuto), Command: "asl", Executable: "/usr/local/bin/asl",
				Available: true, MutationsEnabled: true, Protocol: 1, Version: "1.2.3",
			},
			want: "daemon: running (/tmp/amx.sock)\nready: true\nnotifications: unknown (restart older daemon)\n" +
				"asl: ready (mode auto at /usr/local/bin/asl; asl 1.2.3 protocol 1)\n",
		},
		{
			name: "connected but serving stale data",
			asl: &aslbridge.Status{
				Mode: string(aslbridge.ModeRequired), Command: "asl", Available: true,
				SnapshotAgeSeconds: &age, SnapshotStale: true, RestartCount: 2, Reason: "bridge exited",
			},
			want: "daemon: running (/tmp/amx.sock)\nready: true\nnotifications: unknown (restart older daemon)\n" +
				"asl: read-only (mode required via asl; snapshot 12s old (stale); restarts 2; bridge exited)\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDaemonStatusTestState(t)
			health := ipc.Health{Alive: true, Ready: true, ASL: tc.asl}
			newDaemonExportClient = func() cli.DaemonExportClient {
				return &fakeDaemonExportClient{health: health}
			}
			daemonSocketPath = func() string { return "/tmp/amx.sock" }

			var out bytes.Buffer
			daemonStatusCmd.SetOut(&out)
			if err := daemonStatusCmd.RunE(daemonStatusCmd, nil); err != nil {
				t.Fatalf("RunE: %v", err)
			}
			if got := out.String(); got != tc.want {
				t.Fatalf("output = %q; want %q", got, tc.want)
			}
		})
	}
}
