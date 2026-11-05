package daemon

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// A transcript can quote the same background count the live footer displays.
// Once that footer disappears, the historical quote must neither extend a
// native Stop's deadline nor prevent recovery when its hook was lost.
func TestCodexBackgroundCompletionIgnoresHistoricalActivity(t *testing.T) {
	const footer = "  1 background terminal running · /ps to view · /stop to close\n"
	done := "• The old screen said: 1 background terminal running · /ps to view · /stop to close\n" +
		"• Press Esc to interrupt the command.\n" + tmuxCapture(t, "codex_idle_completed_t0.txt")
	running := strings.Replace(done, "› Ask Codex to do anything", footer+"\n› Ask Codex to do anything", 1)
	for _, nativeStop := range []bool{true, false} {
		name := "lost stop hook"
		if nativeStop {
			name = "native stop hook"
		}
		t.Run(name, func(t *testing.T) {
			eachProbedTmuxOf(t, probedCodex, func(t *testing.T, s *probedSession) {
				s.show(running)
				s.startTurn()
				if nativeStop {
					s.stopHeld()
				}
				for elapsed := time.Duration(0); elapsed < 4*terminalHoldTestTimeout; elapsed += 2 * time.Second {
					s.tick(time.Now())
					time.Sleep(2 * time.Second)
					synctest.Wait()
					if icon := s.icon(); icon != "bolt" {
						t.Fatalf("icon with a live background terminal = %q; want bolt", icon)
					}
				}
				if got := s.rec.len(); got != 0 {
					t.Fatalf("notifications while background work runs = %d; want 0", got)
				}

				s.show(done)
				for range 2 {
					s.tick(time.Now())
					time.Sleep(2 * time.Second)
					synctest.Wait()
				}
				if icon := s.icon(); icon != "stop" {
					t.Fatalf("icon after the background footer disappears = %q; want stop after two reads", icon)
				}
				if nativeStop {
					ev := lastStopEvent(t, s.h, s.slot)
					if ev.StopHoldOutcome != protocol.StopHoldOutcomeConfirmed || ev.StopConfirmedBy != protocol.StopConfirmedByCodexScreen {
						t.Fatalf("released stop = %q by %q; want confirmed by the Codex screen", ev.StopHoldOutcome, ev.StopConfirmedBy)
					}
				}
				time.Sleep(5 * time.Second)
				synctest.Wait()
				if got := s.rec.len(); got != 1 {
					t.Fatalf("notifications after completion = %d; want 1", got)
				}
			})
		})
	}
}

// A Claude turn that ends with a background shell still running sends an honest
// Stop, but there is nothing to answer yet: the mode line still counts the
// shell. The held stop waits for as long as that count is on screen, well past
// its deadline and the active watchdog, and paints only once the probe confirms
// the pane with the shell gone.
func TestHeldStopWaitsForABackgroundShellOnTmux(t *testing.T) {
	running := readCodexCapture(t, "claude_idle_background_shell.txt")
	done := readCodexCapture(t, "claude_idle_background_shell_done.txt")
	eachProbedTmux(t, func(t *testing.T, s *probedSession) {
		s.h.SetStaleThresholds(map[string]time.Duration{"active": 10 * time.Second}, nil)
		s.show(running)
		s.startTurn()
		s.stopHeld()

		// Four deadlines' worth of a pane that keeps counting the shell.
		for elapsed := time.Duration(0); elapsed < 4*terminalHoldTestTimeout; elapsed += 2 * time.Second {
			s.tick(time.Now())
			s.h.sweepStale(time.Now(), 2*time.Second, false, false)
			time.Sleep(2 * time.Second)
			synctest.Wait()
		}
		if icon := s.icon(); icon != "bolt" {
			t.Fatalf("icon while the shell is still counted = %q; want bolt", icon)
		}
		if got := s.rec.len(); got != 0 {
			t.Fatalf("notifications while the shell runs = %d; want 0 (%+v)", got, s.rec.snapshot())
		}
		pending, held := terminalHoldPending(t, s.h, s.slot)
		if !held || !pending.backgroundWork || pending.confirmed {
			t.Fatalf("hold = %+v/%v; want an unconfirmed hold waiting on background work", pending, held)
		}
		s.h.stateMu.Lock()
		snapshot := s.h.pendingTerminalStopSnapshotLocked(s.slot)
		s.h.stateMu.Unlock()
		if snapshot == nil || !snapshot.BackgroundWork {
			t.Fatalf("exported hold = %+v; want background_work", snapshot)
		}

		// The finished line still says "1 shell still running"; only the mode
		// line's count is live, and it is gone.
		s.show(done)
		now := time.Now()
		for i := range 4 {
			s.tick(now.Add(time.Duration(i) * time.Second))
		}
		synctest.Wait()
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon once the shell is gone = %q; want stop", icon)
		}
		if got := s.rec.len(); got != 1 {
			t.Fatalf("notifications = %d; want 1 (%+v)", got, s.rec.snapshot())
		}
		ev := lastStopEvent(t, s.h, s.slot)
		if ev.StopHoldOutcome != protocol.StopHoldOutcomeConfirmed || ev.StopConfirmedBy != protocol.StopConfirmedByClaudeScreen {
			t.Fatalf("released stop = %q by %q; want confirmed by the Claude screen", ev.StopHoldOutcome, ev.StopConfirmedBy)
		}
	})
}

// Background work only holds a stop while the pane can be read. Once it cannot,
// nothing pushes the deadline back and the native stop paints a full timeout
// after the last reading that showed the work.
func TestHeldStopOverBackgroundWorkIsReleasedWhenThePaneGoesUnreadable(t *testing.T) {
	running := readCodexCapture(t, "claude_idle_background_shell.txt")
	eachProbedTmux(t, func(t *testing.T, s *probedSession) {
		s.show(running)
		s.startTurn()
		s.stopHeld()
		for range 10 {
			s.tick(time.Now())
			time.Sleep(2 * time.Second)
			synctest.Wait()
		}
		if icon := s.icon(); icon != "bolt" {
			t.Fatalf("icon while the shell is counted = %q; want bolt", icon)
		}

		s.unreadable()
		for elapsed := time.Duration(0); elapsed <= terminalHoldTestTimeout; elapsed += 2 * time.Second {
			s.tick(time.Now())
			time.Sleep(2 * time.Second)
			synctest.Wait()
		}
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon a timeout after the pane went unreadable = %q; want stop", icon)
		}
		ev := lastStopEvent(t, s.h, s.slot)
		if ev.StopHoldOutcome != protocol.StopHoldOutcomeDeadline {
			t.Fatalf("released stop = %q; want the deadline", ev.StopHoldOutcome)
		}
	})
}
