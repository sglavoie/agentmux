package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRequest_RoundtripCarriesToolName pins that the new ToolName field
// survives a JSON marshal/unmarshal cycle on the wire. Sessions 2+ rely on
// this field being non-empty when the indicator forwards a PreToolUse /
// PostToolUse payload, so a typo or missing tag would silently drop the data.
func TestRequest_RoundtripCarriesToolName(t *testing.T) {
	in := Request{
		Type:     TypeStateUpdate,
		WindowID: 42,
		Action:   "active",
		Event:    "PreToolUse",
		ToolName: "Bash",
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
}

// TestResponse_RoundtripCarriesEvents pins that SessionEvent slices survive
// JSON roundtrip with all fields preserved. Session 2's handleSessionDetail
// returns these to the TUI, so a broken Marshal/Unmarshal here would surface
// as an empty detail pane.
func TestResponse_RoundtripCarriesEvents(t *testing.T) {
	in := Response{
		OK: true,
		Events: []SessionEvent{
			{At: 1700000000, Event: "PreToolUse", Action: "active", ToolName: "Bash"},
			{At: 1700000001, Event: "Stop", Action: "stop", Suppressed: true},
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
	if out.Events[0] != (SessionEvent{At: 1700000000, Event: "PreToolUse", Action: "active", ToolName: "Bash"}) {
		t.Fatalf("Events[0] = %+v", out.Events[0])
	}
	if out.Events[1] != (SessionEvent{At: 1700000001, Event: "Stop", Action: "stop", Suppressed: true}) {
		t.Fatalf("Events[1] = %+v", out.Events[1])
	}
}

// TestSessionEvent_OmitsZeroOptionalFields confirms that omitempty fires on
// ToolName and Suppressed so a wire payload for a minimal event stays compact.
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
}

// TestTypeSessionDetail_Const pins the message-type literal so a careless
// rename in protocol.go does not silently break clients that hardcode the
// string (e.g. in stored debug logs).
func TestTypeSessionDetail_Const(t *testing.T) {
	if string(TypeSessionDetail) != "session_detail" {
		t.Fatalf("TypeSessionDetail = %q, want %q", TypeSessionDetail, "session_detail")
	}
}
