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
	projected, err := ProjectExportEventsPayload(payload, EventsExportSchemaV10)
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
