package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

type fakeDaemonExportClient struct {
	health      ipc.Health
	calls       []int
	scopedCalls []string
	export      func(windowID int) (protocol.EventsExport, error)
}

func (f *fakeDaemonExportClient) ExportEvents(windowID int) (protocol.EventsExport, error) {
	f.calls = append(f.calls, windowID)
	if f.export == nil {
		return protocol.EventsExport{}, nil
	}
	return f.export(windowID)
}

func (f *fakeDaemonExportClient) ExportKittyWindowEvents(windowID int, socket string) (protocol.EventsExport, error) {
	f.scopedCalls = append(f.scopedCalls, fmt.Sprintf("%s:%d", socket, windowID))
	if f.export == nil {
		return protocol.EventsExport{}, nil
	}
	return f.export(windowID)
}

func (f *fakeDaemonExportClient) Health() ipc.Health {
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
	oldRuntimeLister := newDaemonExportRuntime
	// No test reads the operator's tmux server.
	newDaemonExportRuntime = func() ([]session.RunningSession, error) { return nil, nil }
	oldNow := daemonExportNow
	oldWindow := daemonExportEventsWindow
	oldCurrentWindow := daemonExportEventsCurrentWindow
	oldRuntime := daemonExportEventsRuntime
	// A test run typed inside a managed session must not select it.
	t.Setenv(runtimesession.EnvVar, "")
	daemonExportEventsRuntime = ""
	oldLast := daemonExportEventsLast
	oldOutput := daemonExportEventsOutput
	oldPretty := daemonExportEventsPretty
	oldSchema := daemonExportEventsSchema
	oldOut := daemonExportEventsCmd.OutOrStdout()

	t.Cleanup(func() {
		newDaemonExportClient = oldClient
		newDaemonExportKitty = oldKitty
		newDaemonExportRuntime = oldRuntimeLister
		daemonExportNow = oldNow
		daemonExportEventsWindow = oldWindow
		daemonExportEventsCurrentWindow = oldCurrentWindow
		daemonExportEventsRuntime = oldRuntime
		daemonExportEventsLast = oldLast
		daemonExportEventsOutput = oldOutput
		daemonExportEventsPretty = oldPretty
		daemonExportEventsSchema = oldSchema
		daemonExportEventsCmd.SetOut(oldOut)
	})
}

func findExportWindow(t *testing.T, payload protocol.ExportEventsPayload, id int) protocol.ExportWindow {
	t.Helper()
	for _, window := range payload.Windows {
		if window.WindowID == id {
			return window
		}
	}
	t.Fatalf("window %d missing from payload: %+v", id, payload.Windows)
	return protocol.ExportWindow{}
}

func TestDaemonExportEventsCommandRejectsUnsupportedSchema(t *testing.T) {
	resetDaemonExportTestState(t)
	daemonExportEventsSchema = 14

	err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want schema error")
	}
	if got, want := err.Error(), "--schema must be 8, 9, 10, 11, 12, or 13"; got != want {
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
	if got := err.Error(); got != "--current-window requires AMX_RUNTIME_SESSION_ID or KITTY_WINDOW_ID to be set" {
		t.Fatalf("RunE error = %q; want missing KITTY_WINDOW_ID error", got)
	}
}

func TestDaemonExportEventsCurrentWindowRequiresKittySocket(t *testing.T) {
	resetDaemonExportTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "7")
	t.Setenv("KITTY_LISTEN_ON", "")
	daemonExportEventsCurrentWindow = true
	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err == nil ||
		!strings.Contains(err.Error(), "requires a reusable KITTY_LISTEN_ON") {
		t.Fatalf("missing socket error = %v", err)
	}
}

func TestDaemonExportEventsCurrentWindowUsesSocketScopedSlot(t *testing.T) {
	resetDaemonExportTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "7")
	t.Setenv("KITTY_LISTEN_ON", "unix:/kitty-second")
	const alias = 1 << 32
	client := &fakeDaemonExportClient{
		health: ipc.Health{Alive: true},
		export: func(windowID int) (protocol.EventsExport, error) {
			if windowID != 7 {
				t.Fatalf("scoped export asked for raw window %d", windowID)
			}
			return protocol.EventsExport{
				EventsByWindow: map[int][]protocol.SessionEvent{alias: {{Event: "PermissionRequest"}}},
				KittySlots:     map[int]protocol.KittySlot{alias: {Socket: "unix:/kitty-second", WindowID: 7}},
			}, nil
		},
	}
	newDaemonExportClient = func() cli.DaemonExportClient { return client }
	newDaemonExportKitty = func() cli.KittyWindowLister {
		return fakeKittyWindowLister{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{
			ID: 5, Title: "second", Windows: []kitty.Window{{ID: 7}},
		}}}}}
	}
	daemonExportEventsCurrentWindow = true
	daemonExportEventsSchema = protocol.EventsExportSchemaVersion
	daemonExportEventsPretty = false
	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)
	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 0 || len(client.scopedCalls) != 1 || client.scopedCalls[0] != "unix:/kitty-second:7" {
		t.Fatalf("export calls bare=%v scoped=%v", client.calls, client.scopedCalls)
	}
	var payload protocol.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Windows) != 1 || payload.Windows[0].WindowID != alias ||
		len(payload.Windows[0].Events) != 1 || payload.Windows[0].Events[0].Event != "PermissionRequest" ||
		!payload.Windows[0].Live.Found {
		t.Fatalf("socket scoped export payload = %+v", payload.Windows)
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
				health: ipc.Health{Alive: tc.alive, PID: 1, Uptime: time.Second},
				export: func(windowID int) (protocol.EventsExport, error) {
					return protocol.EventsExport{}, tc.exportErr
				},
			}
			newDaemonExportClient = func() cli.DaemonExportClient { return client }
			newDaemonExportKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }
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
