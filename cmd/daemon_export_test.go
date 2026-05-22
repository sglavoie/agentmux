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
	oldOutput := daemonExportEventsOutput
	oldPretty := daemonExportEventsPretty
	oldOut := daemonExportEventsCmd.OutOrStdout()

	t.Cleanup(func() {
		newDaemonExportClient = oldClient
		newDaemonExportKitty = oldKitty
		daemonExportNow = oldNow
		daemonExportEventsWindow = oldWindow
		daemonExportEventsOutput = oldOutput
		daemonExportEventsPretty = oldPretty
		daemonExportEventsCmd.SetOut(oldOut)
	})
}

func TestDaemonExportEventsCommand_AllWindowsIncludesWindowMeta(t *testing.T) {
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
						{At: 1_700_000_000, Event: "PreToolUse", Action: "active", ResultIconState: "bolt"},
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
	if payload.DaemonPID != 4321 || payload.DaemonUptimeSeconds != 120 {
		t.Fatalf("health = pid:%d uptime:%d; want 4321/120", payload.DaemonPID, payload.DaemonUptimeSeconds)
	}
	if got := payload.Windows["42"][0].ResultIconState; got != "bolt" {
		t.Fatalf("windows[42][0].result_icon_state = %q; want bolt", got)
	}
	if got := payload.WindowMeta["42"].Durable.SessionTitle; got != "W:api" {
		t.Fatalf("window_meta[42].durable.session_title = %q; want W:api", got)
	}
	if got := payload.WindowMeta["88"].Durable.SessionTitle; got != "W:stale" {
		t.Fatalf("window_meta[88].durable.session_title = %q; want W:stale", got)
	}
	live := payload.WindowMeta["42"].Live
	if !live.Found || live.TabID != 91 || live.OSWindowID != 900 || live.TabTitle != "⚡W:api" ||
		live.BaseTitle != "W:api" || live.IconState != "bolt" || live.WindowTitle != "api window" ||
		live.Project != "api" || live.Profile != "work" || live.Harness != "claude" ||
		live.Directory != "/work/api" {
		t.Fatalf("window_meta[42].live = %+v", live)
	}
	if payload.WindowMeta["77"].Live.Found {
		t.Fatalf("window_meta[77].live.found = true; want false")
	}
	if payload.WindowMeta["88"].Live.Found {
		t.Fatalf("window_meta[88].live.found = true; want false")
	}
	if _, ok := payload.Windows["88"]; ok {
		t.Fatalf("windows unexpectedly includes metadata-only key 88: %+v", payload.Windows["88"])
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
	if len(payload.Windows) != 1 || len(payload.WindowMeta) != 1 {
		t.Fatalf("payload window counts = %d/%d; want 1/1", len(payload.Windows), len(payload.WindowMeta))
	}
	if _, ok := payload.Windows["42"]; !ok {
		t.Fatalf("windows missing requested key 42: %+v", payload.Windows)
	}
	if _, ok := payload.WindowMeta["42"]; !ok {
		t.Fatalf("window_meta missing requested key 42: %+v", payload.WindowMeta)
	}
}

func TestDaemonExportEventsCommand_SingleWindowKeepsEmptyWindowsArray(t *testing.T) {
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
	events, ok := payload.Windows["42"]
	if !ok {
		t.Fatalf("windows missing requested key 42: %+v", payload.Windows)
	}
	if len(events) != 0 {
		t.Fatalf("windows[42] len = %d; want 0", len(events))
	}
	if !payload.WindowMeta["42"].Live.Found {
		t.Fatalf("window_meta[42].live.found = false; want true")
	}
}
