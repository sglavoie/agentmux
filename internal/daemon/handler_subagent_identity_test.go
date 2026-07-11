package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/hookevent"
)

// These tests drive the full hook path (normalization stamp → legacy machine →
// reducer cutover) with identity-carrying payloads, pinning the ID-based
// subagent aggregation invariant end to end: the visible state comes from
// per-agent facts grouped by parent session, not from the legacy scalar
// counter. Assertions read the authoritative visible state (windowEffective),
// never the legacy Response fields.

func subagentHookEvent(t *testing.T, h *Handler, windowID int, event string, payload hookevent.HookPayload) Response {
	t.Helper()
	return h.Handle(Request{
		Type:        TypeHookEvent,
		WindowID:    windowID,
		Event:       event,
		Harness:     "claude",
		PayloadJSON: hookPayloadJSON(t, payload),
	})
}

// TestHandler_SubagentIdentifiedConcurrentChildren: two identified agents run
// concurrently; an intermediate stop ends only that agent's fact (the legacy
// "still running" suppression no longer hides it from the reducer), delayed
// parent activity cannot overwrite 🤖, and the last stop yields to the parent
// session's own activity.
func TestHandler_SubagentIdentifiedConcurrentChildren(t *testing.T) {
	h := newTestHandler()

	subagentHookEvent(t, h, 1, "UserPromptSubmit", hookevent.HookPayload{SessionID: "s1"})
	subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"})
	subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s1", AgentID: "a2"})
	if eff, icon := windowEffective(t, h, 1); eff.Icon != "subagent" || icon != "subagent" {
		t.Fatalf("two agents running: %+v/%q, want subagent", eff, icon)
	}

	subagentHookEvent(t, h, 1, "SubagentStop", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"})
	if eff, _ := windowEffective(t, h, 1); eff.Icon != "subagent" {
		t.Fatalf("after intermediate stop: %+v, want subagent from a2", eff)
	}

	subagentHookEvent(t, h, 1, "PostToolUse", hookevent.HookPayload{SessionID: "s1"})
	if eff, _ := windowEffective(t, h, 1); eff.Icon != "subagent" {
		t.Fatalf("delayed parent activity during agents: %+v, want subagent", eff)
	}

	subagentHookEvent(t, h, 1, "SubagentStop", hookevent.HookPayload{SessionID: "s1", AgentID: "a2"})
	if eff, icon := windowEffective(t, h, 1); eff.Icon != "bolt" || icon != "bolt" {
		t.Fatalf("after last stop: %+v/%q, want bolt from parent activity", eff, icon)
	}
}

// TestHandler_SubagentDuplicateStartsAndStopsIdempotent: a duplicated start
// collapses onto one per-agent fact, so a single stop ends the robot — the
// legacy counter's inflation cannot leak into the visible state — and a
// duplicate stop replay changes nothing.
func TestHandler_SubagentDuplicateStartsAndStopsIdempotent(t *testing.T) {
	h := newTestHandler()

	subagentHookEvent(t, h, 1, "UserPromptSubmit", hookevent.HookPayload{SessionID: "s1"})
	subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"})
	subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"}) // duplicate delivery

	subagentHookEvent(t, h, 1, "SubagentStop", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"})
	if eff, _ := windowEffective(t, h, 1); eff.Icon != "bolt" {
		t.Fatalf("after single stop of duplicated start: %+v, want bolt", eff)
	}

	subagentHookEvent(t, h, 1, "SubagentStop", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"})
	if eff, _ := windowEffective(t, h, 1); eff.Icon != "bolt" {
		t.Fatalf("after duplicate stop replay: %+v, want bolt", eff)
	}
}

// TestHandler_SubagentStopBeforeStartDoesNotStrandRobot: a stop delivered
// before its own start (reordered hook processes) leaves an icon-less
// tombstone that absorbs the late start, so 🤖 never strands on an agent that
// already finished.
func TestHandler_SubagentStopBeforeStartDoesNotStrandRobot(t *testing.T) {
	h := newTestHandler()

	subagentHookEvent(t, h, 1, "UserPromptSubmit", hookevent.HookPayload{SessionID: "s1"})
	subagentHookEvent(t, h, 1, "SubagentStop", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"})
	subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"})

	if eff, icon := windowEffective(t, h, 1); eff.Icon != "bolt" || icon != "bolt" {
		t.Fatalf("stop-before-start: %+v/%q, want bolt (agent already finished)", eff, icon)
	}
}

// TestHandler_SubagentIdentityLessLegacyMultiset: a harness that omits agent
// ids aggregates on the balanced per-session bucket — intermediate stops keep
// 🤖 while any agent remains, and the last stop yields to parent activity.
func TestHandler_SubagentIdentityLessLegacyMultiset(t *testing.T) {
	h := newTestHandler()

	subagentHookEvent(t, h, 1, "UserPromptSubmit", hookevent.HookPayload{SessionID: "s1"})
	subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s1"})
	subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s1"})

	subagentHookEvent(t, h, 1, "SubagentStop", hookevent.HookPayload{SessionID: "s1"})
	if eff, _ := windowEffective(t, h, 1); eff.Icon != "subagent" {
		t.Fatalf("one identity-less agent still running: %+v, want subagent", eff)
	}

	subagentHookEvent(t, h, 1, "SubagentStop", hookevent.HookPayload{SessionID: "s1"})
	if eff, _ := windowEffective(t, h, 1); eff.Icon != "bolt" {
		t.Fatalf("all identity-less agents done: %+v, want bolt", eff)
	}
}

// TestHandler_SubagentMixedSessionEndClearsOnlyItsAgents: agents from two
// sessions share one window; each SessionEnd clears only its own session's
// agents. SessionEnd stops ride the reset-defer debounce, so the test runs
// under synctest and sleeps past the delay.
func TestHandler_SubagentMixedSessionEndClearsOnlyItsAgents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTestHandler()

		subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s1", AgentID: "a1"})
		subagentHookEvent(t, h, 1, "SubagentStart", hookevent.HookPayload{SessionID: "s2", AgentID: "b1"})

		subagentHookEvent(t, h, 1, "SessionEnd", hookevent.HookPayload{SessionID: "s1", Reason: "user_exit"})
		time.Sleep(time.Second)
		synctest.Wait()
		if eff, icon := windowEffective(t, h, 1); eff.Icon != "subagent" || icon != "subagent" {
			t.Fatalf("after s1 end: %+v/%q, want subagent from s2's agent", eff, icon)
		}

		subagentHookEvent(t, h, 1, "SessionEnd", hookevent.HookPayload{SessionID: "s2", Reason: "user_exit"})
		time.Sleep(time.Second)
		synctest.Wait()
		if eff, icon := windowEffective(t, h, 1); eff.Icon != "stop" || icon != "stop" {
			t.Fatalf("after s2 end: %+v/%q, want stop (no agents remain)", eff, icon)
		}
	})
}
