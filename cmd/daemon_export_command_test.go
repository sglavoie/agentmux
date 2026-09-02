package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestDaemonExportEventsCommand_AllWindowsUsesSchemaV10Payload(t *testing.T) {
	resetDaemonExportTestState(t)

	now := time.Unix(1_700_000_100, 0)
	daemonBuild := buildinfo.Info{Version: "9.9.9", Revision: "abc123", GoVersion: "go1.99", BinaryHash: "feedface"}
	metrics := protocol.MetricsSnapshot{Accepted: 7, QueueFull: 0, QueueLastWaitNs: 1500}
	client := &fakeDaemonExportClient{
		health: daemon.Health{
			Alive: true, PID: 4321, Uptime: 2 * time.Minute, Ready: true,
			Build: &daemonBuild, Metrics: &metrics,
		},
		export: func(windowID int) (protocol.EventsExport, error) {
			if windowID != 0 {
				return protocol.EventsExport{}, fmt.Errorf("unexpected filter %d", windowID)
			}
			return protocol.EventsExport{
				EventsByWindow: map[int][]protocol.SessionEvent{
					42: {
						{
							At: 1_700_000_000, Event: "PreToolUse", Action: "active", Source: "new", ResultIconState: "bolt",
							DaemonSeq: 11, ReceivedAtNs: 1_700_000_000_000_000_001,
							SourceKind: "harness", SourceKey: "sess-1", SourceSeq: 3, RouteDecision: "source", OriginWindowID: 91,
						},
						{At: 1_700_000_001, Event: "ScreenPromptProbe", Action: "elicitation", Source: "codex_screen", PromptKind: "edit_approval", ResultIconState: "question"},
					},
					77: {
						{At: 1_700_000_005, Event: "Stop", Action: "stop", ResultIconState: "stop"},
					},
				},
				WindowMetadata: map[int]protocol.WindowDebugMetadata{
					42: {
						Stats: protocol.WindowDebugStats{
							EventCount: 2, FirstEventAt: 1_700_000_000, LastEventAt: 1_700_000_001,
							SuppressionReasons: map[string]int{"guard_blocked": 2},
						},
						Durable: protocol.WindowDebugDurable{
							SessionTitle:    "W:api",
							Profile:         "work",
							Harness:         "codex",
							ActivityAt:      1_700_000_001,
							LastAction:      "elicitation",
							LastActiveEvent: "PreToolUse",
						},
						Renderer: protocol.WindowDebugRenderer{
							DesiredIcon: "question", StateSet: true, Looping: true, LoopingSet: true, RenderFailures: 3,
							LastRenderError: "set title: unavailable", KittyListenOn: "unix:/tmp/kitty-42",
						},
						Shadow: protocol.WindowDebugShadow{
							EffectiveIcon: "question", EffectiveGen: 4,
							Facts: []protocol.SourceFactSnapshot{{
								Kind: protocol.SourceHarness, Key: "sess-1", Action: "elicitation", Icon: "question", Seq: 4,
								PipeMode: true, Acked: true, AckedAt: 1_700_000_001, OriginWindowID: 91,
								HarnessProcessHarness: "codex", HarnessProcessPID: 4321,
								HarnessProcessStartToken: "Mon Jul 13 10:11:12 2026",
							}},
						},
						Watchdog: protocol.WindowDebugWatchdog{
							LastEvent: "SourceExpired", LastReason: "stale active fact", LastAt: 1_700_000_001,
							SourceExpiries: 2, StaleSweeps: 1, Reconciles: 3,
						},
					},
					77: {
						Stats: protocol.WindowDebugStats{EventCount: 1, FirstEventAt: 1_700_000_005, LastEventAt: 1_700_000_005},
						Durable: protocol.WindowDebugDurable{
							SessionTitle: "P:dotfiles",
							Profile:      "personal",
							Harness:      "copilot",
							ActivityAt:   1_700_000_005,
							LastAction:   "stop",
						},
					},
					88: {
						Stats: protocol.WindowDebugStats{EventCount: 4, FirstEventAt: 1_699_999_900, LastEventAt: 1_699_999_999},
						Durable: protocol.WindowDebugDurable{
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

	var payload protocol.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, out.String())
	}

	if payload.SchemaVersion != protocol.EventsExportSchemaVersion {
		t.Fatalf("schema_version = %d; want %d", payload.SchemaVersion, protocol.EventsExportSchemaVersion)
	}
	if payload.SchemaVersion != 13 {
		t.Fatalf("schema_version = %d; want 13", payload.SchemaVersion)
	}
	if payload.ExportedAt != now.Unix() {
		t.Fatalf("exported_at = %d; want %d", payload.ExportedAt, now.Unix())
	}
	if payload.Daemon.PID != 4321 || payload.Daemon.UptimeSeconds != 120 || !payload.Daemon.Ready {
		t.Fatalf("daemon = %+v; want pid=4321 uptime_seconds=120 ready", payload.Daemon)
	}
	if payload.Daemon.Build == nil || payload.Daemon.Build.BinaryHash != "feedface" {
		t.Fatalf("daemon build = %+v; want binary hash feedface", payload.Daemon.Build)
	}
	if payload.Daemon.Metrics == nil || payload.Daemon.Metrics.Accepted != 7 || payload.Daemon.Metrics.QueueLastWaitNs != 1500 {
		t.Fatalf("daemon metrics = %+v; want accepted=7 queue_last_wait_ns=1500", payload.Daemon.Metrics)
	}
	if payload.CLIBuild == nil || payload.CLIBuild.Version == "" {
		t.Fatalf("cli_build = %+v; want the exporting CLI's identity", payload.CLIBuild)
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
	if window42.Renderer.DesiredIcon != "question" || !window42.Renderer.StateSet || !window42.Renderer.Looping || !window42.Renderer.LoopingSet ||
		window42.Renderer.RenderFailures != 3 || window42.Renderer.LastRenderError != "set title: unavailable" ||
		window42.Renderer.KittyListenOn != "unix:/tmp/kitty-42" {
		t.Fatalf("window 42 renderer = %+v", window42.Renderer)
	}
	if got := window42.Stats.SuppressionReasons["guard_blocked"]; got != 2 {
		t.Fatalf("window 42 suppression reasons = %+v; want guard_blocked=2", window42.Stats.SuppressionReasons)
	}
	if window42.Events[0].DaemonSeq != 11 || window42.Events[0].ReceivedAtNs != 1_700_000_000_000_000_001 ||
		window42.Events[0].SourceKind != "harness" || window42.Events[0].SourceKey != "sess-1" ||
		window42.Events[0].SourceSeq != 3 || window42.Events[0].RouteDecision != "source" ||
		window42.Events[0].OriginWindowID != 91 {
		t.Fatalf("window 42 v8 event fields = %+v", window42.Events[0])
	}
	if len(window42.Shadow.Facts) != 1 || window42.Shadow.Facts[0].Kind != protocol.SourceHarness ||
		window42.Shadow.Facts[0].Icon != "question" || !window42.Shadow.Facts[0].PipeMode ||
		!window42.Shadow.Facts[0].Acked || window42.Shadow.Facts[0].AckedAt != 1_700_000_001 ||
		window42.Shadow.Facts[0].OriginWindowID != 91 || window42.Shadow.Facts[0].HarnessProcessHarness != "codex" ||
		window42.Shadow.Facts[0].HarnessProcessPID != 4321 || window42.Shadow.Facts[0].HarnessProcessStartToken != "Mon Jul 13 10:11:12 2026" {
		t.Fatalf("window 42 shadow facts = %+v", window42.Shadow.Facts)
	}
	if window42.Watchdog.LastEvent != "SourceExpired" || window42.Watchdog.SourceExpiries != 2 ||
		window42.Watchdog.Reconciles != 3 {
		t.Fatalf("window 42 watchdog = %+v", window42.Watchdog)
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
		export: func(windowID int) (protocol.EventsExport, error) {
			if windowID != 42 {
				return protocol.EventsExport{}, fmt.Errorf("windowID = %d; want 42", windowID)
			}
			return protocol.EventsExport{
				EventsByWindow: map[int][]protocol.SessionEvent{
					42: {{At: 1, Event: "PreToolUse", Action: "active", ResultIconState: "bolt"}},
				},
				WindowMetadata: map[int]protocol.WindowDebugMetadata{
					42: {
						Stats: protocol.WindowDebugStats{EventCount: 1, FirstEventAt: 1, LastEventAt: 1},
						Durable: protocol.WindowDebugDurable{
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

	var payload protocol.ExportEventsPayload
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
		export: func(windowID int) (protocol.EventsExport, error) {
			if windowID != 42 {
				return protocol.EventsExport{}, fmt.Errorf("windowID = %d; want 42", windowID)
			}
			return protocol.EventsExport{}, nil
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

	var payload protocol.ExportEventsPayload
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

func TestDaemonExportEventsCommandSchema8ProjectsCausalFields(t *testing.T) {
	resetDaemonExportTestState(t)
	client := &fakeDaemonExportClient{
		health: daemon.Health{Alive: true},
		export: func(int) (protocol.EventsExport, error) {
			return protocol.EventsExport{
				EventsByWindow: map[int][]protocol.SessionEvent{42: {{
					At: 1, Event: "Stop", Action: "stop", ResultIconState: "stop",
					TraceID: "trace-v9", DecisionReason: "accepted", RenderGeneration: 2,
				}}},
				WindowMetadata: map[int]protocol.WindowDebugMetadata{42: {
					Renderer: protocol.WindowDebugRenderer{
						Generations: []protocol.RenderGenerationTrace{{Generation: 2, TraceIDs: []string{"trace-v9"}, Stage: "rendered"}},
					},
					Shadow: protocol.WindowDebugShadow{Facts: []protocol.SourceFactSnapshot{{
						Kind: protocol.SourceHarness, Key: "claude:s1", Icon: "bolt",
						HarnessProcessHarness: "claude", HarnessProcessPID: 4321,
						HarnessProcessStartToken: "Mon Jul 13 10:11:12 2026",
					}}},
				}},
			}, nil
		},
	}
	newDaemonExportClient = func() daemonExportClient { return client }
	newDaemonExportKitty = func() kittyWindowLister { return fakeKittyWindowLister{} }
	daemonExportEventsSchema = protocol.EventsExportSchemaV8
	daemonExportEventsPretty = false
	daemonExportEventsWindow = 42
	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)

	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if got := int(raw["schema_version"].(float64)); got != 8 {
		t.Fatalf("schema_version = %d; want 8", got)
	}
	window := raw["windows"].([]any)[0].(map[string]any)
	if _, ok := window["renderer"].(map[string]any)["generations"]; ok {
		t.Fatalf("schema 8 leaked renderer generations: %v", window)
	}
	event := window["events"].([]any)[0].(map[string]any)
	if _, ok := event["trace_id"]; ok {
		t.Fatalf("schema 8 leaked causal event fields: %v", event)
	}
	fact := window["shadow"].(map[string]any)["facts"].([]any)[0].(map[string]any)
	for _, key := range []string{"ph", "pp", "pt"} {
		if _, ok := fact[key]; ok {
			t.Fatalf("schema 8 leaked process field %q: %v", key, fact)
		}
	}
}

func TestDaemonExportEventsCommandSchema9PreservesCausalFieldsWithoutProcessIdentity(t *testing.T) {
	resetDaemonExportTestState(t)
	client := &fakeDaemonExportClient{
		health: daemon.Health{Alive: true},
		export: func(int) (protocol.EventsExport, error) {
			return protocol.EventsExport{
				EventsByWindow: map[int][]protocol.SessionEvent{42: {{
					At: 1, Event: "Stop", Action: "stop", ResultIconState: "stop",
					TraceID: "trace-v9", DecisionReason: "accepted", RenderGeneration: 2,
				}}},
				WindowMetadata: map[int]protocol.WindowDebugMetadata{42: {
					Renderer: protocol.WindowDebugRenderer{
						Generations: []protocol.RenderGenerationTrace{{Generation: 2, TraceIDs: []string{"trace-v9"}, Stage: "rendered"}},
					},
					Shadow: protocol.WindowDebugShadow{Facts: []protocol.SourceFactSnapshot{{
						Kind: protocol.SourceHarness, Key: "claude:s1", Icon: "bolt", CausalOrder: 1,
						HarnessProcessHarness: "claude", HarnessProcessPID: 4321,
						HarnessProcessStartToken: "Mon Jul 13 10:11:12 2026",
					}}},
				}},
			}, nil
		},
	}
	newDaemonExportClient = func() daemonExportClient { return client }
	newDaemonExportKitty = func() kittyWindowLister { return fakeKittyWindowLister{} }
	daemonExportEventsSchema = protocol.EventsExportSchemaV9
	daemonExportEventsPretty = false
	daemonExportEventsWindow = 42
	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)

	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if got := int(raw["schema_version"].(float64)); got != 9 {
		t.Fatalf("schema_version = %d; want 9", got)
	}
	window := raw["windows"].([]any)[0].(map[string]any)
	if _, ok := window["renderer"].(map[string]any)["generations"]; !ok {
		t.Fatalf("schema 9 lost renderer generations: %v", window)
	}
	event := window["events"].([]any)[0].(map[string]any)
	if event["trace_id"] != "trace-v9" {
		t.Fatalf("schema 9 lost causal event fields: %v", event)
	}
	fact := window["shadow"].(map[string]any)["facts"].([]any)[0].(map[string]any)
	for _, key := range []string{"ph", "pp", "pt"} {
		if _, ok := fact[key]; ok {
			t.Fatalf("schema 9 leaked process field %q: %v", key, fact)
		}
	}
}
