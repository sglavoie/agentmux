package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// The three probes that used to read Kitty only: Claude's pane title, the
// Codex cleared screen, and the Copilot transcript. Each scenario runs on a
// Kitty window and on a tmux runtime session through the production probe
// tick, over the screens, title and history captured from live tmux panes
// (internal/screenstate/testdata/tmux), and has to end the same way on both.

// tmuxCapture reads one committed tmux capture, byte for byte.
func tmuxCapture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "screenstate", "testdata", "tmux", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tmux capture %s: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("tmux capture %s is empty", path)
	}
	return string(data)
}

// tmuxCapturedTitle is a captured pane title as tmux reports it: one line.
func tmuxCapturedTitle(t *testing.T, name string) string {
	t.Helper()
	return strings.TrimSuffix(tmuxCapture(t, name+".title"), "\n")
}

// quietRound runs enough ticks for a session on the quiet cadence to be read
// once, and returns the instant after them.
func (s *probedSession) quietRound(from time.Time) time.Time {
	for range runtimeQuietProbeEvery {
		s.tick(from)
		from = from.Add(time.Second)
	}
	return from
}

func (s *probedSession) lastActiveEvent() string {
	s.h.stateMu.Lock()
	defer s.h.stateMu.Unlock()
	return s.h.store[s.slot].LastActiveEvent
}

// ticksUntilStop counts the probe ticks an idle screen takes to settle a
// bolt, giving up well past any sample policy.
func (s *probedSession) ticksUntilStop(from time.Time) int {
	s.t.Helper()
	for tick := 1; tick <= 12; tick++ {
		s.tick(from.Add(time.Duration(tick) * time.Second))
		if s.icon() == "stop" {
			return tick
		}
	}
	s.t.Fatalf("the idle screen never settled; icon %q", s.icon())
	return 0
}

// A turn whose hooks never arrived leaves a session with no icon while
// Claude's pane title names the turn. The title restores the bolt once; the
// screen then settles it, and a title still carrying its marker costs the
// settle one more agreeing read than a resting title does.
func TestClaudePaneTitleRecoversAndPacesAStuckTurnOnEitherBackend(t *testing.T) {
	streaming := tmuxCapture(t, "claude_working_streaming_titled.txt")
	finished := tmuxCapture(t, "claude_idle_completed_titled.txt")
	working := tmuxCapturedTitle(t, "claude_working_streaming_titled")
	resting := tmuxCapturedTitle(t, "claude_idle_completed_titled")
	if !activity.ClaudeWindowTitleHasDescriptionMarker(working) || !activity.ClaudeTitleHasMarker(resting) {
		t.Fatalf("captured titles %q and %q no longer carry Claude's marker over a turn description", working, resting)
	}
	generic := "✳ " + activity.ClaudeRestingTitleText

	eachProbedBackend(t, func(t *testing.T, s *probedSession) {
		if resp := s.hook("SessionStart"); !resp.OK {
			t.Fatalf("SessionStart = %+v", resp)
		}
		s.show(streaming)

		// Claude's generic title says nothing about a turn.
		s.retitle(generic)
		now := s.quietRound(time.Now())
		if icon := s.icon(); icon != "" {
			t.Fatalf("icon under the generic title = %q; want none", icon)
		}

		// A streaming frame reads like a finished one (see
		// TestTmuxClaudeStreamingTurnIsOnlyHeldByItsFingerprint), so the
		// scenario moves on at the tick the title acts on.
		s.retitle(working)
		for tick := 0; s.icon() == "" && tick < runtimeQuietProbeEvery; tick++ {
			s.tick(now)
			now = now.Add(time.Second)
		}
		if icon, event := s.icon(), s.lastActiveEvent(); icon != "bolt" || event != screenActiveProbeEvent {
			t.Fatalf("icon %q after %q; want the title to restore the bolt as %s", icon, event, screenActiveProbeEvent)
		}
		if got := s.rec.len(); got != 0 {
			t.Fatalf("the recovery notified %d times; want 0", got)
		}

		// The turn ends and the title stays, as Claude leaves it.
		s.show(finished)
		s.retitle(resting)
		want := activity.ClaudeIdleProbeQuiescentSamples + activity.ClaudeIdleProbeMarkerSampleBump
		if got := s.ticksUntilStop(now); got != want {
			t.Fatalf("the finished screen settled after %d reads under a marked title; want %d", got, want)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if got := s.rec.len(); got != 1 {
			t.Fatalf("notifications = %d; want 1 (%+v)", got, s.rec.snapshot())
		}
	})

	// The same settle with no marker on the title, and so no bump.
	eachProbedBackend(t, func(t *testing.T, s *probedSession) {
		s.show(finished)
		s.retitle("claude")
		s.startTurn()
		if got := s.ticksUntilStop(time.Now()); got != activity.ClaudeIdleProbeQuiescentSamples {
			t.Fatalf("the finished screen settled after %d reads under an unmarked title; want %d",
				got, activity.ClaudeIdleProbeQuiescentSamples)
		}
	})
}

// A title is evidence about the registered pane under the same rules as its
// screen: one read from another server generation restores nothing.
func TestTmuxPaneTitleFromAnotherGenerationRecoversNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, screens := newProbedTmuxSession(t)
		s.show(tmuxCapture(t, "claude_working_streaming_titled.txt"))
		s.retitle(tmuxCapturedTitle(t, "claude_working_streaming_titled"))
		screens.set(func(p *fakePaneScreens) { p.generation = "tmux-t-2" })
		s.quietRound(time.Now())
		if icon := s.icon(); icon != "" {
			t.Fatalf("icon = %q; a title read from another generation restored it", icon)
		}
		screens.mu.Lock()
		reads := screens.reads
		screens.mu.Unlock()
		if reads == 0 {
			t.Fatal("fixture: the pane was never read")
		}
	})
}

// codexStopped drives a Codex session through a turn to a confirmed stop.
func codexStopped(t *testing.T, s *probedSession) time.Time {
	t.Helper()
	s.show(tmuxCapture(t, "codex_idle_completed_t0.txt"))
	s.startTurn()
	s.stopHeld()
	now := time.Now()
	for range 4 {
		s.tick(now)
		now = now.Add(time.Second)
	}
	if !s.confirmed() {
		t.Fatal("the finished Codex screen never confirmed the held stop")
	}
	time.Sleep(terminalHoldTestDelay)
	synctest.Wait()
	if icon := s.icon(); icon != "stop" {
		t.Fatalf("icon after the confirmed stop = %q; want stop", icon)
	}
	return now
}

// A /clear typed at a stopped Codex session takes its stop away, and Codex
// reports no hook for it: the cleared screen is the only evidence. The screen
// must look cleared and the pane's history must hold no transcript.
func TestCodexClearedScreenClearsAStoppedSessionOnEitherBackend(t *testing.T) {
	cleared := tmuxCapture(t, "codex_cleared.txt")
	clearedHistory := tmuxCapture(t, "codex_cleared.all.txt")
	finished := tmuxCapture(t, "codex_idle_completed_t0.txt")

	eachProbedBackendOf(t, probedCodex, func(t *testing.T, s *probedSession) {
		now := codexStopped(t, s)
		notified := s.rec.len()

		// The finished turn is still on screen: nothing to clear.
		s.showHistory(finished)
		now = s.quietRound(now)
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon with the turn still on screen = %q; want stop", icon)
		}

		// A cleared screen above a history that still holds the turn.
		s.show(cleared)
		s.showHistory(finished + cleared)
		now = s.quietRound(now)
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon with the turn still in the history = %q; want stop", icon)
		}

		s.showHistory(clearedHistory)
		s.quietRound(now)
		if icon := s.icon(); icon != "" {
			t.Fatalf("icon after /clear = %q; want none", icon)
		}
		detail := s.h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: s.slot})
		ev, ok := lastEventNamed(detail.Events, screenClearProbeEvent)
		if !ok || ev.Action != "user_clear" || !ev.ResetFlow || ev.CurrentIconState != "stop" || ev.ResultIconState != "" {
			t.Fatalf("clear event = %+v (%v); want user_clear stop->none in a reset flow", ev, ok)
		}
		if got := s.rec.len(); got != notified {
			t.Fatalf("the clear notified: %d notifications, was %d", got, notified)
		}
	})
}

// On tmux the history is a second read, and a capture names no generation.
// It counts only when the registered server still answers for the pane after
// it; and a stopped session is read on the quiet cadence, not every tick.
func TestTmuxCodexClearReadsTheHistoryOfTheRegisteredPaneOnly(t *testing.T) {
	cleared := tmuxCapture(t, "codex_cleared.txt")
	clearedHistory := tmuxCapture(t, "codex_cleared.all.txt")
	for name, spoil := range map[string]func(*fakePaneScreens){
		"the server restarted during the capture": func(p *fakePaneScreens) {
			p.duringCapture = func(p *fakePaneScreens) { p.generation = "tmux-t-2" }
		},
		"the agent exited during the capture": func(p *fakePaneScreens) {
			p.duringCapture = func(p *fakePaneScreens) { p.exited = true }
		},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, screens := newProbedTmuxSessionOf(t, probedCodex)
				now := codexStopped(t, s)
				s.show(cleared)
				s.showHistory(clearedHistory)
				screens.set(spoil)
				s.quietRound(now)
				screens.mu.Lock()
				captures := screens.captures
				screens.mu.Unlock()
				if captures != 1 {
					t.Fatalf("%d history reads; want the one that was spoiled", captures)
				}
				if icon := s.icon(); icon != "stop" {
					t.Fatalf("icon = %q; a history read nothing vouches for cleared the stop", icon)
				}
			})
		})
	}

	t.Run("cadence", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, screens := newProbedTmuxSessionOf(t, probedCodex)
			now := codexStopped(t, s)
			counts := func() (int, int) {
				screens.mu.Lock()
				defer screens.mu.Unlock()
				return screens.reads, screens.captures
			}
			reads, captures := counts()
			for range 2 * runtimeQuietProbeEvery {
				s.tick(now)
				now = now.Add(time.Second)
			}
			afterReads, afterCaptures := counts()
			if got := afterReads - reads; got != 2 {
				t.Fatalf("%d reads of a stopped Codex session over %d ticks; want one every %d", got, 2*runtimeQuietProbeEvery, runtimeQuietProbeEvery)
			}
			if afterCaptures != captures {
				t.Fatalf("a screen that does not look cleared cost %d history reads", afterCaptures-captures)
			}

			// A history that cannot be captured clears nothing.
			s.show(cleared)
			s.showHistory(clearedHistory)
			screens.set(func(p *fakePaneScreens) {
				p.duringCapture = func(p *fakePaneScreens) { p.err = errors.New("capture-pane failed") }
			})
			s.quietRound(now)
			if icon := s.icon(); icon != "stop" {
				t.Fatalf("icon = %q after a failed history read; want stop", icon)
			}
		})
	})
}

// probedCopilotSession gives the session's daemon a Copilot home holding the
// transcript of its conversation.
func probedCopilotTranscript(t *testing.T, s *probedSession, content string) string {
	t.Helper()
	home := t.TempDir()
	writeCopilotTranscript(t, home, "conv", content)
	s.h.SetCopilotTranscriptHomes([]string{home}, "")
	return home
}

// Copilot's stop is confirmed by its transcript, and its pane may refuse:
// a permission dialog on screen means a human is being waited on, whatever
// the transcript's tail says.
func TestCopilotHeldStopIsConfirmedByItsTranscriptOnEitherBackend(t *testing.T) {
	prompt := tmuxCapture(t, "copilot_permission_prompt.txt")
	finished := tmuxCapture(t, "copilot_idle_completed_t0.txt")

	eachProbedBackendOf(t, probedCopilot, func(t *testing.T, s *probedSession) {
		probedCopilotTranscript(t, s, copilotTurnEndTranscript)
		s.show(prompt)
		s.startTurn()
		s.stopHeld()
		now := time.Now()
		for range 3 {
			s.tick(now)
			now = now.Add(time.Second)
		}
		if s.confirmed() {
			t.Fatal("a finished transcript confirmed the stop over a permission dialog")
		}

		s.show(finished)
		s.tick(now)
		if s.confirmed() {
			t.Fatal("one transcript read confirmed the stop; a turn end needs two that agree")
		}
		s.tick(now.Add(time.Second))
		pending, _ := terminalHoldPending(t, s.h, s.slot)
		if !pending.confirmed || pending.confirmedBy != protocol.StopConfirmedByCopilotTranscript {
			t.Fatalf("hold after two reads = %+v; want confirmed by %s", pending, protocol.StopConfirmedByCopilotTranscript)
		}

		time.Sleep(terminalHoldTestDelay)
		synctest.Wait()
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon after the confirmed hold = %q; want stop", icon)
		}
		if got := s.rec.len(); got != 1 {
			t.Fatalf("notifications = %d; want 1 (%+v)", got, s.rec.snapshot())
		}
		detail := s.h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: s.slot})
		ev, ok := lastEventNamed(detail.Events, "agentStop")
		if !ok || ev.StopHoldOutcome != protocol.StopHoldOutcomeConfirmed || ev.StopConfirmedBy != protocol.StopConfirmedByCopilotTranscript {
			t.Fatalf("released stop = %+v (%v); want confirmed by the Copilot transcript", ev, ok)
		}
		assertNoProbeStopFact(t, s.h, s.slot)
	})
}

// A Copilot turn whose stop hook never arrived is settled from its
// transcript, and a transcript that grows afterwards takes that stop back:
// the route to the settled session is found again with no live producer.
func TestCopilotTranscriptSettlesAndRecoversAStuckTurnOnEitherBackend(t *testing.T) {
	finished := tmuxCapture(t, "copilot_idle_completed_t0.txt")

	eachProbedBackendOf(t, probedCopilot, func(t *testing.T, s *probedSession) {
		home := probedCopilotTranscript(t, s, copilotTurnEndTranscript)
		s.show(finished)
		s.startTurn()
		now := time.Now()

		s.tick(now)
		if icon := s.icon(); icon != "bolt" {
			t.Fatalf("icon after one transcript read = %q; want bolt", icon)
		}
		s.tick(now.Add(time.Second))
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon after two transcript reads = %q; want stop", icon)
		}
		detail := s.h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: s.slot})
		if ev, ok := lastEventNamed(detail.Events, copilotTranscriptStopProbeEvent); !ok || ev.Action != "stop" {
			t.Fatalf("settle event = %+v (%v); want a transcript stop", ev, ok)
		}

		// The stop was real: more reads leave it alone.
		s.tick(now.Add(2 * time.Second))
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon after a third read of the same transcript = %q; want stop", icon)
		}

		// Copilot went on working after all.
		appendCopilotTranscript(t, home, "conv", `{"type":"assistant.turn_start","data":{}}`+"\n"+`{"type":"tool.execution_start","data":{}}`+"\n")
		s.tick(now.Add(3 * time.Second))
		if icon := s.icon(); icon != "bolt" {
			t.Fatalf("icon after the transcript grew = %q; want the stop taken back", icon)
		}
	})
}

// The production reader against a real private tmux server, with no Kitty:
// the title a pane program set reaches the busy-title recovery, and a
// stopped Codex session is cleared from its own pane and history.
func TestRuntimeProbesReadTitleAndHistoryOfARealTmuxPane(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	tmuxDo := func(args ...string) {
		t.Helper()
		argv := append([]string{"-S", w.tmux.Server.Socket()}, args...)
		if out, err := exec.Command(w.tmux.Server.Binary(), argv...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
	}
	tickUntil := func(h *Handler, slot int, want string) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); slotIcon(h, slot) != want; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("icon %q; want %q", slotIcon(h, slot), want)
			}
			h.runScreenProbeTick(time.Now())
		}
	}

	titled := w.launch("api")
	cleared := w.launch("web")
	h, _ := w.startDaemon(time.Now())
	h.SetRuntimeScreenReader(NewTmuxRuntimeScreenReader())
	h.SetStopConfirm(0, 0, false)

	requireAccepted(t, h.Handle(runtimeHook(t, titled.id, "SessionStart", hookevent.HookPayload{SessionID: "conv"})))
	slot := slotOf(t, h, titled.id)
	for range runtimeQuietProbeEvery {
		h.runScreenProbeTick(time.Now())
	}
	if icon := slotIcon(h, slot); icon != "" {
		t.Fatalf("icon under tmux's default pane title = %q; want none", icon)
	}
	tmuxDo("select-pane", "-t", titled.launched.Pane.ID, "-T", tmuxCapturedTitle(t, "claude_working_streaming_titled"))
	tickUntil(h, slot, "bolt")

	// The stand-in agent echoes what is typed at it: a cleared Codex screen.
	hook := func(event string) {
		t.Helper()
		req := runtimeHook(t, cleared.id, event, hookevent.HookPayload{SessionID: "conv-2"})
		req.Harness = "codex"
		requireAccepted(t, h.Handle(req))
	}
	hook("UserPromptSubmit")
	hook("Stop")
	codexSlot := slotOf(t, h, cleared.id)
	// A stop nothing confirms still rides the short reset hold.
	for deadline := time.Now().Add(10 * time.Second); slotIcon(h, codexSlot) != "stop"; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("icon of the stopped session = %q; want stop", slotIcon(h, codexSlot))
		}
	}
	for range 2 * runtimeQuietProbeEvery {
		h.runScreenProbeTick(time.Now())
	}
	if icon := slotIcon(h, codexSlot); icon != "stop" {
		t.Fatalf("icon over a screen that is not cleared = %q; want stop", icon)
	}
	for _, line := range []string{"› Ask Codex to do anything", "  GPT-6-Astra high · Ready · Context 0% used"} {
		tmuxDo("send-keys", "-t", cleared.launched.Pane.ID, "-l", line)
		tmuxDo("send-keys", "-t", cleared.launched.Pane.ID, "Enter")
	}
	tickUntil(h, codexSlot, "")

	if clients := w.survey().Clients; len(clients) != 0 {
		t.Fatalf("reading the panes attached %d terminals", len(clients))
	}
	w.requireSameAgent(titled, "its title was read")
	w.requireSameAgent(cleared, "its history was read")
	if health := h.runtimeHealthSnapshot(time.Now()); health.Capture.Reads == 0 || health.Capture.Failures != 0 {
		t.Fatalf("capture health = %+v; want the panes read without failure", health.Capture)
	}
}
