package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestProbeCopilotTranscriptStop_ConfirmsHeldStopInsteadOfSettling is the
// Copilot member of the confirmation set. Copilot reads a transcript rather than
// a screen, but the seam is the same one: two agreeing reads at an unchanged
// transcript generation confirm the withheld agentStop instead of settling a
// synthetic transcript stop.
func TestProbeCopilotTranscriptStop_ConfirmsHeldStopInsteadOfSettling(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, _, _ := copilotProbeHandlerNotifier(t, 42, "bolt", home)
	releaseHoldAtTestEnd(t, h, 42)
	base := time.Now()
	armCopilotTerminalHold(t, h, 42, "sess-1")

	h.ProbeCopilotTranscriptStop(base)
	if pending, ok := terminalHoldPending(t, h, 42); !ok || pending.confirmed {
		t.Fatalf("hold after one transcript read = %+v/%v, want an unconfirmed hold", pending, ok)
	}

	h.ProbeCopilotTranscriptStop(base)
	pending, ok := terminalHoldPending(t, h, 42)
	if !ok || !pending.confirmed || pending.confirmedBy != protocol.StopConfirmedByCopilotTranscript {
		t.Fatalf("hold after two transcript reads = %+v/%v, want confirmed by %s", pending, ok, protocol.StopConfirmedByCopilotTranscript)
	}
	assertNoProbeStopFact(t, h, 42)
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("icon while the confirmed hold waits = %q, want bolt", eff.Icon)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
		t.Fatalf("probe recorded %+v; a confirmation is not a synthetic stop", ev)
	}
}

// TestProbeCopilotTranscriptStop_AgentStopStillConvergesToStop is the
// end-to-end non-regression the confirmed-idle gate owes Copilot: a native
// agentStop still converges the tab to 🛑 and still notifies exactly once. The
// gate only changes when — after the transcript confirms the turn really
// ended — and stamps that provenance on the committed stop.
func TestProbeCopilotTranscriptStop_AgentStopStillConvergesToStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
		h, _, rec := copilotProbeHandlerNotifier(t, 42, "bolt", home)
		base := time.Now()
		armCopilotTerminalHold(t, h, 42, "sess-1")

		settleCopilotTurnEndProbe(h, base)
		time.Sleep(defaultStopConfirmDelay)
		synctest.Wait()

		if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
			t.Fatalf("effective = %+v visible %q, want stop/stop for a native agentStop", eff, visible)
		}
		if calls := rec.snapshot(); len(calls) != 1 {
			t.Fatalf("stop notifications = %d, want exactly 1", len(calls))
		}
		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		ev, ok := lastEventNamed(resp.Events, "agentStop")
		if !ok {
			t.Fatalf("no committed agentStop row in %+v", resp.Events)
		}
		if ev.StopHoldOutcome != protocol.StopHoldOutcomeConfirmed || ev.StopConfirmedBy != protocol.StopConfirmedByCopilotTranscript {
			t.Fatalf("committed agentStop = %+v, want outcome %s confirmed by %s", ev, protocol.StopHoldOutcomeConfirmed, protocol.StopConfirmedByCopilotTranscript)
		}
		assertNoProbeStopFact(t, h, 42)
	})
}

// TestProbeCopilotTranscriptStop_StableCompletionNotifiesOnce is the positive
// twin: a genuinely final assistant.turn_end (the transcript stops growing) passes
// the stabilization policy AND the post-settle ConfirmNotify re-read still reads
// terminal, so exactly one "Session stopped" notification dispatches. This guards
// the suppression above from silently swallowing genuine completions.
func TestProbeCopilotTranscriptStop_StableCompletionNotifiesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
		h, _, rec := copilotProbeHandlerNotifier(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		settleCopilotTurnEndProbe(h, base)
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective = %+v, want stop for a genuinely stable completion", eff)
		}
		calls := rec.snapshot()
		if len(calls) != 1 {
			t.Fatalf("stop notifications = %d, want exactly 1 for a stable completion", len(calls))
		}
		if calls[0].subtitle != "Session stopped" {
			t.Fatalf("notification subtitle = %q, want %q", calls[0].subtitle, "Session stopped")
		}
	})
}
