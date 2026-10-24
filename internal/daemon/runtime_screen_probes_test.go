package daemon

import (
	"errors"
	"os/exec"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// The stop hold, the idle probe that confirms it, and the context-limit
// banner are one behaviour whichever backend holds the agent. Each scenario
// below runs twice, on a Kitty window and on a tmux runtime session, through
// the production probe tick, and has to end the same way on both.

// probedHarness is how one harness's hooks name a turn.
type probedHarness struct {
	name         string
	prompt, stop string
	payload      func(event string) hookevent.HookPayload
}

var (
	probedClaude = probedHarness{name: harness.Claude, prompt: "UserPromptSubmit", stop: "Stop",
		payload: func(string) hookevent.HookPayload { return hookevent.HookPayload{SessionID: "conv"} }}
	probedCodex = probedHarness{name: harness.Codex, prompt: "UserPromptSubmit", stop: "Stop",
		payload: func(string) hookevent.HookPayload { return hookevent.HookPayload{SessionID: "conv"} }}
	probedCopilot = probedHarness{name: harness.Copilot, prompt: "userPromptSubmitted", stop: "agentStop",
		payload: func(event string) hookevent.HookPayload {
			payload := hookevent.HookPayload{SessionIDCamel: "conv"}
			if event == "agentStop" {
				payload.StopReasonCamel = "end_turn"
			}
			return payload
		}}
)

// probedSession is one agent on one backend, with a screen the test controls.
type probedSession struct {
	t       *testing.T
	h       *Handler
	rec     *callRecorder
	slot    int
	harness probedHarness
	// show puts text on the agent's screen; unreadable makes the next reads
	// fail, which is a pane with no capture.
	show       func(text string)
	unreadable func()
	// retitle sets the title the agent's program gave its pane, and
	// showHistory what the pane's screen reads as together with its history.
	retitle     func(title string)
	showHistory func(text string)
	hook        func(event string) protocol.Response
	// tick is one pass of the daemon's screen probes.
	tick func(now time.Time)
	// end closes the session the way its backend reports it gone: Kitty
	// lists the window no more, or the registry records the runtime session
	// ended and maintenance retires its slot.
	end func()
}

const probedGeneration = "tmux-t-1"

// fakePaneScreens is a tmux server's screens as the daemon's reader sees
// them: one pane, whose text, generation and liveness the test sets.
type fakePaneScreens struct {
	mu         sync.Mutex
	text       string
	title      string
	history    string
	err        error
	generation string
	exited     bool
	reads      int
	// captures counts whole-history reads; duringCapture runs inside one,
	// under the lock, as whatever happened to the server meanwhile.
	captures      int
	duringCapture func(*fakePaneScreens)
}

func (s *fakePaneScreens) Capture(pane backend.Ref, extent backend.Extent) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if extent != backend.ExtentAll {
		return "", errors.New("the probes capture a pane only with its history")
	}
	s.captures++
	if s.err != nil {
		return "", s.err
	}
	if s.duringCapture != nil {
		s.duringCapture(s)
	}
	return s.history, nil
}

func (s *fakePaneScreens) Observe(pane backend.Ref) (backend.Screen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.err != nil {
		return backend.Screen{}, s.err
	}
	return backend.Screen{
		Instance:   runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: tmuxT, Generation: s.generation},
		Pane:       pane,
		Text:       s.text,
		Title:      s.title,
		Exited:     s.exited,
		CapturedAt: time.Now(),
	}, nil
}

func (s *fakePaneScreens) set(change func(*fakePaneScreens)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(s)
}

func newProbedHandler(t *testing.T) (*Handler, *callRecorder) {
	t.Helper()
	notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true, Error: true})
	h := NewHandler(activity.NewStateGuard(time.Hour), notifier)
	notifier.SetIsMuted(h.IsMuted)
	h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, true)
	return h, rec
}

func newProbedKittySessionOf(t *testing.T, agent probedHarness) *probedSession {
	t.Helper()
	h, rec := newProbedHandler(t)
	fake := &fakeKittyClient{
		windows:         codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{Profile: "work", Harness: agent.name}),
		getText:         map[string]string{},
		getTextByExtent: map[getTextKey]string{},
	}
	h.SetKittyClient(fake)
	return &probedSession{
		t: t, h: h, rec: rec, slot: 42, harness: agent,
		// The maintenance loop republishes the window tree between probe
		// ticks; here the test does.
		tick: func(now time.Time) {
			h.primeSnapshot()
			h.runScreenProbeTick(now)
		},
		show: func(text string) {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			fake.getText[kitty.MatchWindowByID(42)], fake.getTextErr = text, nil
		},
		unreadable: func() {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			fake.getTextErr = errors.New("get-text failed")
		},
		retitle: func(title string) {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			fake.windows[0].Tabs[0].Windows[0].Title = title
		},
		showHistory: func(text string) {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			fake.getTextByExtent[getTextKey{match: kitty.MatchWindowByID(42), extent: "all"}] = text
		},
		hook: func(event string) protocol.Response {
			return h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: event, Harness: agent.name,
				PayloadJSON: hookPayloadJSON(t, agent.payload(event))})
		},
		end: func() { h.pruneClosedWindows(time.Now(), map[int]bool{}) },
	}
}

// newProbedTmuxSession is the same agent on a tmux runtime session, in a
// daemon with no Kitty at all.
func newProbedTmuxSession(t *testing.T) (*probedSession, *fakePaneScreens) {
	t.Helper()
	return newProbedTmuxSessionOf(t, probedClaude)
}

func newProbedTmuxSessionOf(t *testing.T, agent probedHarness) (*probedSession, *fakePaneScreens) {
	t.Helper()
	h, rec := newProbedHandler(t)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	runtime := newRuntimeID(t)
	directory.put(runtime, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, probedGeneration, "%7")
	screens := &fakePaneScreens{generation: probedGeneration}
	h.SetRuntimeScreenReader(func(instance runtimesession.BackendInstance) (RuntimeScreen, error) {
		if instance.Kind != runtimesession.BackendTmux || instance.Address != tmuxT {
			t.Errorf("screen reader asked for %+v; want the session's own server", instance)
		}
		return screens, nil
	})
	session := &probedSession{
		t: t, h: h, rec: rec, harness: agent,
		tick:        h.runScreenProbeTick,
		show:        func(text string) { screens.set(func(s *fakePaneScreens) { s.text, s.err = text, nil }) },
		unreadable:  func() { screens.set(func(s *fakePaneScreens) { s.err = errors.New("capture-pane failed") }) },
		retitle:     func(title string) { screens.set(func(s *fakePaneScreens) { s.title = title }) },
		showHistory: func(text string) { screens.set(func(s *fakePaneScreens) { s.history = text }) },
		hook: func(event string) protocol.Response {
			req := runtimeHook(t, runtime, event, agent.payload(event))
			req.Harness = agent.name
			return h.Handle(req)
		},
		end: func() {
			directory.setState(runtime, runtimesession.StateEnded)
			h.retireEndedRuntimeSessions(time.Now())
		},
	}
	// The first hook is what gives the session its slot.
	if resp := session.hook("SessionStart"); !resp.OK {
		t.Fatalf("SessionStart = %+v", resp)
	}
	session.slot = slotOf(t, h, runtime)
	return session, screens
}

func eachProbedBackend(t *testing.T, scenario func(t *testing.T, s *probedSession)) {
	t.Helper()
	eachProbedBackendOf(t, probedClaude, scenario)
}

func eachProbedBackendOf(t *testing.T, agent probedHarness, scenario func(t *testing.T, s *probedSession)) {
	t.Helper()
	t.Run("kitty", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) { scenario(t, newProbedKittySessionOf(t, agent)) })
	})
	t.Run("tmux", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, _ := newProbedTmuxSessionOf(t, agent)
			scenario(t, s)
		})
	})
}

func (s *probedSession) icon() string {
	s.t.Helper()
	eff, _ := windowEffective(s.t, s.h, s.slot)
	return eff.Icon
}

// startTurn sends the prompt that puts the agent to work.
func (s *probedSession) startTurn() {
	s.t.Helper()
	if resp := s.hook(s.harness.prompt); !resp.OK {
		s.t.Fatalf("%s = %+v", s.harness.prompt, resp)
	}
	if icon := s.icon(); icon != "bolt" {
		s.t.Fatalf("icon after the prompt = %q; want bolt", icon)
	}
}

// stopHeld sends the native Stop and requires it to be held for confirmation.
func (s *probedSession) stopHeld() {
	s.t.Helper()
	if resp := s.hook(s.harness.stop); !resp.OK {
		s.t.Fatalf("%s = %+v", s.harness.stop, resp)
	}
	pending, armed := terminalHoldPending(s.t, s.h, s.slot)
	if !armed || pending.class != stopHoldClassTerminal {
		s.t.Fatalf("pending hold = %+v/%v; want the stop held for confirmation", pending, armed)
	}
	if icon := s.icon(); icon != "bolt" {
		s.t.Fatalf("icon while the stop is held = %q; want bolt", icon)
	}
}

func (s *probedSession) confirmed() bool {
	pending, ok := terminalHoldPending(s.t, s.h, s.slot)
	return ok && pending.confirmed
}

// An idle screen confirms the held stop after two agreeing reads, and the
// native stop is what paints: one notification, no stop of the probe's own.
func TestHeldStopIsConfirmedByTheIdleScreenOnEitherBackend(t *testing.T) {
	eachProbedBackend(t, func(t *testing.T, s *probedSession) {
		s.show(claudeIdleScreen)
		s.startTurn()
		s.stopHeld()
		base := time.Now()

		s.tick(base)
		if s.confirmed() {
			t.Fatal("one read of the screen confirmed the stop; Claude needs two that agree")
		}
		s.tick(base.Add(time.Second))
		pending, _ := terminalHoldPending(t, s.h, s.slot)
		if !pending.confirmed || pending.confirmedBy != protocol.StopConfirmedByClaudeScreen {
			t.Fatalf("hold after two reads = %+v; want confirmed by %s", pending, protocol.StopConfirmedByClaudeScreen)
		}
		if icon := s.icon(); icon != "bolt" {
			t.Fatalf("icon inside the quiet period = %q; want bolt", icon)
		}

		time.Sleep(terminalHoldTestDelay)
		synctest.Wait()
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon after the confirmed hold = %q; want stop", icon)
		}
		if got := s.rec.len(); got != 1 {
			t.Fatalf("notifications = %d; want 1 (%+v)", got, s.rec.snapshot())
		}
		ev := lastStopEvent(t, s.h, s.slot)
		if ev.StopHoldOutcome != protocol.StopHoldOutcomeConfirmed || ev.StopConfirmedBy != protocol.StopConfirmedByClaudeScreen {
			t.Fatalf("released stop = %q by %q; want confirmed by the Claude screen", ev.StopHoldOutcome, ev.StopConfirmedBy)
		}
		assertNoProbeStopFact(t, s.h, s.slot)
	})
}

// A pane that cannot be captured confirms nothing: the stop stays held past
// its quiet period and is released at the deadline, as a stop nothing
// confirmed.
func TestHeldStopOfAnUnreadablePaneIsReleasedAtTheDeadlineOnEitherBackend(t *testing.T) {
	eachProbedBackend(t, func(t *testing.T, s *probedSession) {
		s.unreadable()
		s.startTurn()
		s.stopHeld()
		base := time.Now()
		for i := range 3 {
			s.tick(base.Add(time.Duration(i) * time.Second))
		}
		if s.confirmed() {
			t.Fatal("a pane that could not be read confirmed the stop")
		}

		time.Sleep(terminalHoldTestDelay + time.Second)
		synctest.Wait()
		if icon := s.icon(); icon != "bolt" {
			t.Fatalf("icon after the quiet period alone = %q; want bolt", icon)
		}
		if got := s.rec.len(); got != 0 {
			t.Fatalf("notifications before the deadline = %d; want 0", got)
		}

		time.Sleep(terminalHoldTestTimeout)
		synctest.Wait()
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon after the deadline = %q; want stop", icon)
		}
		if got := s.rec.len(); got != 1 {
			t.Fatalf("notifications = %d; want 1 (%+v)", got, s.rec.snapshot())
		}
		ev := lastStopEvent(t, s.h, s.slot)
		if ev.StopHoldOutcome != protocol.StopHoldOutcomeDeadline || ev.StopConfirmedBy != protocol.StopConfirmedByDeadline {
			t.Fatalf("released stop = %q by %q; want the deadline", ev.StopHoldOutcome, ev.StopConfirmedBy)
		}
	})
}

// A session that ends while its stop is held is gone before the hold could
// release it: the stop neither paints nor notifies, and the closed slot does
// not come back. On tmux the binding that named the session goes with the
// slot, so a release would notify under no session's name.
func TestHeldStopOfASessionThatEndsIsDroppedOnEitherBackend(t *testing.T) {
	eachProbedBackend(t, func(t *testing.T, s *probedSession) {
		s.unreadable()
		s.startTurn()
		s.stopHeld()

		s.end()
		if _, held := terminalHoldPending(t, s.h, s.slot); held {
			t.Fatal("the stop of an ended session stayed held")
		}

		time.Sleep(terminalHoldTestTimeout + time.Second)
		synctest.Wait()
		if got := s.rec.len(); got != 0 {
			t.Fatalf("notifications = %d; want 0 (%+v)", got, s.rec.snapshot())
		}
		s.h.stateMu.Lock()
		_, revived := s.h.store[s.slot]
		s.h.stateMu.Unlock()
		if revived {
			t.Fatal("the held stop brought the closed slot's state back")
		}
	})
}

// The context-limit banner on a working agent's screen turns it to the error
// state once, with one notification; a pane that cannot be read reports
// nothing.
func TestContextLimitBannerIsDetectedOnEitherBackend(t *testing.T) {
	banner := loadClaudeContextLimitFixture(t)
	eachProbedBackend(t, func(t *testing.T, s *probedSession) {
		s.startTurn()
		now := time.Now()

		s.unreadable()
		s.tick(now)
		if icon := s.icon(); icon != "bolt" || s.rec.len() != 0 {
			t.Fatalf("unreadable pane: icon %q, %d notifications; want bolt and none", icon, s.rec.len())
		}

		s.show(banner)
		s.tick(now.Add(time.Second))
		if icon := s.icon(); icon != "error" {
			t.Fatalf("icon with the banner on screen = %q; want error", icon)
		}
		s.h.stateMu.Lock()
		notified, lastAction := s.h.store[s.slot].ContextLimitNotified, s.h.store[s.slot].LastAction
		s.h.stateMu.Unlock()
		if !notified || lastAction != "stop_failure" {
			t.Fatalf("notified=%v last action %q; want the banner recorded as stop_failure", notified, lastAction)
		}
		detail := s.h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: s.slot})
		ev, ok := lastEventNamed(detail.Events, claudeContextLimitProbeEvent)
		if !ok || ev.Action != "stop_failure" || ev.ResultIconState != "error" || ev.CurrentIconState != "bolt" || !ev.ContextLimit {
			t.Fatalf("banner event = %+v (%v); want stop_failure bolt->error marked context_limit", ev, ok)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		notifications := s.rec.len()
		if notifications != 1 {
			t.Fatalf("notifications = %d; want 1 (%+v)", notifications, s.rec.snapshot())
		}

		// The banner stays on screen: nothing more is said about it.
		s.tick(now.Add(2 * time.Second))
		s.tick(now.Add(3 * time.Second))
		if s.icon() != "error" || s.rec.len() != notifications {
			t.Fatalf("after two more ticks: icon %q, %d notifications; want error and still %d", s.icon(), s.rec.len(), notifications)
		}
	})
}

// A tmux screen is only evidence about the registered pane of the registered
// server generation while its agent runs. A capture from a restarted server,
// or of a pane whose agent exited, is treated as no capture.
func TestTmuxScreensFromAnotherGenerationOrAnExitedPaneAreNoEvidence(t *testing.T) {
	banner := loadClaudeContextLimitFixture(t)
	for name, spoil := range map[string]func(*fakePaneScreens){
		"another server generation": func(s *fakePaneScreens) { s.generation = "tmux-t-2" },
		"an exited pane":            func(s *fakePaneScreens) { s.exited = true },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, screens := newProbedTmuxSession(t)
				screens.set(spoil)
				s.show(banner)
				s.startTurn()
				s.tick(time.Now())
				if icon := s.icon(); icon != "bolt" || s.rec.len() != 0 {
					t.Fatalf("icon %q, %d notifications; want the banner ignored", icon, s.rec.len())
				}

				s.show(claudeIdleScreen)
				s.stopHeld()
				s.tick(time.Now().Add(time.Second))
				s.tick(time.Now().Add(2 * time.Second))
				if s.confirmed() {
					t.Fatal("a screen that is not the agent's confirmed its stop")
				}
				screens.mu.Lock()
				reads := screens.reads
				screens.mu.Unlock()
				if reads == 0 {
					t.Fatal("fixture: the pane was never read")
				}
				s.h.cancelStopHold(s.slot)
			})
		})
	}
}

// A tmux stop is held for confirmation only when something can confirm it:
// a reader for its pane and a probe for its harness. Otherwise it keeps the
// short reset hold, as a Kitty stop does in a daemon with no Kitty.
func TestTmuxStopIsHeldOnlyWhenItsPaneCanConfirmIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, harness string
		reader, want  bool
	}{
		{name: "claude with a reader", harness: harness.Claude, reader: true, want: true},
		{name: "codex with a reader", harness: harness.Codex, reader: true, want: true},
		{name: "copilot with a reader", harness: harness.Copilot, reader: true, want: true},
		{name: "copilot with no reader", harness: harness.Copilot},
		{name: "claude with no reader", harness: harness.Claude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newProbedHandler(t)
			// A Kitty that is running changes nothing for a tmux session.
			h.SetKittyClient(&fakeKittyClient{})
			if tc.reader {
				h.SetRuntimeScreenReader(func(runtimesession.BackendInstance) (RuntimeScreen, error) {
					return &fakePaneScreens{generation: probedGeneration}, nil
				})
			}
			h.stateMu.Lock()
			armable := h.terminalStopHoldArmableLocked(protocol.Request{WindowID: runtimeSlotBase + 1, Action: "stop", Event: "Stop", Harness: tc.harness})
			h.stateMu.Unlock()
			if armable != tc.want {
				t.Fatalf("armable = %v; want %v", armable, tc.want)
			}
		})
	}
}

// A tmux session at rest on a finished turn costs no screen read, and one
// showing no icon is read only every few ticks: each read is a tmux client.
func TestRuntimeProbesSpareSessionsThatAreNotWorking(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, screens := newProbedTmuxSession(t)
		s.show(claudeIdleScreen)
		reads := func() int {
			screens.mu.Lock()
			defer screens.mu.Unlock()
			return screens.reads
		}
		now := time.Now()
		ticks := 2 * runtimeQuietProbeEvery
		for i := range ticks {
			s.tick(now.Add(time.Duration(i) * time.Second))
		}
		if got := reads(); got != 2 {
			t.Fatalf("%d reads of a session with no icon over %d ticks; want one every %d", got, ticks, runtimeQuietProbeEvery)
		}

		// Working: read on every tick.
		s.startTurn()
		s.show("✻ Thinking… (12s · esc to interrupt)")
		before := reads()
		for i := range 3 {
			s.tick(now.Add(time.Duration(ticks+i) * time.Second))
		}
		if got := reads() - before; got != 3 {
			t.Fatalf("%d reads of a working session over 3 ticks; want 3", got)
		}

		// At rest on a finished turn: not read at all.
		s.show(claudeIdleScreen)
		s.stopHeld()
		s.tick(now.Add(time.Minute))
		s.tick(now.Add(time.Minute + time.Second))
		time.Sleep(terminalHoldTestDelay)
		synctest.Wait()
		if icon := s.icon(); icon != "stop" {
			t.Fatalf("icon = %q; want the session at rest", icon)
		}
		before = reads()
		for i := range ticks {
			s.tick(now.Add(2*time.Minute + time.Duration(i)*time.Second))
		}
		if got := reads() - before; got != 0 {
			t.Fatalf("%d reads of a session at rest; want none", got)
		}
	})
}

// The production reader against a real private tmux server: the daemon reads
// the registered pane of a tmux agent with no terminal attached and no Kitty,
// finds the context-limit banner on it, and reads nothing once the server is
// gone.
func TestRuntimeScreenProbeReadsARealTmuxPane(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	agent := w.launch("api")
	h, _ := w.startDaemon(time.Now())
	h.SetRuntimeScreenReader(NewTmuxRuntimeScreenReader())
	requireAccepted(t, h.Handle(runtimeHook(t, agent.id, "UserPromptSubmit", hookevent.HookPayload{SessionID: "conv"})))
	slot := slotOf(t, h, agent.id)

	h.runScreenProbeTick(time.Now())
	if icon := slotIcon(h, slot); icon != "bolt" {
		t.Fatalf("icon of a working agent with nothing on screen = %q; want bolt", icon)
	}
	health := h.runtimeHealthSnapshot(time.Now())
	if health.Capture.Reads == 0 || health.Capture.Failures != 0 {
		t.Fatalf("capture health = %+v; want the pane read without failure", health.Capture)
	}

	// The stand-in agent echoes what is typed at it, which is how the banner
	// gets on its screen.
	typed := exec.Command(w.tmux.Server.Binary(), "-S", w.tmux.Server.Socket(), "send-keys", "-t", agent.launched.Pane.ID, "-l",
		"Context limit reached · /compact or /clear to continue")
	if out, err := typed.CombinedOutput(); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	for deadline := time.Now().Add(10 * time.Second); slotIcon(h, slot) != "error"; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the banner on the pane was never detected: icon %q", slotIcon(h, slot))
		}
		h.runScreenProbeTick(time.Now())
	}
	if clients := w.survey().Clients; len(clients) != 0 {
		t.Fatalf("reading the pane attached %d terminals", len(clients))
	}
	w.requireSameAgent(agent, "its screen was read")

	// A second agent starts working and the server dies under it: its pane
	// can no longer be captured, and the probe reports a failed read and
	// changes nothing.
	other := w.launch("web")
	requireAccepted(t, h.Handle(runtimeHook(t, other.id, "UserPromptSubmit", hookevent.HookPayload{SessionID: "conv-2"})))
	otherSlot := slotOf(t, h, other.id)
	if err := w.tmux.Server.Kill(); err != nil {
		t.Fatal(err)
	}
	h.runScreenProbeTick(time.Now())
	if icon := slotIcon(h, otherSlot); icon != "bolt" {
		t.Fatalf("icon after a read of a pane that is gone = %q; want bolt kept", icon)
	}
	if after := h.runtimeHealthSnapshot(time.Now()); after.Capture.Failures == 0 {
		t.Fatalf("capture health = %+v; want the failed read counted", after.Capture)
	}
}
