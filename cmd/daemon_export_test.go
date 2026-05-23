package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func TestDaemonExportEventsCommand_AllWindowsUsesSchemaV3Payload(t *testing.T) {
	resetDaemonExportTestState(t)

	now := time.Unix(1_700_000_100, 0)
	client := &fakeDaemonExportClient{
		health: daemon.Health{Alive: true, PID: 4321, Uptime: 2 * time.Minute},
		export: func(windowID int) (daemon.EventsExport, error) {
			if windowID != 0 {
				return daemon.EventsExport{}, fmt.Errorf("unexpected filter %d", windowID)
			}
			return daemon.EventsExport{
				EventsByWindow: map[int][]daemon.SessionEvent{
					42: {
						{At: 1_700_000_000, Event: "PreToolUse", Action: "active", Source: "new", ResultIconState: "bolt"},
					},
					77: {
						{At: 1_700_000_005, Event: "Stop", Action: "stop", ResultIconState: "stop"},
					},
				},
				WindowMetadata: map[int]daemon.WindowDebugMetadata{
					42: {
						Stats: daemon.WindowDebugStats{EventCount: 1, FirstEventAt: 1_700_000_000, LastEventAt: 1_700_000_000},
						Durable: daemon.WindowDebugDurable{
							SessionTitle:    "W:api",
							Profile:         "work",
							Harness:         "claude",
							ActivityAt:      1_700_000_000,
							LastAction:      "active",
							LastActiveEvent: "PreToolUse",
						},
					},
					77: {
						Stats: daemon.WindowDebugStats{EventCount: 1, FirstEventAt: 1_700_000_005, LastEventAt: 1_700_000_005},
						Durable: daemon.WindowDebugDurable{
							SessionTitle: "P:dotfiles",
							Profile:      "personal",
							Harness:      "copilot",
							ActivityAt:   1_700_000_005,
							LastAction:   "stop",
						},
					},
					88: {
						Stats: daemon.WindowDebugStats{EventCount: 4, FirstEventAt: 1_699_999_900, LastEventAt: 1_699_999_999},
						Durable: daemon.WindowDebugDurable{
							SessionTitle: "W:stale",
							Profile:      "work",
							Harness:      "claude",
							ActivityAt:   1_699_999_999,
							LastAction:   "active",
						},
					},
				},
			}, nil
		},
	}

	newDaemonExportClient = func() daemonExportClient { return client }
	newDaemonExportKitty = func() kittyWindowLister {
		return fakeKittyWindowLister{
			windows: []kitty.OSWindow{{
				ID: 900,
				Tabs: []kitty.Tab{{
					ID:    91,
					Title: "⚡W:api",
					Windows: []kitty.Window{{
						ID:    42,
						Title: "api window",
						CWD:   "/work/api",
						UserVars: kitty.UserVars{
							Profile:      "work",
							Harness:      "claude",
							Project:      "api",
							AMXDirectory: "/work/api",
						},
					}},
				}},
			}},
		}
	}
	daemonExportNow = func() time.Time { return now }
	daemonExportEventsWindow = 0
	daemonExportEventsCurrentWindow = false
	daemonExportEventsLast = 0
	daemonExportEventsOutput = ""
	daemonExportEventsPretty = false

	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)

	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	var payload daemon.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, out.String())
	}

	if payload.SchemaVersion != daemon.EventsExportSchemaVersion {
		t.Fatalf("schema_version = %d; want %d", payload.SchemaVersion, daemon.EventsExportSchemaVersion)
	}
	if payload.ExportedAt != now.Unix() {
		t.Fatalf("exported_at = %d; want %d", payload.ExportedAt, now.Unix())
	}
	if payload.Daemon.PID != 4321 || payload.Daemon.UptimeSeconds != 120 {
		t.Fatalf("daemon = %+v; want pid=4321 uptime_seconds=120", payload.Daemon)
	}
	if len(payload.Windows) != 3 {
		t.Fatalf("len(windows) = %d; want 3 (%+v)", len(payload.Windows), payload.Windows)
	}

	window42 := findExportWindow(t, payload, 42)
	if len(window42.Events) != 1 || window42.Events[0].ResultIconState != "bolt" {
		t.Fatalf("window 42 events = %+v; want one bolt event", window42.Events)
	}
	if window42.Events[0].Source != "new" {
		t.Fatalf("window 42 source = %q; want new", window42.Events[0].Source)
	}
	if window42.Durable.SessionTitle != "W:api" {
		t.Fatalf("window 42 durable.session_title = %q; want W:api", window42.Durable.SessionTitle)
	}
	if !window42.Live.Found || window42.Live.TabID != 91 || window42.Live.OSWindowID != 900 || window42.Live.TabTitle != "⚡W:api" ||
		window42.Live.BaseTitle != "W:api" || window42.Live.IconState != "bolt" || window42.Live.WindowTitle != "api window" ||
		window42.Live.Project != "api" || window42.Live.Profile != "work" || window42.Live.Harness != "claude" ||
		window42.Live.Directory != "/work/api" {
		t.Fatalf("window 42 live = %+v", window42.Live)
	}

	window77 := findExportWindow(t, payload, 77)
	if window77.Live.Found {
		t.Fatalf("window 77 live.found = true; want false")
	}

	window88 := findExportWindow(t, payload, 88)
	if window88.Durable.SessionTitle != "W:stale" {
		t.Fatalf("window 88 durable.session_title = %q; want W:stale", window88.Durable.SessionTitle)
	}
	if window88.Live.Found {
		t.Fatalf("window 88 live.found = true; want false")
	}
	if len(window88.Events) != 0 {
		t.Fatalf("window 88 events = %+v; want empty", window88.Events)
	}
}

func TestDaemonExportEventsCommand_SingleWindowUsesFilteredExportShape(t *testing.T) {
	resetDaemonExportTestState(t)

	client := &fakeDaemonExportClient{
		health: daemon.Health{Alive: true, PID: 99, Uptime: 5 * time.Second},
		export: func(windowID int) (daemon.EventsExport, error) {
			if windowID != 42 {
				return daemon.EventsExport{}, fmt.Errorf("windowID = %d; want 42", windowID)
			}
			return daemon.EventsExport{
				EventsByWindow: map[int][]daemon.SessionEvent{
					42: {{At: 1, Event: "PreToolUse", Action: "active", ResultIconState: "bolt"}},
				},
				WindowMetadata: map[int]daemon.WindowDebugMetadata{
					42: {
						Stats: daemon.WindowDebugStats{EventCount: 1, FirstEventAt: 1, LastEventAt: 1},
						Durable: daemon.WindowDebugDurable{
							SessionTitle: "W:api",
							Profile:      "work",
							Harness:      "claude",
						},
					},
				},
			}, nil
		},
	}

	newDaemonExportClient = func() daemonExportClient { return client }
	newDaemonExportKitty = func() kittyWindowLister {
		return fakeKittyWindowLister{
			windows: []kitty.OSWindow{{
				ID: 5,
				Tabs: []kitty.Tab{{
					ID:    6,
					Title: "⚡W:api",
					Windows: []kitty.Window{{
						ID:    42,
						Title: "api window",
						CWD:   "/work/api",
						UserVars: kitty.UserVars{
							Profile:      "work",
							Harness:      "claude",
							Project:      "api",
							AMXDirectory: "/work/api",
						},
					}},
				}},
			}},
		}
	}
	daemonExportNow = func() time.Time { return time.Unix(100, 0) }
	daemonExportEventsWindow = 42
	daemonExportEventsCurrentWindow = false
	daemonExportEventsLast = 0
	daemonExportEventsOutput = ""
	daemonExportEventsPretty = false

	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)

	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	if len(client.calls) != 1 || client.calls[0] != 42 {
		t.Fatalf("ExportEvents calls = %v; want [42]", client.calls)
	}

	var payload daemon.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, out.String())
	}
	if len(payload.Windows) != 1 {
		t.Fatalf("len(windows) = %d; want 1 (%+v)", len(payload.Windows), payload.Windows)
	}
	if window := findExportWindow(t, payload, 42); len(window.Events) != 1 || window.Durable.SessionTitle != "W:api" {
		t.Fatalf("window 42 = %+v; want one event and durable metadata", window)
	}
}

func TestDaemonExportEventsCommand_SingleWindowKeepsEmptyEventsArray(t *testing.T) {
	resetDaemonExportTestState(t)

	client := &fakeDaemonExportClient{
		health: daemon.Health{Alive: true, PID: 1, Uptime: time.Second},
		export: func(windowID int) (daemon.EventsExport, error) {
			if windowID != 42 {
				return daemon.EventsExport{}, fmt.Errorf("windowID = %d; want 42", windowID)
			}
			return daemon.EventsExport{}, nil
		},
	}

	newDaemonExportClient = func() daemonExportClient { return client }
	newDaemonExportKitty = func() kittyWindowLister {
		return fakeKittyWindowLister{
			windows: []kitty.OSWindow{{
				ID: 5,
				Tabs: []kitty.Tab{{
					ID:    6,
					Title: "W:api",
					Windows: []kitty.Window{{
						ID:    42,
						Title: "api window",
						CWD:   "/work/api",
					}},
				}},
			}},
		}
	}
	daemonExportNow = func() time.Time { return time.Unix(100, 0) }
	daemonExportEventsWindow = 42
	daemonExportEventsCurrentWindow = false
	daemonExportEventsLast = 0
	daemonExportEventsOutput = ""
	daemonExportEventsPretty = false

	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)

	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	var payload daemon.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, out.String())
	}
	window42 := findExportWindow(t, payload, 42)
	if len(window42.Events) != 0 {
		t.Fatalf("window 42 events len = %d; want 0", len(window42.Events))
	}
	if !window42.Live.Found {
		t.Fatalf("window 42 live.found = false; want true")
	}
}

func TestDaemonExportEventsCommand_CurrentWindowResolvesKittyEnv(t *testing.T) {
	resetDaemonExportTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "77")

	client := &fakeDaemonExportClient{
		health: daemon.Health{Alive: true, PID: 7, Uptime: 7 * time.Second},
		export: func(windowID int) (daemon.EventsExport, error) {
			if windowID != 77 {
				return daemon.EventsExport{}, fmt.Errorf("windowID = %d; want 77", windowID)
			}
			return daemon.EventsExport{
				EventsByWindow: map[int][]daemon.SessionEvent{
					77: {{At: 7, Event: "Stop", Action: "stop", ResultIconState: "stop"}},
				},
			}, nil
		},
	}

	newDaemonExportClient = func() daemonExportClient { return client }
	newDaemonExportKitty = func() kittyWindowLister { return fakeKittyWindowLister{} }
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

	var payload daemon.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, out.String())
	}
	if len(payload.Windows) != 1 || payload.Windows[0].WindowID != 77 {
		t.Fatalf("payload windows = %+v; want one window_id=77", payload.Windows)
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
		health: daemon.Health{Alive: true, PID: 1, Uptime: time.Second},
		export: func(windowID int) (daemon.EventsExport, error) {
			return daemon.EventsExport{}, nil
		},
	}

	newDaemonExportClient = func() daemonExportClient { return client }
	newDaemonExportKitty = func() kittyWindowLister { return fakeKittyWindowLister{} }
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
		health: daemon.Health{Alive: true, PID: 42, Uptime: 42 * time.Second},
		export: func(windowID int) (daemon.EventsExport, error) {
			if windowID != 0 {
				return daemon.EventsExport{}, fmt.Errorf("windowID = %d; want 0", windowID)
			}
			return daemon.EventsExport{
				EventsByWindow: map[int][]daemon.SessionEvent{
					42: {
						{At: 1, Event: "PreToolUse", Action: "active", ResultIconState: "bolt"},
						{At: 2, Event: "PostToolUse", Action: "active", ResultIconState: "bolt"},
						{At: 3, Event: "Stop", Action: "stop", ResultIconState: "stop"},
					},
				},
				WindowMetadata: map[int]daemon.WindowDebugMetadata{
					42: {
						Stats: daemon.WindowDebugStats{EventCount: 3, FirstEventAt: 1, LastEventAt: 3},
						Durable: daemon.WindowDebugDurable{
							SessionTitle: "W:api",
						},
					},
				},
			}, nil
		},
	}

	newDaemonExportClient = func() daemonExportClient { return client }
	newDaemonExportKitty = func() kittyWindowLister { return fakeKittyWindowLister{} }
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

	var payload daemon.ExportEventsPayload
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
