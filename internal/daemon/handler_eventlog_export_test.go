package daemon

import (
	"encoding/json"
	"testing"
)

func TestProjectExportEventsPayloadV8RemovesSchemaV9CausalFields(t *testing.T) {
	payload := ExportEventsPayload{
		SchemaVersion: EventsExportSchemaVersion,
		Windows: []ExportWindow{{
			WindowID: 7,
			Renderer: WindowDebugRenderer{
				DesiredIcon: "bolt", QueuedGen: 3,
				Generations: []RenderGenerationTrace{{Generation: 3, TraceIDs: []string{"trace-1"}, Stage: "rendered"}},
			},
			Events: []SessionEvent{{
				At: 1, Event: "PreToolUse", Action: "active", ResultIconState: "bolt",
				DaemonSeq: 2, OriginWindowID: 8, TraceID: "trace-1", RequestID: "request-1",
				DecisionReason: DecisionReasonAccepted, RenderGeneration: 3, RenderOutcome: "rendered",
				NotificationOutcome: "sent", ProbeTraceIDs: []string{"probe-1"},
			}},
			Shadow: WindowDebugShadow{Facts: []SourceFactSnapshot{{
				Kind: SourceHarness, Key: "claude:s1", Action: "active", Icon: "bolt",
				CausalOrder: 2, OriginWindowID: 8, PipeMode: true,
				HarnessProcessHarness: "claude", HarnessProcessPID: 4321,
				HarnessProcessStartToken: "Mon Jul 13 10:11:12 2026",
			}}},
		}},
	}

	projected, err := ProjectExportEventsPayload(payload, EventsExportSchemaV8)
	if err != nil {
		t.Fatalf("ProjectExportEventsPayload: %v", err)
	}
	data, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := int(raw["schema_version"].(float64)); got != EventsExportSchemaV8 {
		t.Fatalf("schema_version = %d; want 8", got)
	}
	window := raw["windows"].([]any)[0].(map[string]any)
	renderer := window["renderer"].(map[string]any)
	if _, ok := renderer["generations"]; ok {
		t.Fatalf("schema v8 renderer leaked generations: %v", renderer)
	}
	event := window["events"].([]any)[0].(map[string]any)
	for _, key := range []string{"trace_id", "request_id", "decision_reason", "render_generation", "notification_outcome", "probe_trace_ids"} {
		if _, ok := event[key]; ok {
			t.Fatalf("schema v8 event leaked %q: %v", key, event)
		}
	}
	for _, key := range []string{"daemon_seq", "origin_window_id", "result_icon_state"} {
		if _, ok := event[key]; !ok {
			t.Fatalf("schema v8 event lost prior field %q: %v", key, event)
		}
	}
	fact := window["shadow"].(map[string]any)["facts"].([]any)[0].(map[string]any)
	for _, key := range []string{"ph", "pp", "pt"} {
		if _, ok := fact[key]; ok {
			t.Fatalf("schema v8 fact leaked process field %q: %v", key, fact)
		}
	}
	for _, key := range []string{"k", "y", "o", "ow", "pm"} {
		if _, ok := fact[key]; !ok {
			t.Fatalf("schema v8 fact lost prior field %q: %v", key, fact)
		}
	}
}

func TestProjectExportEventsPayloadV9PreservesCausalShapeWithoutProcessFields(t *testing.T) {
	payload := ExportEventsPayload{
		Windows: []ExportWindow{{
			WindowID: 7,
			Renderer: WindowDebugRenderer{
				Generations: []RenderGenerationTrace{{Generation: 3, TraceIDs: []string{"trace-1"}, Stage: "rendered"}},
			},
			Shadow: WindowDebugShadow{Facts: []SourceFactSnapshot{{
				Kind: SourceHarness, Key: "claude:s1", Icon: "bolt", CausalOrder: 2,
				OriginWindowID: 8, PipeMode: true, HarnessProcessHarness: "claude",
				HarnessProcessPID: 4321, HarnessProcessStartToken: "Mon Jul 13 10:11:12 2026",
			}}},
			Events: []SessionEvent{{
				At: 1, Event: "PreToolUse", Action: "active", ResultIconState: "bolt",
				TraceID: "trace-1", RequestID: "request-1", ReducerDecision: DecisionApplied,
				ReducerInput: &ReducerInputEvidence{EmittedAt: 1}, RenderGeneration: 3,
				ProcessHarness: "claude", ProcessPID: 4321, ProcessStartToken: "Mon Jul 13 10:11:12 2026",
				ProcessObservation: string(HarnessProcessMissing), ProcessMode: "interactive",
				WithdrawnFacts: processLifecycleTestIntPointer(1), TerminalHookEvidence: processTerminalHookEvidenceAbsent,
			}},
			Watchdog: WindowDebugWatchdog{
				ProcessExits: 1, LastProcessExitReason: HarnessProcessExitReasonInteractive,
				LastProcessExitObservation: string(HarnessProcessMissing), LastProcessExitAt: 2,
			},
		}},
	}

	projected, err := ProjectExportEventsPayload(payload, EventsExportSchemaV9)
	if err != nil {
		t.Fatalf("ProjectExportEventsPayload: %v", err)
	}
	data, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := int(raw["schema_version"].(float64)); got != EventsExportSchemaV9 {
		t.Fatalf("schema_version = %d; want 9", got)
	}
	window := raw["windows"].([]any)[0].(map[string]any)
	if _, ok := window["renderer"].(map[string]any)["generations"]; !ok {
		t.Fatalf("schema v9 lost render generations: %v", window)
	}
	event := window["events"].([]any)[0].(map[string]any)
	for _, key := range []string{"trace_id", "request_id", "reducer_decision", "reducer_input", "render_generation"} {
		if _, ok := event[key]; !ok {
			t.Fatalf("schema v9 event lost causal field %q: %v", key, event)
		}
	}
	for _, key := range []string{"process_harness", "process_pid", "process_start_token", "process_observation", "process_mode", "withdrawn_facts", "terminal_hook_evidence"} {
		if _, ok := event[key]; ok {
			t.Fatalf("schema v9 event leaked process field %q: %v", key, event)
		}
	}
	watchdog := window["watchdog"].(map[string]any)
	for _, key := range []string{"process_exits", "last_process_exit_reason", "last_process_exit_observation", "last_process_exit_at"} {
		if _, ok := watchdog[key]; ok {
			t.Fatalf("schema v9 watchdog leaked process field %q: %v", key, watchdog)
		}
	}
	fact := window["shadow"].(map[string]any)["facts"].([]any)[0].(map[string]any)
	for _, key := range []string{"ph", "pp", "pt"} {
		if _, ok := fact[key]; ok {
			t.Fatalf("schema v9 fact leaked process field %q: %v", key, fact)
		}
	}
	for _, key := range []string{"k", "y", "o", "ow", "pm"} {
		if _, ok := fact[key]; !ok {
			t.Fatalf("schema v9 fact lost prior field %q: %v", key, fact)
		}
	}
}

func TestProjectExportEventsPayloadV10IncludesProcessFields(t *testing.T) {
	payload := ExportEventsPayload{Windows: []ExportWindow{{
		Shadow: WindowDebugShadow{Facts: []SourceFactSnapshot{{
			Kind: SourceHarness, Key: "claude:s1", Icon: "bolt",
			HarnessProcessHarness: "claude", HarnessProcessPID: 4321,
			HarnessProcessStartToken: "Mon Jul 13 10:11:12 2026",
		}}},
		Watchdog: WindowDebugWatchdog{
			ProcessExits: 1, LastProcessExitReason: HarnessProcessExitReasonPipe,
			LastProcessExitObservation: string(HarnessProcessReplacedInstance), LastProcessExitAt: 2,
		},
		Events: []SessionEvent{{
			At: 2, Event: EventHarnessProcessExited, Action: "idle", Reason: HarnessProcessExitReasonPipe,
			ProcessHarness: "claude", ProcessPID: 4321, ProcessStartToken: "Mon Jul 13 10:11:12 2026",
			ProcessObservation: string(HarnessProcessReplacedInstance), ProcessMode: "pipe", WithdrawnFacts: processLifecycleTestIntPointer(1),
			TerminalHookEvidence: processTerminalHookEvidenceAbsent,
		}},
	}}}
	projected, err := ProjectExportEventsPayload(payload, EventsExportSchemaVersion)
	if err != nil {
		t.Fatalf("ProjectExportEventsPayload: %v", err)
	}
	data, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := int(raw["schema_version"].(float64)); got != 10 {
		t.Fatalf("schema_version = %d; want 10", got)
	}
	fact := raw["windows"].([]any)[0].(map[string]any)["shadow"].(map[string]any)["facts"].([]any)[0].(map[string]any)
	if fact["ph"] != "claude" || int(fact["pp"].(float64)) != 4321 || fact["pt"] != "Mon Jul 13 10:11:12 2026" {
		t.Fatalf("schema v10 fact process fields = %v", fact)
	}
	window := raw["windows"].([]any)[0].(map[string]any)
	event := window["events"].([]any)[0].(map[string]any)
	if event["process_harness"] != "claude" || int(event["process_pid"].(float64)) != 4321 ||
		event["process_start_token"] != "Mon Jul 13 10:11:12 2026" || event["process_observation"] != string(HarnessProcessReplacedInstance) {
		t.Fatalf("schema v10 event process fields = %v", event)
	}
	watchdog := window["watchdog"].(map[string]any)
	if int(watchdog["process_exits"].(float64)) != 1 || watchdog["last_process_exit_reason"] != HarnessProcessExitReasonPipe {
		t.Fatalf("schema v10 watchdog process fields = %v", watchdog)
	}
}

func processLifecycleTestIntPointer(value int) *int { return &value }

func TestHandler_EventsExport_CopilotCompactCleanupSynthesizesStop(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Harness:      "copilot",
		Action:       "compact",
		Event:        "preCompact",
		SessionTitle: "W:api",
		Account:      "work",
	})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Harness:      "copilot",
		Action:       "idle",
		Event:        "notification",
		SessionTitle: "W:api",
		Account:      "work",
	})

	resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 1})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if len(resp.EventsByWindow[1]) != 2 {
		t.Fatalf("events = %d; want 2 (%+v)", len(resp.EventsByWindow[1]), resp.EventsByWindow[1])
	}
	ev := resp.EventsByWindow[1][1]
	if ev.Event != "notification" {
		t.Fatalf("event = %q; want notification", ev.Event)
	}
	if ev.Action != "stop" {
		t.Fatalf("action = %q; want stop", ev.Action)
	}
	if ev.ResultIconState != "stop" {
		t.Fatalf("result_icon_state = %q; want stop", ev.ResultIconState)
	}
	if ev.CurrentIconState != "compact" {
		t.Fatalf("current_icon_state = %q; want compact", ev.CurrentIconState)
	}
	meta := resp.WindowMetadata[1]
	if meta.Durable.LastAction != "stop" {
		t.Fatalf("last_action = %q; want stop", meta.Durable.LastAction)
	}
	if meta.Durable.Harness != "copilot" {
		t.Fatalf("harness = %q; want copilot", meta.Durable.Harness)
	}
}

func TestHandler_EventsExportIncludesRendererFailuresAndSuppressionReasons(t *testing.T) {
	h := newTestHandler()
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 7, Action: "failure", KittyListenOn: "unix:/tmp/kitty-7"})
	// The legacy failure guard refuses the permission (logged as guard_blocked),
	// but the reducer applies it: key outranks warning in the icon precedence,
	// so the authoritative visible icon is the key.
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 7, Action: "permission"})

	h.stateMu.Lock()
	st := h.store[7]
	st.RenderFailures = 3
	st.LastRenderError = "set user vars: kitty unavailable"
	h.syncRendererMetadataLocked(7)
	h.stateMu.Unlock()

	resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 7})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	meta := resp.WindowMetadata[7]
	if meta.Renderer.DesiredIcon != "key" || !meta.Renderer.StateSet || meta.Renderer.RenderFailures != 3 ||
		meta.Renderer.LastRenderError != "set user vars: kitty unavailable" ||
		meta.Renderer.KittyListenOn != "unix:/tmp/kitty-7" {
		t.Fatalf("renderer metadata = %+v", meta.Renderer)
	}
	if got := meta.Stats.SuppressionReasons[suppressionReasonGuardBlocked]; got != 1 {
		t.Fatalf("suppression reasons = %+v; want %s=1", meta.Stats.SuppressionReasons, suppressionReasonGuardBlocked)
	}
}

// TestHandler_SessionDetail_UnknownWindow asserts that querying detail for a
// window that has never produced an event returns a non-nil empty slice. The
// TUI relies on this to distinguish "no daemon error" from "no events".
func TestHandler_SessionDetail_UnknownWindow(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 99})
	if !resp.OK {
		t.Fatalf("session detail: want OK=true, got %+v", resp)
	}
	if resp.Events == nil {
		t.Fatal("Events = nil; want non-nil empty slice")
	}
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d; want 0", len(resp.Events))
	}
}

// TestHandler_EventsExport_ReturnsAllWindows seeds three windows with distinct
// event counts and asserts the export returns every window keyed by its ID
// with the right per-window count. This pins the multi-window aggregation that
// the new `amx daemon export-events` CLI depends on.
func TestHandler_EventsExport_ReturnsAllWindows(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse", SessionTitle: "W:one"})

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "active", Event: "PreToolUse", SessionTitle: "W:two"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 2, Action: "stop", Event: "Stop", SessionTitle: "W:two"})

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 3, Action: "active", Event: "PreToolUse", SessionTitle: "W:three"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 3, Action: "active", Event: "PostToolUse", SessionTitle: "W:three"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 3, Action: "stop", Event: "Stop", SessionTitle: "W:three"})

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if got := len(resp.EventsByWindow); got != 3 {
		t.Fatalf("len(EventsByWindow) = %d; want 3 (%+v)", got, resp.EventsByWindow)
	}
	want := map[int]int{1: 1, 2: 2, 3: 3}
	for id, n := range want {
		got := len(resp.EventsByWindow[id])
		if got != n {
			t.Errorf("window %d events = %d; want %d", id, got, n)
		}
	}
	if got := len(resp.WindowMetadata); got != 3 {
		t.Fatalf("len(WindowMetadata) = %d; want 3 (%+v)", got, resp.WindowMetadata)
	}
}

func TestHandler_EventsExport_WindowFilterReturnsOneWindow(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse", SessionTitle: "W:one"})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     2,
		Action:       "active",
		Event:        "PreToolUse",
		SessionTitle: "W:two",
		Account:      "work",
		Harness:      "copilot",
	})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     2,
		Action:       "stop",
		Event:        "Stop",
		SessionTitle: "W:two",
		Account:      "work",
		Harness:      "copilot",
	})

	resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 2})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if got := len(resp.EventsByWindow); got != 1 {
		t.Fatalf("len(EventsByWindow) = %d; want 1 (%+v)", got, resp.EventsByWindow)
	}
	if got := len(resp.WindowMetadata); got != 1 {
		t.Fatalf("len(WindowMetadata) = %d; want 1 (%+v)", got, resp.WindowMetadata)
	}
	if got := len(resp.EventsByWindow[2]); got != 2 {
		t.Fatalf("window 2 events = %d; want 2", got)
	}
	if _, ok := resp.EventsByWindow[1]; ok {
		t.Fatalf("unexpected window 1 events in filtered export: %+v", resp.EventsByWindow)
	}
	if meta := resp.WindowMetadata[2]; meta.Durable.SessionTitle != "W:two" || meta.Durable.Harness != "copilot" {
		t.Fatalf("window 2 metadata = %+v; want durable session title/harness", meta)
	}
}

// TestHandler_EventsExport_EmptyWhenNoEvents asserts the export succeeds when
// no events have ever been buffered. The map may be nil or empty — both
// encode the same "no investigation surface yet" semantics on the wire.
func TestHandler_EventsExport_EmptyWhenNoEvents(t *testing.T) {
	h := newTestHandler()

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if len(resp.EventsByWindow) != 0 {
		t.Fatalf("EventsByWindow = %+v; want empty", resp.EventsByWindow)
	}
	if len(resp.WindowMetadata) != 0 {
		t.Fatalf("WindowMetadata = %+v; want empty", resp.WindowMetadata)
	}
}

// TestHandler_EventsExport_DoesNotMutateOriginal pins the defensive copy in
// handleEventsExport: a caller that mutates the returned slice must not be
// able to corrupt the daemon's internal ring buffer. Without the per-window
// `make+copy`, the returned slice header would alias the buffer and any
// in-place write would leak back into subsequent exports.
func TestHandler_EventsExport_DoesNotMutateOriginal(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "PreToolUse",
		ToolName:     "Bash",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "copilot",
	})

	first := h.Handle(Request{Type: TypeEventsExport})
	if !first.OK || len(first.EventsByWindow[1]) != 1 {
		t.Fatalf("first export: want one event for window 1, got %+v", first)
	}

	// Tamper with the returned slice. If the handler aliases its internal
	// buffer, the next export will reflect this mutation.
	first.EventsByWindow[1][0].Event = "TAMPERED"
	meta := first.WindowMetadata[1]
	meta.Durable.SessionTitle = "TAMPERED"
	first.WindowMetadata[1] = meta

	second := h.Handle(Request{Type: TypeEventsExport})
	if !second.OK || len(second.EventsByWindow[1]) != 1 {
		t.Fatalf("second export: want one event for window 1, got %+v", second)
	}
	if got := second.EventsByWindow[1][0].Event; got != "PreToolUse" {
		t.Fatalf("internal buffer mutated: event = %q; want %q", got, "PreToolUse")
	}
	if got := second.WindowMetadata[1].Durable.SessionTitle; got != "W:api" {
		t.Fatalf("metadata mutated: session_title = %q; want %q", got, "W:api")
	}
}

func TestHandler_EventsExport_CarriesSource(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "SessionStart",
		Source:       "new",
		SessionTitle: "W:api",
	})

	resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 1})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if len(resp.EventsByWindow[1]) != 1 {
		t.Fatalf("window 1 events = %d; want 1 (%+v)", len(resp.EventsByWindow[1]), resp.EventsByWindow[1])
	}
	if got := resp.EventsByWindow[1][0].Source; got != "new" {
		t.Fatalf("source = %q; want new", got)
	}
}

func TestHandler_EventsExport_IncludesDurableMetadata(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "PreToolUse",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "Copilot",
		PipeMode:     true,
	})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "idle",
		Event:        "TeammateIdle",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "Copilot",
		PipeMode:     true,
	})
	// Simulate a degraded terminal hook where the daemon no longer gets live
	// Kitty identity strings. The durable snapshot must keep the prior values.
	h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 1,
		Action:   "stop",
		Event:    "Stop",
		PipeMode: true,
	})

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	meta, ok := resp.WindowMetadata[1]
	if !ok {
		t.Fatalf("WindowMetadata[1] missing; got %+v", resp.WindowMetadata)
	}
	if meta.Stats.EventCount != 3 {
		t.Fatalf("event_count = %d; want 3", meta.Stats.EventCount)
	}
	if meta.Stats.FirstEventAt == 0 || meta.Stats.LastEventAt == 0 {
		t.Fatalf("stats timestamps = %+v; want non-zero first/last", meta.Stats)
	}
	if meta.Durable.SessionTitle != "W:api" {
		t.Fatalf("session_title = %q; want %q", meta.Durable.SessionTitle, "W:api")
	}
	if meta.Durable.Profile != "work" {
		t.Fatalf("profile = %q; want %q", meta.Durable.Profile, "work")
	}
	if meta.Durable.Harness != "copilot" {
		t.Fatalf("harness = %q; want %q", meta.Durable.Harness, "copilot")
	}
	if !meta.Durable.PipeMode {
		t.Fatal("pipe_mode = false; want true")
	}
	if meta.Durable.ActivityAt == 0 {
		t.Fatal("activity_at = 0; want non-zero")
	}
	if meta.Durable.LastAction != "stop" {
		t.Fatalf("last_action = %q; want %q", meta.Durable.LastAction, "stop")
	}
	if meta.Durable.LastActiveEvent != "PreToolUse" {
		t.Fatalf("last_active_event = %q; want %q", meta.Durable.LastActiveEvent, "PreToolUse")
	}
}

func TestHandler_EventsExport_DurableActiveMetadataSurvivesIdleCleanup(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "active",
		Event:        "PreToolUse",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "claude",
	})
	h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     1,
		Action:       "idle",
		Event:        "TeammateIdle",
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "claude",
	})

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	meta := resp.WindowMetadata[1]
	if meta.Durable.LastAction != "active" {
		t.Fatalf("last_action after idle = %q; want active", meta.Durable.LastAction)
	}
	if meta.Durable.LastActiveEvent != "PreToolUse" {
		t.Fatalf("last_active_event after idle = %q; want PreToolUse", meta.Durable.LastActiveEvent)
	}
}

func TestHandler_EventsExport_DurableSubagentCountPreservedOnTerminalCleanup(t *testing.T) {
	h := newTestHandler()

	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart", SessionTitle: "W:api"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "subagent", Event: "SubagentStart", SessionTitle: "W:api"})
	h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "stop", Event: "Stop", SessionTitle: "W:api"})

	resp := h.Handle(Request{Type: TypeEventsExport})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	if got := resp.WindowMetadata[1].Durable.SubagentCount; got != 2 {
		t.Fatalf("subagent_count after terminal cleanup = %d; want 2 (preserve last known in-flight count)", got)
	}
}
