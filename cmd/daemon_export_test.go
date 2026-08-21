package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
)

type fakeDaemonExportClient struct {
	health daemon.Health
	calls  []int
	export func(windowID int) (daemon.EventsExport, error)
}

func (f *fakeDaemonExportClient) ExportEvents(windowID int) (daemon.EventsExport, error) {
	f.calls = append(f.calls, windowID)
	if f.export == nil {
		return daemon.EventsExport{}, nil
	}
	return f.export(windowID)
}

func (f *fakeDaemonExportClient) Health() daemon.Health {
	return f.health
}

type fakeKittyWindowLister struct {
	windows []kitty.OSWindow
	err     error
}

func (f fakeKittyWindowLister) ListWindows() ([]kitty.OSWindow, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.windows, nil
}

type fakeDaemonLifecycleClient struct {
	sendResp     daemon.Response
	sendErr      error
	sendCalls    []daemon.Request
	restartErr   error
	restartCalls []string
}

func (f *fakeDaemonLifecycleClient) Send(req daemon.Request) (daemon.Response, error) {
	f.sendCalls = append(f.sendCalls, req)
	return f.sendResp, f.sendErr
}

func (f *fakeDaemonLifecycleClient) RestartDetached(amxBin string) error {
	f.restartCalls = append(f.restartCalls, amxBin)
	return f.restartErr
}

func resetDaemonLifecycleTestState(t *testing.T) {
	t.Helper()
	oldClient := newDaemonLifecycleClient
	oldStopOut := daemonStopCmd.OutOrStdout()
	oldRestartOut := daemonRestartCmd.OutOrStdout()
	t.Cleanup(func() {
		newDaemonLifecycleClient = oldClient
		daemonStopCmd.SetOut(oldStopOut)
		daemonRestartCmd.SetOut(oldRestartOut)
	})
}

func resetDaemonExportTestState(t *testing.T) {
	t.Helper()
	oldClient := newDaemonExportClient
	oldKitty := newDaemonExportKitty
	oldNow := daemonExportNow
	oldWindow := daemonExportEventsWindow
	oldCurrentWindow := daemonExportEventsCurrentWindow
	oldLast := daemonExportEventsLast
	oldOutput := daemonExportEventsOutput
	oldPretty := daemonExportEventsPretty
	oldSchema := daemonExportEventsSchema
	oldOut := daemonExportEventsCmd.OutOrStdout()

	t.Cleanup(func() {
		newDaemonExportClient = oldClient
		newDaemonExportKitty = oldKitty
		daemonExportNow = oldNow
		daemonExportEventsWindow = oldWindow
		daemonExportEventsCurrentWindow = oldCurrentWindow
		daemonExportEventsLast = oldLast
		daemonExportEventsOutput = oldOutput
		daemonExportEventsPretty = oldPretty
		daemonExportEventsSchema = oldSchema
		daemonExportEventsCmd.SetOut(oldOut)
	})
}

func TestDaemonStopCommandWritesToCommandOutput(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{
		sendResp: daemon.Response{OK: true},
	}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }

	var out bytes.Buffer
	daemonStopCmd.SetOut(&out)

	if err := daemonStopCmd.RunE(daemonStopCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), "daemon stopped\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
	if len(client.sendCalls) != 1 || client.sendCalls[0].Type != daemon.TypeShutdown {
		t.Fatalf("Send calls = %+v; want one shutdown request", client.sendCalls)
	}
}

func TestDaemonRestartCommandWritesToCommandOutput(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }

	var out bytes.Buffer
	daemonRestartCmd.SetOut(&out)

	if err := daemonRestartCmd.RunE(daemonRestartCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), "daemon restarted\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
	if len(client.restartCalls) != 1 || client.restartCalls[0] == "" {
		t.Fatalf("RestartDetached calls = %+v; want one non-empty binary path", client.restartCalls)
	}
}

func findExportWindow(t *testing.T, payload daemon.ExportEventsPayload, id int) daemon.ExportWindow {
	t.Helper()
	for _, window := range payload.Windows {
		if window.WindowID == id {
			return window
		}
	}
	t.Fatalf("window %d missing from payload: %+v", id, payload.Windows)
	return daemon.ExportWindow{}
}

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
	notification := daemon.NotificationCapability{
		Enabled: true, Available: true, Interactive: true, Backend: "alerter",
		Executable: "/opt/homebrew/bin/alerter", Version: "26.5",
	}
	cases := []struct {
		name   string
		health daemon.Health
		want   string
	}{
		{
			name:   "running ready with build",
			health: daemon.Health{Alive: true, Ready: true, Build: &build, Notification: &notification},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: true\n" +
				"build: " + build.String() + "\n" +
				"notifications: ready (alerter 26.5 at /opt/homebrew/bin/alerter)\n",
		},
		{
			name:   "running degraded",
			health: daemon.Health{Alive: true, Ready: false, DegradedReason: "initial snapshot timed out"},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: false (initial snapshot timed out)\n" +
				"notifications: unknown (restart older daemon)\n",
		},
		{
			name: "notifications unavailable without degrading daemon",
			health: daemon.Health{Alive: true, Ready: true, Notification: &daemon.NotificationCapability{
				Enabled: true, Backend: "alerter", Reason: "not_found",
			}},
			want: "daemon: running (/tmp/amx.sock)\n" +
				"ready: true\n" +
				"notifications: unavailable (alerter; not_found)\n",
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
			newDaemonExportClient = func() daemonExportClient {
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

func TestDaemonExportEventsCommandRejectsPositionalArgs(t *testing.T) {
	err := daemonExportEventsCmd.Args(daemonExportEventsCmd, []string{"events.json"})
	if err == nil {
		t.Fatal("Args error = nil; want positional argument error")
	}
	if got := err.Error(); !strings.Contains(got, `unknown command "events.json" for "amx daemon export-events"`) {
		t.Fatalf("Args error = %q; want unknown positional argument error", got)
	}
}

func TestDaemonExportEventsCommandRejectsUnsupportedSchema(t *testing.T) {
	resetDaemonExportTestState(t)
	daemonExportEventsSchema = 13

	err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want schema error")
	}
	if got, want := err.Error(), "--schema must be 8, 9, 10, 11, or 12"; got != want {
		t.Fatalf("RunE error = %q; want %q", got, want)
	}
}

func TestDaemonExportEventsCommand_CurrentWindowRejectsExplicitWindow(t *testing.T) {
	resetDaemonExportTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "77")

	daemonExportEventsWindow = 42
	daemonExportEventsCurrentWindow = true
	daemonExportEventsLast = 0
	daemonExportEventsOutput = ""
	daemonExportEventsPretty = false

	err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want conflict error")
	}
	if got := err.Error(); got != "--window and --current-window cannot be used together" {
		t.Fatalf("RunE error = %q; want conflict error", got)
	}
}

func TestDaemonExportEventsCommand_CurrentWindowRequiresKittyWindowID(t *testing.T) {
	resetDaemonExportTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "")

	daemonExportEventsWindow = 0
	daemonExportEventsCurrentWindow = true
	daemonExportEventsLast = 0
	daemonExportEventsOutput = ""
	daemonExportEventsPretty = false

	err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want KITTY_WINDOW_ID error")
	}
	if got := err.Error(); got != "--current-window requires KITTY_WINDOW_ID to be set" {
		t.Fatalf("RunE error = %q; want missing KITTY_WINDOW_ID error", got)
	}
}

// TestDaemonExportEventsCommand_ErrorMessageReflectsDaemonState pins the fix
// that "daemon not running" is reported only when the daemon is actually
// unreachable. Previously any ExportEvents error (including a live daemon
// returning !resp.OK) was incorrectly wrapped as "daemon not running".
func TestDaemonExportEventsCommand_CurrentWindowRejectsInvalidKittyWindowID(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{name: "non-numeric value", env: "abc"},
		{name: "zero value", env: "0"},
		{name: "negative value", env: "-3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDaemonExportTestState(t)
			t.Setenv("KITTY_WINDOW_ID", tc.env)

			daemonExportEventsWindow = 0
			daemonExportEventsCurrentWindow = true
			daemonExportEventsLast = 0
			daemonExportEventsOutput = ""
			daemonExportEventsPretty = false

			err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil)
			if err == nil {
				t.Fatal("RunE error = nil; want invalid KITTY_WINDOW_ID error")
			}
			if !strings.Contains(err.Error(), "--current-window requires a valid KITTY_WINDOW_ID") {
				t.Fatalf("RunE error = %q; want valid-KITTY_WINDOW_ID error", err.Error())
			}
		})
	}
}

func TestDaemonExportEventsCommand_LastRejectsNegative(t *testing.T) {
	resetDaemonExportTestState(t)
	daemonExportEventsWindow = 0
	daemonExportEventsCurrentWindow = false
	daemonExportEventsLast = -1
	daemonExportEventsOutput = ""
	daemonExportEventsPretty = false

	err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want --last error")
	}
	if got := err.Error(); got != "--last must be >= 0" {
		t.Fatalf("RunE error = %q; want %q", got, "--last must be >= 0")
	}
}

func TestDaemonExportEventsCommand_ErrorMessageReflectsDaemonState(t *testing.T) {
	cases := []struct {
		name        string
		alive       bool
		exportErr   error
		wantPrefix  string
		wantContain string
	}{
		{
			name:        "dead daemon reports not running",
			alive:       false,
			exportErr:   fmt.Errorf("dial unix: connect: no such file or directory"),
			wantPrefix:  "daemon not running:",
			wantContain: "dial unix",
		},
		{
			name:        "live daemon reports export failure",
			alive:       true,
			exportErr:   fmt.Errorf("daemon error: snapshot busy"),
			wantPrefix:  "export events:",
			wantContain: "snapshot busy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDaemonExportTestState(t)

			client := &fakeDaemonExportClient{
				health: daemon.Health{Alive: tc.alive, PID: 1, Uptime: time.Second},
				export: func(windowID int) (daemon.EventsExport, error) {
					return daemon.EventsExport{}, tc.exportErr
				},
			}
			newDaemonExportClient = func() daemonExportClient { return client }
			newDaemonExportKitty = func() kittyWindowLister { return fakeKittyWindowLister{} }
			daemonExportNow = func() time.Time { return time.Unix(0, 0) }
			daemonExportEventsWindow = 0
			daemonExportEventsCurrentWindow = false
			daemonExportEventsLast = 0
			daemonExportEventsOutput = ""
			daemonExportEventsPretty = false

			var out bytes.Buffer
			daemonExportEventsCmd.SetOut(&out)

			err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil)
			if err == nil {
				t.Fatalf("RunE error = nil; want %q", tc.wantPrefix)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, tc.wantPrefix) {
				t.Fatalf("RunE error = %q; want prefix %q", msg, tc.wantPrefix)
			}
			if !strings.Contains(msg, tc.wantContain) {
				t.Fatalf("RunE error = %q; want to contain %q", msg, tc.wantContain)
			}
		})
	}
}
