package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestDaemonExportEventsCommand_CurrentWindowResolvesKittyEnv(t *testing.T) {
	resetDaemonExportTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "77")

	client := &fakeDaemonExportClient{
		health: ipc.Health{Alive: true, PID: 7, Uptime: 7 * time.Second},
		export: func(windowID int) (protocol.EventsExport, error) {
			if windowID != 77 {
				return protocol.EventsExport{}, fmt.Errorf("windowID = %d; want 77", windowID)
			}
			return protocol.EventsExport{
				EventsByWindow: map[int][]protocol.SessionEvent{
					77: {{At: 7, Event: "Stop", Action: "stop", ResultIconState: "stop"}},
				},
			}, nil
		},
	}

	newDaemonExportClient = func() cli.DaemonExportClient { return client }
	newDaemonExportKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }
	daemonExportNow = func() time.Time { return time.Unix(700, 0) }
	daemonExportEventsWindow = 0
	daemonExportEventsCurrentWindow = true
	daemonExportEventsLast = 0
	daemonExportEventsOutput = ""
	daemonExportEventsPretty = false

	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)

	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	if len(client.calls) != 1 || client.calls[0] != 77 {
		t.Fatalf("ExportEvents calls = %v; want [77]", client.calls)
	}

	var payload protocol.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, out.String())
	}
	if len(payload.Windows) != 1 || payload.Windows[0].WindowID != 77 {
		t.Fatalf("payload windows = %+v; want one window_id=77", payload.Windows)
	}
}

// TestDaemonExportEventsCommand_OutputExpandsTilde pins the fix for the
// `--output` bug where a leading "~/" was treated as a literal path
// component. Users typing `amx daemon export-events --output ~/events.json`
// previously got a file named "~/events.json" under cwd instead of one in
// their home directory.
func TestDaemonExportEventsCommand_OutputExpandsTilde(t *testing.T) {
	resetDaemonExportTestState(t)

	// Point HOME at a temp dir so the test can assert on the expansion target
	// without touching the real home directory.
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	client := &fakeDaemonExportClient{
		health: ipc.Health{Alive: true, PID: 1, Uptime: time.Second},
		export: func(windowID int) (protocol.EventsExport, error) {
			return protocol.EventsExport{}, nil
		},
	}

	newDaemonExportClient = func() cli.DaemonExportClient { return client }
	newDaemonExportKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }
	daemonExportNow = func() time.Time { return time.Unix(100, 0) }
	daemonExportEventsWindow = 0
	daemonExportEventsCurrentWindow = false
	daemonExportEventsLast = 0
	daemonExportEventsOutput = "~/events.json"
	daemonExportEventsPretty = false

	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)

	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	// The file must land in the expanded home directory, not in cwd.
	expandedPath := filepath.Join(tempHome, "events.json")
	if _, err := os.Stat(expandedPath); err != nil {
		t.Fatalf("expected output at %s, stat err: %v", expandedPath, err)
	}

	// And there must NOT be a literal "~/events.json" file relative to cwd.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	literalPath := filepath.Join(cwd, "~", "events.json")
	if _, err := os.Stat(literalPath); err == nil {
		t.Fatalf("found literal tilde path at %s; tilde was not expanded", literalPath)
	}
}

func TestDaemonExportEventsCommand_LastTrimsPerWindowEvents(t *testing.T) {
	resetDaemonExportTestState(t)

	client := &fakeDaemonExportClient{
		health: ipc.Health{Alive: true, PID: 42, Uptime: 42 * time.Second},
		export: func(windowID int) (protocol.EventsExport, error) {
			if windowID != 0 {
				return protocol.EventsExport{}, fmt.Errorf("windowID = %d; want 0", windowID)
			}
			return protocol.EventsExport{
				EventsByWindow: map[int][]protocol.SessionEvent{
					42: {
						{At: 1, Event: "PreToolUse", Action: "active", ResultIconState: "bolt"},
						{At: 2, Event: "PostToolUse", Action: "active", ResultIconState: "bolt"},
						{At: 3, Event: "Stop", Action: "stop", ResultIconState: "stop"},
					},
				},
				WindowMetadata: map[int]protocol.WindowDebugMetadata{
					42: {
						Stats: protocol.WindowDebugStats{EventCount: 3, FirstEventAt: 1, LastEventAt: 3},
						Durable: protocol.WindowDebugDurable{
							SessionTitle: "W:api",
						},
					},
				},
			}, nil
		},
	}

	newDaemonExportClient = func() cli.DaemonExportClient { return client }
	newDaemonExportKitty = func() cli.KittyWindowLister { return fakeKittyWindowLister{} }
	daemonExportNow = func() time.Time { return time.Unix(100, 0) }
	daemonExportEventsWindow = 0
	daemonExportEventsCurrentWindow = false
	daemonExportEventsLast = 2
	daemonExportEventsOutput = ""
	daemonExportEventsPretty = false

	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)

	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	var payload protocol.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, out.String())
	}

	window42 := findExportWindow(t, payload, 42)
	if window42.Stats.EventCount != 3 {
		t.Fatalf("window 42 stats.event_count = %d; want 3", window42.Stats.EventCount)
	}
	if len(window42.Events) != 2 {
		t.Fatalf("window 42 events len = %d; want 2", len(window42.Events))
	}
	if window42.Events[0].At != 2 || window42.Events[1].At != 3 {
		t.Fatalf("window 42 events = %+v; want trailing events at 2 and 3", window42.Events)
	}
}
