package daemon

import (
	"testing"
	"testing/synctest"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestCodexNewClearsCompletionWithTruncatedFooterOnEitherBackend(t *testing.T) {
	fresh := readCodexCapture(t, "codex_new_narrow.txt")
	finished := tmuxCapture(t, "codex_idle_completed_t0.txt")
	eachProbedBackendOf(t, probedCodex, func(t *testing.T, s *probedSession) {
		for range 2 {
			now := codexStopped(t, s)
			notified := s.rec.len()
			s.show(fresh)
			s.showHistory(finished + fresh)
			now = s.quietRound(now)
			if got := s.icon(); got != "stop" {
				t.Fatalf("old transcript must preserve completion, got %q", got)
			}
			s.showHistory(fresh)
			now = s.quietRound(now)
			if got := s.icon(); got != "" {
				t.Fatalf("fresh /new must clear completion before another prompt, got %q", got)
			}
			s.quietRound(now)
			if got := s.icon(); got != "" {
				t.Fatalf("completion reappeared after /new: %q", got)
			}
			if got := s.rec.len(); got != notified {
				t.Fatalf("reset sent a completion notification: %d -> %d", notified, got)
			}
			detail := s.h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: s.slot})
			ev, ok := lastEventNamed(detail.Events, screenClearProbeEvent)
			if !ok || !ev.ResetFlow || ev.CurrentIconState != "stop" || ev.ResultIconState != "" {
				t.Fatalf("missing audited reset: %+v", ev)
			}
			// The next iteration must still produce a normal working -> stop
			// transition; the reset must not permanently suppress completion.
		}
	})
}

func TestCodexNewObservationCannotClearANewerTurn(t *testing.T) {
	fresh := readCodexCapture(t, "codex_new_narrow.txt")
	synctest.Test(t, func(t *testing.T) {
		s, screens := newProbedTmuxSessionOf(t, probedCodex)
		now := codexStopped(t, s)
		s.show(fresh)
		s.showHistory(fresh)
		injected := false
		screens.set(func(p *fakePaneScreens) {
			p.duringCapture = func(p *fakePaneScreens) {
				injected = true
				s.startTurn()
			}
		})
		s.quietRound(now)
		if !injected || s.icon() != "bolt" {
			t.Fatalf("new turn was cleared by old /new observation: injected=%v, icon=%q", injected, s.icon())
		}
		if got := countEventsNamed(t, s.h, s.slot, screenClearProbeEvent); got != 0 {
			t.Fatalf("stale reset produced %d clear events", got)
		}
	})
}
