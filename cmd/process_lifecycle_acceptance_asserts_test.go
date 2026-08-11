package cmd

import (
	"fmt"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon"
)

func lifecycleAcceptanceIdentity(harnessName string, pid int) daemon.HarnessProcessIdentity {
	return daemon.HarnessProcessIdentity{
		Harness: harnessName, PID: pid,
		StartToken: fmt.Sprintf("Thu Jul 16 10:%02d:%02d 2026", pid%60, (pid/60)%60),
	}
}

func lifecycleAcceptanceFact(
	meta daemon.WindowDebugMetadata,
	kind daemon.SourceKind,
	key string,
) *daemon.SourceFactSnapshot {
	for i := range meta.Shadow.Facts {
		fact := meta.Shadow.Facts[i]
		if fact.Kind == kind && fact.Key == key {
			return &fact
		}
	}
	return nil
}

func lifecycleAcceptanceExit(events []daemon.SessionEvent, pid int) (daemon.SessionEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event == daemon.EventHarnessProcessExited && events[i].ProcessPID == pid {
			return events[i], true
		}
	}
	return daemon.SessionEvent{}, false
}

// lifecycleAcceptanceHoldOutcomes returns the terminal-stop-hold outcomes a
// window recorded, in order, or nil while nothing has been held yet.
func lifecycleAcceptanceHoldOutcomes(events []daemon.SessionEvent) []daemon.StopHoldOutcome {
	var out []daemon.StopHoldOutcome
	for _, event := range events {
		if event.Event == daemon.EventTerminalStopHold {
			out = append(out, event.StopHoldOutcome)
		}
	}
	return out
}

// lifecycleAcceptanceSettledStops counts the rows that actually painted a stop.
// Synthetic hold rows carry action "stop" as well but apply nothing.
func lifecycleAcceptanceSettledStops(events []daemon.SessionEvent) int {
	settled := 0
	for _, event := range events {
		if event.Action == "stop" && event.Event != daemon.EventTerminalStopHold {
			settled++
		}
	}
	return settled
}

func assertLifecycleAcceptancePipeResult(
	t *testing.T,
	export daemon.EventsExport,
	windowID int,
	identity daemon.HarnessProcessIdentity,
	wantIcon string,
	wantObservation daemon.HarnessProcessInspection,
) {
	t.Helper()
	exit, ok := lifecycleAcceptanceExit(export.EventsByWindow[windowID], identity.PID)
	if !ok {
		t.Fatalf("window %d has no exit for %+v", windowID, identity)
	}
	meta := export.WindowMetadata[windowID]
	if exit.Reason != daemon.HarnessProcessExitReasonPipe || exit.ProcessMode != "pipe" ||
		exit.ProcessHarness != identity.Harness || exit.ProcessStartToken != identity.StartToken ||
		exit.ProcessObservation != string(wantObservation) || exit.WithdrawnFacts == nil || *exit.WithdrawnFacts < 1 ||
		exit.SynthesizedStop == nil || *exit.SynthesizedStop || meta.Shadow.EffectiveIcon != wantIcon ||
		!lifecycleAcceptanceRenderComplete(meta, exit.RenderGeneration) {
		t.Fatalf("window %d pipe exit = %+v metadata=%+v", windowID, exit, meta)
	}
}

func lifecycleAcceptanceRenderComplete(meta daemon.WindowDebugMetadata, generation uint64) bool {
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
