package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// TestDaemonStateAcceptanceDocSchemaV8READMEFactKeys pins the schema-v8
// export-events example in README.md to the actual wire shape of
// daemon.SourceFactSnapshot. WindowDebugShadow.Facts serializes that type
// directly, so shadow.facts[] entries can only use its compact JSON tags
// (k/y/i/pm/...), never descriptive spellings like kind/key/icon. The prior
// round-trip test in this file exercised the Go type but never guarded the raw
// key spelling in the documentation snippet, letting the example drift to keys
// that amx daemon export-events cannot emit.
func TestDaemonStateAcceptanceDocSchemaV8READMEFactKeys(t *testing.T) {
	data, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatalf("ReadFile(../README.md): %v", err)
	}

	facts := extractSchemaV8ExampleFacts(t, string(data))
	if len(facts) == 0 {
		t.Fatalf("README schema-v8 example has no shadow.facts entries")
	}

	valid := sourceFactSnapshotWireTags()
	for i, fact := range facts {
		for key := range fact {
			if !valid[key] {
				t.Fatalf("README schema-v8 facts[%d] uses key %q, which is not a SourceFactSnapshot wire tag (valid: %v)", i, key, sortedKeys(valid))
			}
		}
	}

	// The example must show the compact ownership keys and a pipe-mode fact so
	// operators see the real wire spelling and the presence-encoded pm flag.
	var sawPipeMode, sawOrigin, sawAckedAt bool
	for _, fact := range facts {
		for _, need := range []string{"k", "y", "i"} {
			if _, ok := fact[need]; !ok {
				t.Fatalf("README schema-v8 fact object missing compact key %q: %v", need, fact)
			}
		}
		var snap daemon.SourceFactSnapshot
		blob, err := json.Marshal(fact)
		if err != nil {
			t.Fatalf("re-marshal example fact: %v", err)
		}
		if err := json.Unmarshal(blob, &snap); err != nil {
			t.Fatalf("decode example fact into SourceFactSnapshot: %v", err)
		}
		if raw, ok := fact["pm"]; ok {
			var pm bool
			if err := json.Unmarshal(raw, &pm); err != nil {
				t.Fatalf("decode pm value %s: %v", raw, err)
			}
			if pm != snap.PipeMode {
				t.Fatalf("example pm=%v does not round-trip through SourceFactSnapshot (%v)", pm, snap.PipeMode)
			}
			if pm {
				sawPipeMode = true
			}
		}
		if snap.OriginWindowID > 0 {
			sawOrigin = true
		}
		if snap.Acked && snap.AckedAt > 0 {
			sawAckedAt = true
		}
	}
	if !sawPipeMode {
		t.Fatalf("README schema-v8 example must include a pipe-mode fact with pm:true")
	}
	if !sawOrigin || !sawAckedAt {
		t.Fatalf("README schema-v8 example must include ow origin and an acknowledged aa timestamp")
	}
}

func TestCrossHarnessDiagnosticDocumentation(t *testing.T) {
	readmeData, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatalf("ReadFile(../README.md): %v", err)
	}
	readme := string(readmeData)
	for _, want := range []string{
		"--harness=<claude|codex|copilot>",
		"`amx_state` schema v3",
		"`owner_mismatch_prompt`",
		"`owner_mismatch_burst`",
		"`unknown_producer_fact`",
		"`acknowledged_fact_pressure`",
		`"route_decision": "owner"`,
		`"origin_window_id": 91`,
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README missing cross-harness diagnostic contract %q", want)
		}
	}

	acceptanceData, err := os.ReadFile("../docs/daemon_state_acceptance.md")
	if err != nil {
		t.Fatalf("ReadFile(../docs/daemon_state_acceptance.md): %v", err)
	}
	acceptance := string(acceptanceData)
	for _, want := range []string{
		"## Cross-harness route diagnostics (session 6)",
		"19-event synthetic trace",
		"`owner_mismatch_prompt`",
		"`owner_mismatch_burst`",
		"`unknown_producer_fact`",
		"`acknowledged_fact_pressure`",
	} {
		if !strings.Contains(acceptance, want) {
			t.Errorf("acceptance documentation missing %q", want)
		}
	}
}

// extractSchemaV8ExampleFacts returns the shadow.facts array from the schema-v8
// export example decoded as a list of raw JSON objects.
func extractSchemaV8ExampleFacts(t *testing.T, readme string) []map[string]json.RawMessage {
	t.Helper()

	marker := "\"schema_version\": 8"
	start := strings.Index(readme, marker)
	if start < 0 {
		t.Fatalf("README missing schema_version 8 example")
	}
	rest := readme[start:]
	idx := strings.Index(rest, "\"facts\"")
	if idx < 0 {
		t.Fatalf("schema-v8 example missing a facts array")
	}
	rest = rest[idx:]
	open := strings.IndexByte(rest, '[')
	if open < 0 {
		t.Fatalf("facts array has no opening bracket")
	}
	depth := 0
	end := -1
	for i := open; i < len(rest); i++ {
		switch rest[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("facts array has no closing bracket")
	}

	var facts []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rest[open:end+1]), &facts); err != nil {
		t.Fatalf("decode facts array %q: %v", rest[open:end+1], err)
	}
	return facts
}

// sourceFactSnapshotWireTags returns the set of JSON tag names emitted by
// daemon.SourceFactSnapshot.
func sourceFactSnapshotWireTags() map[string]bool {
	tags := map[string]bool{}
	rt := reflect.TypeOf(daemon.SourceFactSnapshot{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" {
			tags[name] = true
		}
	}
	return tags
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestDaemonExportEventsCommand_AllWindowsUsesSchemaV8Payload(t *testing.T) {
	resetDaemonExportTestState(t)

	now := time.Unix(1_700_000_100, 0)
	daemonBuild := buildinfo.Info{Version: "9.9.9", Revision: "abc123", GoVersion: "go1.99", BinaryHash: "feedface"}
	metrics := daemon.MetricsSnapshot{Accepted: 7, QueueFull: 0, QueueLastWaitNs: 1500}
	client := &fakeDaemonExportClient{
		health: daemon.Health{
			Alive: true, PID: 4321, Uptime: 2 * time.Minute, Ready: true,
			Build: &daemonBuild, Metrics: &metrics,
		},
		export: func(windowID int) (daemon.EventsExport, error) {
			if windowID != 0 {
				return daemon.EventsExport{}, fmt.Errorf("unexpected filter %d", windowID)
			}
			return daemon.EventsExport{
				EventsByWindow: map[int][]daemon.SessionEvent{
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
				WindowMetadata: map[int]daemon.WindowDebugMetadata{
					42: {
						Stats: daemon.WindowDebugStats{
							EventCount: 2, FirstEventAt: 1_700_000_000, LastEventAt: 1_700_000_001,
							SuppressionReasons: map[string]int{"guard_blocked": 2},
						},
						Durable: daemon.WindowDebugDurable{
							SessionTitle:    "W:api",
							Profile:         "work",
							Harness:         "codex",
							ActivityAt:      1_700_000_001,
							LastAction:      "elicitation",
							LastActiveEvent: "PreToolUse",
						},
						Renderer: daemon.WindowDebugRenderer{
							DesiredIcon: "question", StateSet: true, Looping: true, LoopingSet: true, RenderFailures: 3,
							LastRenderError: "set title: unavailable", KittyListenOn: "unix:/tmp/kitty-42",
						},
						Shadow: daemon.WindowDebugShadow{
							EffectiveIcon: "question", EffectiveGen: 4,
							Facts: []daemon.SourceFactSnapshot{{
								Kind: daemon.SourceHarness, Key: "sess-1", Action: "elicitation", Icon: "question", Seq: 4,
								PipeMode: true, Acked: true, AckedAt: 1_700_000_001, OriginWindowID: 91,
							}},
						},
						Watchdog: daemon.WindowDebugWatchdog{
							LastEvent: "SourceExpired", LastReason: "stale active fact", LastAt: 1_700_000_001,
							SourceExpiries: 2, StaleSweeps: 1, Reconciles: 3,
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
	if payload.SchemaVersion != 8 {
		t.Fatalf("schema_version = %d; want 8", payload.SchemaVersion)
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
	if len(window42.Shadow.Facts) != 1 || window42.Shadow.Facts[0].Kind != daemon.SourceHarness ||
		window42.Shadow.Facts[0].Icon != "question" || !window42.Shadow.Facts[0].PipeMode ||
		!window42.Shadow.Facts[0].Acked || window42.Shadow.Facts[0].AckedAt != 1_700_000_001 ||
		window42.Shadow.Facts[0].OriginWindowID != 91 {
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
