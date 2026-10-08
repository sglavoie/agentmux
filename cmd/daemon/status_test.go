package daemon

import (
	"bytes"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func resetDaemonStatusTestState(t *testing.T) {
	t.Helper()
	oldClient := newDaemonExportClient
	oldSocketPath := daemonSocketPath
	oldOut := daemonStatusCmd.OutOrStdout()
	t.Cleanup(func() {
		newDaemonExportClient = oldClient
		daemonSocketPath = oldSocketPath
		daemonStatusCmd.SetOut(oldOut)
	})
}

func TestDaemonStatusCommandWritesToCommandOutput(t *testing.T) {
	build := buildinfo.Info{Version: "9.9.9", Revision: "0123456789abcdef", GoVersion: "go1.99"}
	notification := protocol.NotificationCapability{
		Enabled: true, Available: true, Interactive: true, Backend: "alerter",
		Executable: "/opt/homebrew/bin/alerter", Version: "26.5",
	}
	snoozedUntil := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		health ipc.Health
		want   string
	}{
		{
			name:   "running ready with build",
			health: ipc.Health{Alive: true, Ready: true, Build: &build, Notification: &notification},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: true\n" +
				"build: " + build.String() + "\n" +
				"notifications: ready (alerter 26.5 at /opt/homebrew/bin/alerter)\n" +
				"companion: unknown (restart older daemon)\n",
		},
		{
			name: "snoozed until a time",
			health: ipc.Health{Alive: true, Ready: true, Notification: &protocol.NotificationCapability{
				Enabled: true, Available: true, Interactive: true, Backend: "alerter", SnoozedUntil: snoozedUntil.Unix(),
			}},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: true\n" +
				"notifications: ready (alerter); snoozed until " + snoozedUntil.Local().Format("2006-01-02 15:04") + "\n" +
				"companion: unknown (restart older daemon)\n",
		},
		{
			name: "snoozed indefinitely",
			health: ipc.Health{Alive: true, Ready: true, Notification: &protocol.NotificationCapability{
				Enabled: true, Available: true, Interactive: true, Backend: "alerter", SnoozeIndefinite: true,
			}},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: true\n" +
				"notifications: ready (alerter); snoozed until turned off\n" +
				"companion: unknown (restart older daemon)\n",
		},
		{
			name:   "running degraded",
			health: ipc.Health{Alive: true, Ready: false, DegradedReason: "initial snapshot timed out"},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: false (initial snapshot timed out)\n" +
				"notifications: unknown (restart older daemon)\n" +
				"companion: unknown (restart older daemon)\n",
		},
		{
			name: "notifications unavailable without degrading daemon",
			health: ipc.Health{Alive: true, Ready: true, Notification: &protocol.NotificationCapability{
				Enabled: true, Backend: "alerter", Reason: "not_found",
			}},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: true\n" +
				"notifications: unavailable (alerter; not_found)\n" +
				"companion: unknown (restart older daemon)\n",
		},
		{
			name: "companion subscribers",
			health: ipc.Health{Alive: true, Ready: true, Notification: &notification, Companion: &protocol.CompanionHealth{
				Subscribers:                  2,
				NotificationBackendConnected: true,
				Clients: []protocol.CompanionClient{
					{SubscriberID: "sub-1", Client: "Agent Mux.app", ClientVersion: "1.0", NotificationBackend: true},
					{SubscriberID: "sub-2"},
				},
			}},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: true\n" +
				"notifications: ready (alerter 26.5 at /opt/homebrew/bin/alerter)\n" +
				"companion: 2 subscribers (Agent Mux.app 1.0, sub-2); notification backend connected\n",
		},
		{
			name:   "companion idle",
			health: ipc.Health{Alive: true, Ready: true, Notification: &notification, Companion: &protocol.CompanionHealth{}},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: true\n" +
				"notifications: ready (alerter 26.5 at /opt/homebrew/bin/alerter)\n" +
				"companion: no subscribers\n",
		},
		{
			name: "not running",
			want: "daemon: not running (/tmp/amx.sock)\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDaemonStatusTestState(t)
			health := tc.health
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

func TestDaemonStatusRendersASLCapability(t *testing.T) {
	age := int64(12)
	quietAge := int64(1802)
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
			name: "connected with a current snapshot that has not changed",
			asl: &aslbridge.Status{
				Mode: string(aslbridge.ModeRequired), Command: "asl", Executable: "/usr/local/bin/asl",
				Available: true, MutationsEnabled: true, Protocol: 2, Version: "0.2.0",
				SnapshotAgeSeconds: &quietAge,
			},
			want: "daemon: running (/tmp/amx.sock)\nready: true\nnotifications: unknown (restart older daemon)\n" +
				"asl: ready (mode required at /usr/local/bin/asl; asl 0.2.0 protocol 2; snapshot unchanged for 30m2s)\n",
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
			// The companion line follows the ASL line; this daemon reports no
			// companion block, so it reads as unknown.
			want := tc.want + "companion: unknown (restart older daemon)\n"
			if got := out.String(); got != want {
				t.Fatalf("output = %q; want %q", got, want)
			}
		})
	}
}
