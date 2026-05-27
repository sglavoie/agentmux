package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestDaemonExportEventsCommand_AllWindowsUsesSchemaV4Payload(t *testing.T) {
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
						{At: 1_700_000_001, Event: "ScreenPromptProbe", Action: "elicitation", Source: "codex_screen", PromptKind: "edit_approval", ResultIconState: "question"},
					},
					77: {
						{At: 1_700_000_005, Event: "Stop", Action: "stop", ResultIconState: "stop"},
					},
				},
				WindowMetadata: map[int]daemon.WindowDebugMetadata{
					42: {
						Stats: daemon.WindowDebugStats{EventCount: 2, FirstEventAt: 1_700_000_000, LastEventAt: 1_700_000_001},
						Durable: daemon.WindowDebugDurable{
							SessionTitle:    "W:api",
							Profile:         "work",
							Harness:         "codex",
							ActivityAt:      1_700_000_001,
							LastAction:      "elicitation",
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
							Harness:      "codex",
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
							Harness:      "codex",
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
	if payload.SchemaVersion != 4 {
		t.Fatalf("schema_version = %d; want 4", payload.SchemaVersion)
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
	if len(window42.Events) != 2 || window42.Events[0].ResultIconState != "bolt" {
		t.Fatalf("window 42 events = %+v; want bolt event then prompt probe", window42.Events)
	}
	if window42.Events[0].Source != "new" {
		t.Fatalf("window 42 source = %q; want new", window42.Events[0].Source)
	}
	if window42.Events[1].Event != "ScreenPromptProbe" || window42.Events[1].Source != "codex_screen" ||
		window42.Events[1].PromptKind != "edit_approval" || window42.Events[1].ResultIconState != "question" {
		t.Fatalf("window 42 prompt probe event = %+v; want ScreenPromptProbe/codex_screen/edit_approval/question", window42.Events[1])
	}
	if window42.Durable.SessionTitle != "W:api" {
		t.Fatalf("window 42 durable.session_title = %q; want W:api", window42.Durable.SessionTitle)
	}
	if !window42.Live.Found || window42.Live.TabID != 91 || window42.Live.OSWindowID != 900 || window42.Live.TabTitle != "⚡W:api" ||
		window42.Live.BaseTitle != "W:api" || window42.Live.IconState != "bolt" || window42.Live.WindowTitle != "api window" ||
		window42.Live.Project != "api" || window42.Live.Profile != "work" || window42.Live.Harness != "codex" ||
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
