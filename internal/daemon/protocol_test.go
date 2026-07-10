package daemon

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestRequest_RoundtripCarriesDebugContext pins that normalized hook payload
// fields survive a JSON marshal/unmarshal cycle on the wire. The indicator
// forwards these to the daemon for event-log enrichment, so a tag typo would
// silently drop investigation context from export-events output.
func TestRequest_RoundtripCarriesDebugContext(t *testing.T) {
	in := Request{
		Type:             TypeStateUpdate,
		WindowID:         42,
		Action:           "active",
		Event:            "PreToolUse",
		ToolName:         "Bash",
		Source:           "clear",
		Harness:          "copilot",
		NotificationType: "permission_prompt",
		StopReason:       "context_limit_reached",
		Reason:           "error",
		ContextLimit:     true,
		ResetFlow:        true,
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal(Request): %v", err)
	}

	var out Request
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(Request): %v", err)
	}

	if out.ToolName != "Bash" {
		t.Fatalf("ToolName = %q, want %q", out.ToolName, "Bash")
	}
	if out.Source != "clear" {
		t.Fatalf("Source = %q, want %q", out.Source, "clear")
	}
	if out.Harness != "copilot" {
		t.Fatalf("Harness = %q, want %q", out.Harness, "copilot")
	}
	if out.NotificationType != "permission_prompt" ||
		out.StopReason != "context_limit_reached" || out.Reason != "error" || !out.ContextLimit || !out.ResetFlow {
		t.Fatalf("debug context mismatch: in=%+v out=%+v", in, out)
	}
	if out.Type != in.Type || out.WindowID != in.WindowID || out.Action != in.Action || out.Event != in.Event {
		t.Fatalf("Request roundtrip mismatch: in=%+v out=%+v", in, out)
	}
}

// TestRequest_OmittedToolNameMarshalsClean pins that an empty ToolName is
// omitted from the wire payload (omitempty), so existing daemons that do not
// know the field still see a clean request shape.
func TestRequest_OmittedToolNameMarshalsClean(t *testing.T) {
	in := Request{Type: TypeStateQuery}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal(Request): %v", err)
	}
	if got := string(data); strings.Contains(got, "tool_name") {
		t.Fatalf("expected tool_name to be omitted, got: %s", got)
	}
	if got := string(data); strings.Contains(got, "harness") {
		t.Fatalf("expected harness to be omitted, got: %s", got)
	}
	if got := string(data); strings.Contains(got, "source") {
		t.Fatalf("expected source to be omitted, got: %s", got)
	}
	if got := string(data); strings.Contains(got, "current_icon_state") {
		t.Fatalf("expected daemon-owned icon state to be absent from requests, got: %s", got)
	}
}

func TestResponse_RoundtripCarriesStateSnapshots(t *testing.T) {
	in := Response{OK: true, States: map[int]WindowStateSnapshot{
		42: {IconState: "key", Looping: true, LastAction: "permission", LastEventAt: 1_700_000_000, GuardAction: "permission"},
	}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal(Response): %v", err)
	}
	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(Response): %v", err)
	}
	if !reflect.DeepEqual(out.States, in.States) {
		t.Fatalf("state snapshots = %#v, want %#v", out.States, in.States)
	}
}

// TestResponse_RoundtripCarriesEvents pins that SessionEvent slices survive
// JSON roundtrip with all fields preserved. Session 2's handleSessionDetail
// returns these to the TUI, so a broken Marshal/Unmarshal here would surface
// as an empty detail pane.
func TestResponse_RoundtripCarriesEvents(t *testing.T) {
	in := Response{
		OK: true,
		Events: []SessionEvent{
			{
				At:               1700000000,
				Event:            "ScreenPromptProbe",
				Action:           "elicitation",
				Source:           "codex_screen",
				PromptKind:       "edit_approval",
				ResultIconState:  "question",
				CurrentIconState: "bolt",
			},
			{
				At:                1700000001,
				Event:             "Stop",
				Action:            "stop",
				Suppressed:        true,
				ResultIconState:   IconStatePreserve,
				SuppressionReason: "guard_blocked",
				StopReason:        "context_limit_reached",
				Reason:            "error",
				ContextLimit:      true,
				ResetFlow:         true,
			},
		},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal(Response): %v", err)
	}

	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(Response): %v", err)
	}

	if len(out.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2", len(out.Events))
	}
	if out.Events[0] != (SessionEvent{
		At:               1700000000,
		Event:            "ScreenPromptProbe",
		Action:           "elicitation",
		Source:           "codex_screen",
		PromptKind:       "edit_approval",
		ResultIconState:  "question",
		CurrentIconState: "bolt",
	}) {
		t.Fatalf("Events[0] = %+v", out.Events[0])
	}
	if out.Events[1] != (SessionEvent{
		At:                1700000001,
		Event:             "Stop",
		Action:            "stop",
		Suppressed:        true,
		ResultIconState:   IconStatePreserve,
		SuppressionReason: "guard_blocked",
		StopReason:        "context_limit_reached",
		Reason:            "error",
		ContextLimit:      true,
		ResetFlow:         true,
	}) {
		t.Fatalf("Events[1] = %+v", out.Events[1])
	}
}

// TestSessionEvent_OmitsZeroOptionalFields confirms that omitempty still fires
// on the optional enrichment fields. result_icon_state is intentionally always
// present so export-events can distinguish "clear icon" from "field omitted".
func TestSessionEvent_OmitsZeroOptionalFields(t *testing.T) {
	e := SessionEvent{At: 1, Event: "Stop", Action: "stop"}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal(SessionEvent): %v", err)
	}
	got := string(data)
	if strings.Contains(got, "tool_name") {
		t.Fatalf("expected tool_name to be omitted, got: %s", got)
	}
	if strings.Contains(got, "suppressed") {
		t.Fatalf("expected suppressed to be omitted, got: %s", got)
	}
	if !strings.Contains(got, `"result_icon_state":""`) {
		t.Fatalf("expected result_icon_state to be present, got: %s", got)
	}
	for _, field := range []string{
		"suppression_reason",
		"current_icon_state",
		"notification_type",
		"stop_reason",
		"reason",
		"source",
		"prompt_kind",
		"context_limit",
		"reset_flow",
	} {
		if strings.Contains(got, field) {
			t.Fatalf("expected %s to be omitted, got: %s", field, got)
		}
	}
}

// TestTypeSessionDetail_Const pins the message-type literal so a careless
// rename in protocol.go does not silently break clients that hardcode the
// string (e.g. in stored debug logs).
func TestTypeSessionDetail_Const(t *testing.T) {
	if string(TypeSessionDetail) != "session_detail" {
		t.Fatalf("TypeSessionDetail = %q, want %q", TypeSessionDetail, "session_detail")
	}
}

// TestResponse_RoundtripCarriesEventsByWindow pins that the per-window export
// map survives JSON roundtrip with every nested SessionEvent field intact,
// including the new PreservesIcon flag added in Session 3. Session 4's
// TypeEventsExport returns this map, so a tag typo would silently empty the
// investigator's view.
func TestResponse_RoundtripCarriesEventsByWindow(t *testing.T) {
	in := Response{
		OK: true,
		EventsByWindow: map[int][]SessionEvent{
			1: {
				{At: 1700000000, Event: "ScreenPromptProbe", Action: "elicitation", Source: "codex_screen", PromptKind: "request_user_input", ResultIconState: "question"},
				{At: 1700000001, Event: "agentStop", Action: "active", PreservesIcon: true, ResultIconState: IconStatePreserve},
			},
			2: {
				{
					At:                1700000010,
					Event:             "Stop",
					Action:            "stop",
					Suppressed:        true,
					ResultIconState:   IconStatePreserve,
					SuppressionReason: "active_after_terminal",
					CurrentIconState:  "stop",
				},
			},
		},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal(Response): %v", err)
	}

	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(Response): %v", err)
	}

	if !reflect.DeepEqual(out.EventsByWindow, in.EventsByWindow) {
		t.Fatalf("EventsByWindow roundtrip mismatch:\nin=%#v\nout=%#v", in.EventsByWindow, out.EventsByWindow)
	}
}

func TestResponse_RoundtripCarriesWindowMetadata(t *testing.T) {
	in := Response{
		OK: true,
		WindowMetadata: map[int]WindowDebugMetadata{
			1: {
				Stats: WindowDebugStats{
					EventCount:   7,
					FirstEventAt: 1700000000,
					LastEventAt:  1700000006,
				},
				Durable: WindowDebugDurable{
					SessionTitle:    "W:api",
					Profile:         "work",
					Harness:         "copilot",
					PipeMode:        true,
					ActivityAt:      1700000006,
					LastAction:      "stop",
					LastActiveEvent: "PreToolUse",
					SubagentCount:   2,
				},
			},
		},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal(Response): %v", err)
	}

	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(Response): %v", err)
	}

	if !reflect.DeepEqual(out.WindowMetadata, in.WindowMetadata) {
		t.Fatalf("WindowMetadata roundtrip mismatch:\nin=%#v\nout=%#v", in.WindowMetadata, out.WindowMetadata)
	}
}

// TestResponse_OmittedEventsByWindowMarshalsClean pins that omitempty fires on
// the new field so a TypeSessionDetail response does not waste bytes on an
// empty map.
func TestResponse_OmittedEventsByWindowMarshalsClean(t *testing.T) {
	in := Response{OK: true, Events: []SessionEvent{{At: 1, Event: "Stop", Action: "stop"}}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal(Response): %v", err)
	}
	if got := string(data); strings.Contains(got, "events_by_window") {
		t.Fatalf("expected events_by_window to be omitted, got: %s", got)
	}
	if got := string(data); strings.Contains(got, "window_metadata") {
		t.Fatalf("expected window_metadata to be omitted, got: %s", got)
	}
}
