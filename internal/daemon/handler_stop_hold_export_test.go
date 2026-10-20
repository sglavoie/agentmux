package daemon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func stopHoldExportPayload() protocol.ExportEventsPayload {
	return protocol.ExportEventsPayload{Windows: []protocol.ExportWindow{{
		WindowID: 3,
		Stats: protocol.WindowDebugStats{
			EventCount: 2,
			SuppressionReasons: map[string]int{
				suppressionReasonGuardBlocked:           1,
				suppressionReasonTerminalStopHeld:       2,
				suppressionReasonTerminalStopSuperseded: 3,
				suppressionReasonTerminalStopBypassed:   4,
			},
		},
		Shadow: protocol.WindowDebugShadow{
			EffectiveIcon:    "bolt",
			EffectiveLooping: true,
			EffectiveGen:     7,
			MismatchCount:    1,
			BusySinceAt:      90,
			Mismatches: []protocol.ShadowMismatch{{
				At: 99, Event: "PreToolUse", Action: "active", LegacyIcon: "", ShadowIcon: "bolt",
			}},
			Facts: []protocol.SourceFactSnapshot{{
				Kind: protocol.SourceHarness, Key: "claude:session", Action: "active", Icon: "bolt", Seq: 7,
			}},
			PendingTerminalStop: &protocol.PendingTerminalStop{
				ArmedAt: 100, QuietUntil: 102, Deadline: 108, Confirmed: true, Class: protocol.StopHoldClassTerminal,
			},
		},
		Events: []protocol.SessionEvent{
			{
				At: 100, Event: protocol.EventTerminalStopHold, Action: "stop",
				StopHoldOutcome: protocol.StopHoldOutcomeHeld,
			},
			{
				At: 103, Event: "Stop", Action: "stop",
				StopHoldOutcome: protocol.StopHoldOutcomeConfirmed, StopHoldWaitedMs: 1400,
				StopConfirmedBy: protocol.StopConfirmedByClaudeScreen,
			},
		},
	}}}
}

func stopHoldExportJSON(t *testing.T, schema int) map[string]any {
	t.Helper()
	projected, err := protocol.ProjectExportEventsPayload(stopHoldExportPayload(), schema)
	if err != nil {
		t.Fatalf("ProjectExportEventsPayload(%d): %v", schema, err)
	}
	data, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return raw
}

func TestProjectExportEventsPayloadV12RoundTripsStopHoldFields(t *testing.T) {
	t.Parallel()

	raw := stopHoldExportJSON(t, protocol.EventsExportSchemaV12)
	if got := int(raw["schema_version"].(float64)); got != 12 {
		t.Fatalf("schema_version = %d; want 12", got)
	}
	window := raw["windows"].([]any)[0].(map[string]any)
	pending, ok := window["shadow"].(map[string]any)["pending_terminal_stop"].(map[string]any)
	if !ok {
		t.Fatalf("schema v12 shadow lost pending_terminal_stop: %v", window["shadow"])
	}
	if int(pending["armed_at"].(float64)) != 100 || int(pending["quiet_until"].(float64)) != 102 ||
		int(pending["deadline"].(float64)) != 108 || pending["confirmed"] != true ||
		pending["class"] != protocol.StopHoldClassTerminal {
		t.Fatalf("schema v12 pending_terminal_stop = %v", pending)
	}
	events := window["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("schema v12 events = %d; want 2", len(events))
	}
	held := events[0].(map[string]any)
	if held["event"] != protocol.EventTerminalStopHold || held["stop_hold_outcome"] != string(protocol.StopHoldOutcomeHeld) {
		t.Fatalf("schema v12 hold row = %v", held)
	}
	confirmed := events[1].(map[string]any)
	if confirmed["stop_hold_outcome"] != string(protocol.StopHoldOutcomeConfirmed) ||
		int(confirmed["stop_hold_waited_ms"].(float64)) != 1400 ||
		confirmed["stop_confirmed_by"] != string(protocol.StopConfirmedByClaudeScreen) {
		t.Fatalf("schema v12 confirmed row = %v", confirmed)
	}
	reasons := window["stats"].(map[string]any)["suppression_reasons"].(map[string]any)
	for reason, want := range map[string]int{
		suppressionReasonTerminalStopHeld:       2,
		suppressionReasonTerminalStopSuperseded: 3,
		suppressionReasonTerminalStopBypassed:   4,
	} {
		if got, ok := reasons[reason].(float64); !ok || int(got) != want {
			t.Fatalf("schema v12 dropped stop-hold aggregate %q: %v", reason, reasons)
		}
	}
}

func TestProjectExportEventsPayloadV11IdentityProjection(t *testing.T) {
	payload := stopHoldExportPayload()
	projectedV11, err := protocol.ProjectExportEventsPayload(payload, protocol.EventsExportSchemaV11)
	if err != nil {
		t.Fatalf("ProjectExportEventsPayload(v11): %v", err)
	}
	projectedV12, err := protocol.ProjectExportEventsPayload(payload, protocol.EventsExportSchemaV12)
	if err != nil {
		t.Fatalf("ProjectExportEventsPayload(v12): %v", err)
	}
	v11JSON, err := json.Marshal(projectedV11)
	if err != nil {
		t.Fatalf("Marshal(v11): %v", err)
	}
	v12JSON, err := json.Marshal(projectedV12)
	if err != nil {
		t.Fatalf("Marshal(v12): %v", err)
	}
	normalizedV12 := bytes.Replace(v12JSON, []byte(`,"busy_since_at":90`), nil, 1)
	normalizedV12 = bytes.Replace(normalizedV12, []byte(`"schema_version":12`), []byte(`"schema_version":11`), 1)
	if !bytes.Equal(v11JSON, normalizedV12) {
		t.Fatalf("schema v11 projection differs from v12 beyond schema_version and busy_since_at:\nv11: %s\nv12 normalized: %s", v11JSON, normalizedV12)
	}

	var rawV11 map[string]any
	if err := json.Unmarshal(v11JSON, &rawV11); err != nil {
		t.Fatalf("Unmarshal(v11): %v", err)
	}
	var rawV12 map[string]any
	if err := json.Unmarshal(v12JSON, &rawV12); err != nil {
		t.Fatalf("Unmarshal(v12): %v", err)
	}
	if got := int(rawV11["schema_version"].(float64)); got != 11 {
		t.Fatalf("schema_version = %d; want 11", got)
	}
	window := rawV11["windows"].([]any)[0].(map[string]any)
	shadowV11 := window["shadow"].(map[string]any)
	if _, ok := shadowV11["busy_since_at"]; ok {
		t.Fatalf("schema v11 shadow leaked busy_since_at: %v", shadowV11)
	}
	shadowV12 := rawV12["windows"].([]any)[0].(map[string]any)["shadow"].(map[string]any)
	if got, ok := shadowV12["busy_since_at"].(float64); !ok || int64(got) != 90 {
		t.Fatalf("schema v12 busy_since_at = %v; want 90", shadowV12["busy_since_at"])
	}
	pending, ok := shadowV11["pending_terminal_stop"].(map[string]any)
	if !ok {
		t.Fatalf("schema v11 shadow lost pending_terminal_stop: %v", window["shadow"])
	}
	if int(pending["armed_at"].(float64)) != 100 || int(pending["quiet_until"].(float64)) != 102 ||
		int(pending["deadline"].(float64)) != 108 || pending["confirmed"] != true ||
		pending["class"] != protocol.StopHoldClassTerminal {
		t.Fatalf("schema v11 pending_terminal_stop = %v", pending)
	}
	events := window["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("schema v11 events = %d; want 2", len(events))
	}
	held := events[0].(map[string]any)
	if held["event"] != protocol.EventTerminalStopHold || held["stop_hold_outcome"] != string(protocol.StopHoldOutcomeHeld) {
		t.Fatalf("schema v11 hold row = %v", held)
	}
	confirmed := events[1].(map[string]any)
	if confirmed["stop_hold_outcome"] != string(protocol.StopHoldOutcomeConfirmed) ||
		int(confirmed["stop_hold_waited_ms"].(float64)) != 1400 ||
		confirmed["stop_confirmed_by"] != string(protocol.StopConfirmedByClaudeScreen) {
		t.Fatalf("schema v11 confirmed row = %v", confirmed)
	}
	reasons := window["stats"].(map[string]any)["suppression_reasons"].(map[string]any)
	for reason, want := range map[string]int{
		suppressionReasonGuardBlocked:           1,
		suppressionReasonTerminalStopHeld:       2,
		suppressionReasonTerminalStopSuperseded: 3,
		suppressionReasonTerminalStopBypassed:   4,
	} {
		if got, ok := reasons[reason].(float64); !ok || int(got) != want {
			t.Fatalf("schema v11 suppression aggregate %q = %v; want %d", reason, reasons[reason], want)
		}
	}
}

func TestHandler_ExportProjectsBusySinceAndOmitsItWhenIdle(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "active", Event: "PreToolUse", SessionTitle: "W:api"})

	h.stateMu.Lock()
	anchor := h.store[42].BusySince
	h.stateMu.Unlock()
	if anchor.IsZero() {
		t.Fatal("active window has a zero busy anchor")
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 42})
	if got := resp.WindowMetadata[42].Shadow.BusySinceAt; got != anchor.Unix() {
		t.Fatalf("busy_since_at = %d; want anchor %d", got, anchor.Unix())
	}

	h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop", SessionTitle: "W:api"})
	resp = h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 42})
	encoded, err := json.Marshal(resp.WindowMetadata[42].Shadow)
	if err != nil {
		t.Fatalf("Marshal(idle shadow): %v", err)
	}
	if strings.Contains(string(encoded), `"busy_since_at"`) {
		t.Fatalf("idle shadow emitted busy_since_at: %s", encoded)
	}
}

func TestProjectExportEventsPayloadV10OmitsStopHoldDiagnostics(t *testing.T) {
	t.Parallel()

	raw := stopHoldExportJSON(t, protocol.EventsExportSchemaV10)
	if got := int(raw["schema_version"].(float64)); got != 10 {
		t.Fatalf("schema_version = %d; want 10", got)
	}
	window := raw["windows"].([]any)[0].(map[string]any)
	if _, ok := window["shadow"].(map[string]any)["pending_terminal_stop"]; ok {
		t.Fatalf("schema v10 shadow leaked pending_terminal_stop: %v", window["shadow"])
	}
	events := window["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("schema v10 events = %d; want 1 (TerminalStopHold must be dropped)", len(events))
	}
	event := events[0].(map[string]any)
	if event["event"] == protocol.EventTerminalStopHold {
		t.Fatalf("schema v10 kept the synthetic hold row: %v", event)
	}
	for _, key := range []string{"stop_hold_outcome", "stop_hold_waited_ms", "stop_confirmed_by"} {
		if _, ok := event[key]; ok {
			t.Fatalf("schema v10 event leaked stop-hold field %q: %v", key, event)
		}
	}

	// Every pre-v11 projection shares one stats section, so the suppression
	// aggregates for the hold rows dropped above are checked for all three here
	// rather than in a v9/v8 test the stop-hold fixture does not otherwise need.
	for _, tc := range []struct {
		name   string
		schema int
	}{
		{"v10", protocol.EventsExportSchemaV10},
		{"v9", protocol.EventsExportSchemaV9},
		{"v8", protocol.EventsExportSchemaV8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stats := stopHoldExportJSON(t, tc.schema)["windows"].([]any)[0].(map[string]any)["stats"].(map[string]any)
			reasons, _ := stats["suppression_reasons"].(map[string]any)
			for _, reason := range []string{
				suppressionReasonTerminalStopHeld,
				suppressionReasonTerminalStopSuperseded,
				suppressionReasonTerminalStopBypassed,
			} {
				if _, ok := reasons[reason]; ok {
					t.Fatalf("schema %d leaked stop-hold suppression aggregate %q: %v", tc.schema, reason, reasons)
				}
			}
			if got, ok := reasons[suppressionReasonGuardBlocked].(float64); !ok || int(got) != 1 {
				t.Fatalf("schema %d dropped the pre-v11 suppression aggregates: %v", tc.schema, reasons)
			}
		})
	}
}

// TestHandler_ExportProjectsLiveTerminalStopHold pins the only evidence a
// consumer has that a window is showing ⚡ for a stop that has already been
// received: the hold itself, projected live at export time. It disappears the
// moment the hold releases, which is what makes a lingering section actionable
// (the stop_hold_stuck doctor check).
func TestHandler_ExportProjectsLiveTerminalStopHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _ := newTerminalHoldTestHandler(t)
		armTerminalHold(t, h)

		resp := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 42})
		if !resp.OK {
			t.Fatalf("events export: want OK=true, got %+v", resp)
		}
		held := resp.WindowMetadata[42].Shadow.PendingTerminalStop
		if held == nil {
			t.Fatalf("export lost the live terminal hold: %+v", resp.WindowMetadata[42].Shadow)
		}
		armed := time.Now()
		if held.Class != protocol.StopHoldClassTerminal || held.Confirmed {
			t.Fatalf("exported hold = %+v, want an unconfirmed %s hold", held, protocol.StopHoldClassTerminal)
		}
		if held.ArmedAt != armed.Unix() ||
			held.QuietUntil != armed.Add(terminalHoldTestDelay).Unix() ||
			held.Deadline != armed.Add(terminalHoldTestTimeout).Unix() {
			t.Fatalf("exported hold timings = %+v, want armed %d with a %s quiet period and a %s deadline",
				held, armed.Unix(), terminalHoldTestDelay, terminalHoldTestTimeout)
		}

		time.Sleep(terminalHoldTestTimeout + time.Second)
		synctest.Wait()

		resp = h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 42})
		if released := resp.WindowMetadata[42].Shadow.PendingTerminalStop; released != nil {
			t.Fatalf("export kept a released hold: %+v", released)
		}
	})
}

func TestHandler_AppendStopHoldEventRecordsSuppressedRow(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	req := protocol.Request{
		Type:         protocol.TypeStateUpdate,
		WindowID:     4,
		Harness:      "claude",
		Action:       "stop",
		Event:        "Stop",
		SessionTitle: "W:api",
		Account:      "work",
	}
	h.appendStopHoldEvent(req, protocol.StopHoldOutcomeHeld, 1500*time.Millisecond)

	resp := h.Handle(protocol.Request{Type: protocol.TypeEventsExport, WindowID: 4})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	events := resp.EventsByWindow[4]
	if len(events) != 1 {
		t.Fatalf("events = %d; want 1 (%+v)", len(events), events)
	}
	ev := events[0]
	if ev.Event != protocol.EventTerminalStopHold {
		t.Fatalf("event = %q; want %q", ev.Event, protocol.EventTerminalStopHold)
	}
	if ev.StopHoldOutcome != protocol.StopHoldOutcomeHeld {
		t.Fatalf("stop_hold_outcome = %q; want %q", ev.StopHoldOutcome, protocol.StopHoldOutcomeHeld)
	}
	if ev.StopHoldWaitedMs != 1500 {
		t.Fatalf("stop_hold_waited_ms = %d; want 1500", ev.StopHoldWaitedMs)
	}
	if ev.DecisionReason != protocol.DecisionReasonTerminalStopHeld || ev.ReducerReason != protocol.DecisionReasonTerminalStopHeld {
		t.Fatalf("reason = %q/%q; want %q", ev.DecisionReason, ev.ReducerReason, protocol.DecisionReasonTerminalStopHeld)
	}
	if ev.ReducerDecision != protocol.DecisionSuppressed {
		t.Fatalf("reducer_decision = %q; want %q", ev.ReducerDecision, protocol.DecisionSuppressed)
	}
	if reason, ok := stopHoldDecisionReason(protocol.StopHoldOutcomeConfirmed); ok {
		t.Fatalf("confirmed outcome produced a hold row reason %q", reason)
	}
	if got := resp.WindowMetadata[4].Stats.SuppressionReasons[suppressionReasonTerminalStopHeld]; got != 1 {
		t.Fatalf("suppression reasons = %+v; want %s=1", resp.WindowMetadata[4].Stats.SuppressionReasons, suppressionReasonTerminalStopHeld)
	}
}

// TestStopHoldDecisionReasonCoversHoldEndingOutcomes pins which outcomes own a
// synthetic row. An outcome that ends the hold without committing its stop has
// nowhere else to record itself; the two release outcomes are stamped on the
// stop the hold commits and must not also produce a row.
func TestStopHoldDecisionReasonCoversHoldEndingOutcomes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		outcome protocol.StopHoldOutcome
		want    string
	}{
		{protocol.StopHoldOutcomeSuperseded, suppressionReasonTerminalStopSuperseded},
		{protocol.StopHoldOutcomeBypassed, suppressionReasonTerminalStopBypassed},
	} {
		reason, ok := stopHoldDecisionReason(tc.outcome)
		if !ok || reason != tc.want {
			t.Fatalf("stopHoldDecisionReason(%q) = %q, %v; want %q", tc.outcome, reason, ok, tc.want)
		}
	}
	for _, outcome := range []protocol.StopHoldOutcome{protocol.StopHoldOutcomeConfirmed, protocol.StopHoldOutcomeDeadline} {
		if _, ok := stopHoldDecisionReason(outcome); ok {
			t.Fatalf("release outcome %q must not produce its own hold row", outcome)
		}
	}
}
