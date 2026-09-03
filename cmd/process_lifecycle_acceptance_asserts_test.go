package cmd

import (
	"fmt"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func lifecycleAcceptanceIdentity(harnessName string, pid int) protocol.HarnessProcessIdentity {
	return protocol.HarnessProcessIdentity{
		Harness: harnessName, PID: pid,
		StartToken: fmt.Sprintf("Thu Jul 16 10:%02d:%02d 2026", pid%60, (pid/60)%60),
	}
}

func lifecycleAcceptanceFact(
	meta protocol.WindowDebugMetadata,
	kind protocol.SourceKind,
	key string,
) *protocol.SourceFactSnapshot {
	for i := range meta.Shadow.Facts {
		fact := meta.Shadow.Facts[i]
		if fact.Kind == kind && fact.Key == key {
			return &fact
		}
	}
	return nil
}

func lifecycleAcceptanceExit(events []protocol.SessionEvent, pid int) (protocol.SessionEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event == protocol.EventHarnessProcessExited && events[i].ProcessPID == pid {
			return events[i], true
		}
	}
	return protocol.SessionEvent{}, false
}

// lifecycleAcceptanceReplayedRow reports whether a row is the non-mutating clone
// the daemon appends when its dedupe cache replays a retried IPC request. The
// clone repeats the original row's event and outcome verbatim, so counting it
// would report a hold or a stop the daemon never performed a second time.
func lifecycleAcceptanceReplayedRow(event protocol.SessionEvent) bool {
	return event.DecisionReason == protocol.DecisionReasonRequestReplayed ||
		event.ReducerReason == protocol.DecisionReasonRequestReplayed ||
		event.SuppressionReason == protocol.DecisionReasonRequestReplayed
}

// lifecycleAcceptanceHoldOutcomes returns the terminal-stop-hold outcomes a
// window recorded, in order, or nil while nothing has been held yet. Replayed
// clones are excluded: the fixture reuses its request ID across retries, so a
// retry under load would otherwise read as an extra hold.
func lifecycleAcceptanceHoldOutcomes(events []protocol.SessionEvent) []protocol.StopHoldOutcome {
	var out []protocol.StopHoldOutcome
	for _, event := range events {
		if event.Event == protocol.EventTerminalStopHold && !lifecycleAcceptanceReplayedRow(event) {
			out = append(out, event.StopHoldOutcome)
		}
	}
	return out
}

// lifecycleAcceptanceSettledStops counts the rows that actually painted a stop.
// Synthetic hold rows and replayed clones carry action "stop" as well but apply
// nothing.
func lifecycleAcceptanceSettledStops(events []protocol.SessionEvent) int {
	settled := 0
	for _, event := range events {
		if event.Action == "stop" && event.Event != protocol.EventTerminalStopHold &&
			!lifecycleAcceptanceReplayedRow(event) {
			settled++
		}
	}
	return settled
}

// TestLifecycleAcceptanceOutcomeHelpersIgnoreReplayedRows pins the exclusion the
// lifecycle outcome counters depend on, without waiting for the retry that
// produces a replayed row to actually happen under load. The row shape mirrors
// daemon.Handler.RecordDuplicateDelivery: the prior row is cloned, so its event,
// action, and hold outcome survive, and only the decision reasons say that
// nothing was applied.
func TestLifecycleAcceptanceOutcomeHelpersIgnoreReplayedRows(t *testing.T) {
	replayed := func(event protocol.SessionEvent) protocol.SessionEvent {
		event.Suppressed = true
		event.SuppressionReason = protocol.DecisionReasonRequestReplayed
		event.ReducerDecision = protocol.DecisionDuplicate
		event.ReducerReason = protocol.DecisionReasonRequestReplayed
		event.DecisionReason = protocol.DecisionReasonRequestReplayed
		return event
	}
	held := protocol.SessionEvent{
		Event: protocol.EventTerminalStopHold, Action: "stop",
		StopHoldOutcome: protocol.StopHoldOutcomeHeld,
	}
	bypassed := protocol.SessionEvent{
		Event: protocol.EventTerminalStopHold, Action: "stop",
		StopHoldOutcome: protocol.StopHoldOutcomeBypassed,
	}
	settledStop := protocol.SessionEvent{Event: protocol.EventHarnessProcessExited, Action: "stop"}

	rows := []protocol.SessionEvent{held, replayed(held), settledStop, replayed(settledStop), bypassed}

	outcomes := lifecycleAcceptanceHoldOutcomes(rows)
	if len(outcomes) != 2 || outcomes[0] != protocol.StopHoldOutcomeHeld ||
		outcomes[1] != protocol.StopHoldOutcomeBypassed {
		t.Fatalf("hold outcomes = %v, want [held bypassed]", outcomes)
	}
	if got := lifecycleAcceptanceSettledStops(rows); got != 1 {
		t.Fatalf("settled stops = %d, want exactly 1", got)
	}
}

func assertLifecycleAcceptancePipeResult(
	t *testing.T,
	export protocol.EventsExport,
	windowID int,
	identity protocol.HarnessProcessIdentity,
	wantIcon string,
	wantObservation probes.HarnessProcessInspection,
) {
	t.Helper()
	exit, ok := lifecycleAcceptanceExit(export.EventsByWindow[windowID], identity.PID)
	if !ok {
		t.Fatalf("window %d has no exit for %+v", windowID, identity)
	}
	meta := export.WindowMetadata[windowID]
	if exit.Reason != state.HarnessProcessExitReasonPipe || exit.ProcessMode != "pipe" ||
		exit.ProcessHarness != identity.Harness || exit.ProcessStartToken != identity.StartToken ||
		exit.ProcessObservation != string(wantObservation) || exit.WithdrawnFacts == nil || *exit.WithdrawnFacts < 1 ||
		exit.SynthesizedStop == nil || *exit.SynthesizedStop || meta.Shadow.EffectiveIcon != wantIcon ||
		!lifecycleAcceptanceRenderComplete(meta, exit.RenderGeneration) {
		t.Fatalf("window %d pipe exit = %+v metadata=%+v", windowID, exit, meta)
	}
}

func lifecycleAcceptanceRenderComplete(meta protocol.WindowDebugMetadata, generation uint64) bool {
	if generation == 0 || meta.Renderer.RenderedGen < generation {
		return false
	}
	for _, trace := range meta.Renderer.Generations {
		if trace.Generation == generation && trace.Stage == "rendered" && trace.Outcome == "success" {
			return true
		}
	}
	return false
}
