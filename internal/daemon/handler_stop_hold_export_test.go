package daemon

import (
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"
)

func stopHoldExportPayload() ExportEventsPayload {
	return ExportEventsPayload{Windows: []ExportWindow{{
		WindowID: 3,
		Stats: WindowDebugStats{
			EventCount: 2,
			SuppressionReasons: map[string]int{
				suppressionReasonGuardBlocked:           1,
				suppressionReasonTerminalStopHeld:       2,
				suppressionReasonTerminalStopSuperseded: 3,
				suppressionReasonTerminalStopBypassed:   4,
			},
		},
		Shadow: WindowDebugShadow{
			EffectiveIcon: "bolt",
			PendingTerminalStop: &PendingTerminalStop{
				ArmedAt: 100, QuietUntil: 102, Deadline: 108, Confirmed: true, Class: StopHoldClassTerminal,
			},
		},
		Events: []SessionEvent{
			{
				At: 100, Event: EventTerminalStopHold, Action: "stop",
				StopHoldOutcome: StopHoldOutcomeHeld,
			},
			{
				At: 103, Event: "Stop", Action: "stop",
				StopHoldOutcome: StopHoldOutcomeConfirmed, StopHoldWaitedMs: 1400,
				StopConfirmedBy: StopConfirmedByClaudeScreen,
			},
		},
	}}}
}

func stopHoldExportJSON(t *testing.T, schema int) map[string]any {
	t.Helper()
	projected, err := ProjectExportEventsPayload(stopHoldExportPayload(), schema)
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

func TestProjectExportEventsPayloadV11RoundTripsStopHoldFields(t *testing.T) {
	raw := stopHoldExportJSON(t, EventsExportSchemaVersion)
	if got := int(raw["schema_version"].(float64)); got != 11 {
		t.Fatalf("schema_version = %d; want 11", got)
	}
	window := raw["windows"].([]any)[0].(map[string]any)
	pending, ok := window["shadow"].(map[string]any)["pending_terminal_stop"].(map[string]any)
	if !ok {
		t.Fatalf("schema v11 shadow lost pending_terminal_stop: %v", window["shadow"])
	}
	if int(pending["armed_at"].(float64)) != 100 || int(pending["quiet_until"].(float64)) != 102 ||
		int(pending["deadline"].(float64)) != 108 || pending["confirmed"] != true ||
		pending["class"] != StopHoldClassTerminal {
		t.Fatalf("schema v11 pending_terminal_stop = %v", pending)
	}
	events := window["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("schema v11 events = %d; want 2", len(events))
	}
	held := events[0].(map[string]any)
	if held["event"] != EventTerminalStopHold || held["stop_hold_outcome"] != string(StopHoldOutcomeHeld) {
		t.Fatalf("schema v11 hold row = %v", held)
	}
	confirmed := events[1].(map[string]any)
	if confirmed["stop_hold_outcome"] != string(StopHoldOutcomeConfirmed) ||
		int(confirmed["stop_hold_waited_ms"].(float64)) != 1400 ||
		confirmed["stop_confirmed_by"] != string(StopConfirmedByClaudeScreen) {
		t.Fatalf("schema v11 confirmed row = %v", confirmed)
	}
	reasons := window["stats"].(map[string]any)["suppression_reasons"].(map[string]any)
	for reason, want := range map[string]int{
		suppressionReasonTerminalStopHeld:       2,
		suppressionReasonTerminalStopSuperseded: 3,
		suppressionReasonTerminalStopBypassed:   4,
	} {
		if got, ok := reasons[reason].(float64); !ok || int(got) != want {
			t.Fatalf("schema v11 dropped stop-hold aggregate %q: %v", reason, reasons)
		}
	}
}

func TestProjectExportEventsPayloadV10OmitsStopHoldDiagnostics(t *testing.T) {
	raw := stopHoldExportJSON(t, EventsExportSchemaV10)
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
	if event["event"] == EventTerminalStopHold {
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
		{"v10", EventsExportSchemaV10},
		{"v9", EventsExportSchemaV9},
		{"v8", EventsExportSchemaV8},
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

		resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 42})
		if !resp.OK {
			t.Fatalf("events export: want OK=true, got %+v", resp)
		}
		held := resp.WindowMetadata[42].Shadow.PendingTerminalStop
		if held == nil {
			t.Fatalf("export lost the live terminal hold: %+v", resp.WindowMetadata[42].Shadow)
		}
		armed := time.Now()
		if held.Class != StopHoldClassTerminal || held.Confirmed {
			t.Fatalf("exported hold = %+v, want an unconfirmed %s hold", held, StopHoldClassTerminal)
		}
		if held.ArmedAt != armed.Unix() ||
			held.QuietUntil != armed.Add(terminalHoldTestDelay).Unix() ||
			held.Deadline != armed.Add(terminalHoldTestTimeout).Unix() {
			t.Fatalf("exported hold timings = %+v, want armed %d with a %s quiet period and a %s deadline",
				held, armed.Unix(), terminalHoldTestDelay, terminalHoldTestTimeout)
		}

		time.Sleep(terminalHoldTestTimeout + time.Second)
		synctest.Wait()

		resp = h.Handle(Request{Type: TypeEventsExport, WindowID: 42})
		if released := resp.WindowMetadata[42].Shadow.PendingTerminalStop; released != nil {
			t.Fatalf("export kept a released hold: %+v", released)
		}
	})
}

func TestHandler_AppendStopHoldEventRecordsSuppressedRow(t *testing.T) {
	h := newTestHandler()
	req := Request{
		Type:         TypeStateUpdate,
		WindowID:     4,
		Harness:      "claude",
		Action:       "stop",
		Event:        "Stop",
		SessionTitle: "W:api",
		Account:      "work",
	}
	h.appendStopHoldEvent(req, StopHoldOutcomeHeld, 1500*time.Millisecond)

	resp := h.Handle(Request{Type: TypeEventsExport, WindowID: 4})
	if !resp.OK {
		t.Fatalf("events export: want OK=true, got %+v", resp)
	}
	events := resp.EventsByWindow[4]
	if len(events) != 1 {
		t.Fatalf("events = %d; want 1 (%+v)", len(events), events)
	}
	ev := events[0]
	if ev.Event != EventTerminalStopHold {
		t.Fatalf("event = %q; want %q", ev.Event, EventTerminalStopHold)
	}
	if ev.StopHoldOutcome != StopHoldOutcomeHeld {
		t.Fatalf("stop_hold_outcome = %q; want %q", ev.StopHoldOutcome, StopHoldOutcomeHeld)
	}
	if ev.StopHoldWaitedMs != 1500 {
		t.Fatalf("stop_hold_waited_ms = %d; want 1500", ev.StopHoldWaitedMs)
	}
	if ev.DecisionReason != DecisionReasonTerminalStopHeld || ev.ReducerReason != DecisionReasonTerminalStopHeld {
		t.Fatalf("reason = %q/%q; want %q", ev.DecisionReason, ev.ReducerReason, DecisionReasonTerminalStopHeld)
	}
	if ev.ReducerDecision != DecisionSuppressed {
		t.Fatalf("reducer_decision = %q; want %q", ev.ReducerDecision, DecisionSuppressed)
	}
	if reason, ok := stopHoldDecisionReason(StopHoldOutcomeConfirmed); ok {
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
	for _, tc := range []struct {
		outcome StopHoldOutcome
		want    string
	}{
		{StopHoldOutcomeSuperseded, suppressionReasonTerminalStopSuperseded},
		{StopHoldOutcomeBypassed, suppressionReasonTerminalStopBypassed},
	} {
		reason, ok := stopHoldDecisionReason(tc.outcome)
		if !ok || reason != tc.want {
			t.Fatalf("stopHoldDecisionReason(%q) = %q, %v; want %q", tc.outcome, reason, ok, tc.want)
		}
	}
	for _, outcome := range []StopHoldOutcome{StopHoldOutcomeConfirmed, StopHoldOutcomeDeadline} {
		if _, ok := stopHoldDecisionReason(outcome); ok {
			t.Fatalf("release outcome %q must not produce its own hold row", outcome)
		}
	}
}
