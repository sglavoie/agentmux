package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// restAt puts a slot at rest, unseen, since at: the state a finished turn
// nobody has looked at leaves behind.
func restAt(t *testing.T, h *Handler, slot int, at time.Time) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[slot]
	if !ok {
		t.Fatalf("slot %d has no state", slot)
	}
	st.Unseen, st.PresentationChangedAt = true, at
	h.markWindowDirtyLocked(slot)
}

// A tmux session has no focus for the daemon to read. What stands in for it
// is a terminal attaching to the session, or being used in it, after the
// session came to rest: that clears its unseen attention, and only its own.
// A terminal that was already attached and has not been touched since clears
// nothing, and neither does the absence of one.
//
// This runs a real private tmux server and real terminals on pseudo-terminals.
func TestAttachingATerminalClearsATmuxSessionsUnseenAttention(t *testing.T) {
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 20 * time.Millisecond
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })

	w := newRuntimeFaultWorld(t)
	api, web := w.launch("api"), w.launch("web")
	start := time.Now()
	h, _ := w.startDaemon(start)
	// finishTurn runs one turn of the agent through its hooks and waits for
	// the session to come to rest, which is what raises its unseen attention.
	finishTurn := func(agent faultAgent) int {
		t.Helper()
		payload := hookevent.HookPayload{SessionID: "conv-" + string(agent.id)}
		requireAccepted(t, h.Handle(runtimeHook(t, agent.id, "UserPromptSubmit", payload)))
		requireAccepted(t, h.Handle(runtimeHook(t, agent.id, "Stop", payload)))
		slot := slotOf(t, h, agent.id)
		for deadline := time.Now().Add(10 * time.Second); slotIcon(h, slot) != "stop"; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the session never came to rest: icon %q", slotIcon(h, slot))
			}
		}
		if !unseen(t, h, slot) {
			t.Fatal("a turn that finished with nobody looking is not unseen")
		}
		return slot
	}
	pass := 0
	maintain := func() {
		pass++
		h.maintainRuntimeSessions(start.Add(time.Duration(pass) * time.Minute))
	}
	// tmux keeps attach and activity times to the second, so each step that
	// must be told apart from the one before it starts in a later second.
	nextSecond := func() {
		time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 50*time.Millisecond)))
	}

	// Nobody is looking: both stay unseen however often the daemon checks.
	apiSlot, webSlot := finishTurn(api), finishTurn(web)
	maintain()
	maintain()
	if !unseen(t, h, apiSlot) || !unseen(t, h, webSlot) {
		t.Fatal("a session with no terminal was marked seen")
	}

	// A terminal attaches to api after it came to rest: api is seen.
	nextSecond()
	viewer, _ := w.attach(api, 100, 30)
	maintain()
	if unseen(t, h, apiSlot) {
		t.Fatal("attaching a terminal did not clear the session's unseen attention")
	}
	if !unseen(t, h, webSlot) {
		t.Fatal("attaching to one session cleared another's attention")
	}
	if icon := slotIcon(h, apiSlot); icon != "stop" {
		t.Fatalf("looking at the session changed its icon to %q", icon)
	}

	// The terminal stays attached and untouched while api finishes another
	// turn: being attached is not looking.
	nextSecond()
	finishTurn(api)
	maintain()
	maintain()
	if !unseen(t, h, apiSlot) {
		t.Fatal("a terminal that was merely still attached marked a later turn seen")
	}

	// The operator types in it: that is looking.
	nextSecond()
	h.stateMu.Lock()
	rested := h.store[apiSlot].PresentationChangedAt.Unix()
	h.stateMu.Unlock()
	if err := viewer.Type("x"); err != nil {
		t.Fatal(err)
	}
	w.eventually("the keypress to reach the server", func(s tmux.Survey) bool {
		clients := s.ClientsOf(api.launched.Pane)
		return len(clients) == 1 && clients[0].Activity > rested
	})
	maintain()
	if unseen(t, h, apiSlot) {
		t.Fatal("using the attached terminal did not clear the session's unseen attention")
	}
	if !unseen(t, h, webSlot) {
		t.Fatal("web was cleared without anyone attaching to it")
	}
	w.requireSameAgent(api, "its attention was cleared")

	// The clear is durable: a daemon that starts again restores api as seen
	// and web as still unseen.
	// (Well inside the hour a finished turn's fact is kept for: past it the
	// restored session has no resting turn left to be unseen.)
	restarted, report := w.startDaemon(start.Add(30 * time.Minute))
	if report.Outcomes[string(api.id)] != runtimeRestoreRestored || report.Outcomes[string(web.id)] != runtimeRestoreRestored {
		t.Fatalf("restore report = %+v; want both sessions restored", report)
	}
	if apiUnseen, webUnseen := unseen(t, restarted, slotOf(t, restarted, api.id)), unseen(t, restarted, slotOf(t, restarted, web.id)); apiUnseen || !webUnseen {
		t.Fatalf("the restarted daemon has api unseen=%v web unseen=%v; want api seen and web unseen", apiUnseen, webUnseen)
	}
}

// The observation only ever clears the bit, and only on a session with no
// Kitty window: a Kitty session is seen through Kitty's focus, never through
// this path.
func TestViewerObservationLeavesKittySlotsAndSeenSessionsAlone(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	onKitty, onTmux := newRuntimeID(t), newRuntimeID(t)
	directory.put(onKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	for _, runtime := range []runtimesession.ID{onKitty, onTmux} {
		requireAccepted(t, h.Handle(runtimeHook(t, runtime, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-" + string(runtime)})))
	}
	kittySlot, tmuxSlot := slotOf(t, h, onKitty), slotOf(t, h, onTmux)
	rested := time.Now()
	restAt(t, h, kittySlot, rested)
	restAt(t, h, tmuxSlot, rested)

	// Older than the rest: nothing. An unknown session: nothing.
	h.observeRuntimeViewers([]tmux.Viewed{
		{ID: onTmux, At: rested.Add(-5 * time.Second)},
		{ID: newRuntimeID(t), At: rested.Add(time.Minute)},
		{ID: onKitty, At: rested.Add(time.Minute)},
	}, rested.Add(time.Minute))
	if !unseen(t, h, tmuxSlot) || !unseen(t, h, kittySlot) {
		t.Fatalf("unseen after observations that prove nothing: tmux=%v kitty=%v; want both kept", unseen(t, h, tmuxSlot), unseen(t, h, kittySlot))
	}

	observedAt := rested.Add(2 * time.Minute)
	h.observeRuntimeViewers([]tmux.Viewed{{ID: onTmux, At: rested.Add(time.Second)}}, observedAt)
	if unseen(t, h, tmuxSlot) {
		t.Fatal("a later viewing did not clear the tmux session")
	}
	h.stateMu.Lock()
	changedAt := h.store[tmuxSlot].PresentationChangedAt
	h.stateMu.Unlock()
	if !changedAt.Equal(observedAt) {
		t.Fatalf("presentation changed at %v; want the observation time %v", changedAt, observedAt)
	}
	// Seen stays seen: a second observation changes nothing.
	h.observeRuntimeViewers([]tmux.Viewed{{ID: onTmux, At: rested.Add(time.Hour)}}, observedAt.Add(time.Hour))
	h.stateMu.Lock()
	again := h.store[tmuxSlot].PresentationChangedAt
	h.stateMu.Unlock()
	if !again.Equal(observedAt) {
		t.Fatal("an already seen session was rewritten by a later observation")
	}
}
