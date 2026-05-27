package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

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
		daemonExportEventsCmd.SetOut(oldOut)
	})
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
