package daemon

import (
	"fmt"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

const (
	supervisorRunID      = "run-supervised"
	supervisorQuestionID = "00000000-0000-0000-0000-000000000011"
)

func supervisorQuestionFixture(t *testing.T, notifier *notification.Notifier) (*Handler, aslSupervisorTarget, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	h := NewHandler(NewStateGuard(time.Hour), notifier)
	target := aslSupervisorTarget{
		windowID:       42,
		originWindowID: 84,
		candidate: agentloop.Candidate{
			RunID:    supervisorRunID,
			StateDir: t.TempDir(),
		},
	}

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(42)
	st.LastSessionTitle = "W:api"
	st.LastAccount = "work"
	st.LastHarness = "codex"
	st.Reducer = state.NewReducer(state.ReducerConfig{})
	st.Reducer.Apply(protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceASL, Key: supervisorRunID},
		RunID:  supervisorRunID,
		Phase:  state.ASLPhaseTurn,
		Seq:    1,
		At:     now.Add(-10 * time.Second).Unix(),
	})
	st.Reducer.Apply(protocol.ReducerEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:parent"},
		Action:    "active",
		Seq:       1,
		TurnID:    "turn-parent",
		At:        now.Add(-5 * time.Second).Unix(),
		SessionID: "parent",
	})
	st.ShadowSeq = 2
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(now.Unix()))
	h.registerASLSupervisorTargetLocked(target)
	h.stateMu.Unlock()
	return h, target, now
}

func supervisorProjection(now time.Time, seq uint64) agentloop.SupervisorQuestionProjection {
	return agentloop.SupervisorQuestionProjection{
		RunID:                   supervisorRunID,
		ProjectionSeq:           seq,
		UnansweredQuestions:     1,
		LatestQuestionID:        supervisorQuestionID,
		LatestQuestionCreatedAt: now.Add(-2 * time.Second),
		UpdatedAt:               now,
	}
}

func applySupervisorStop(h *Handler, now time.Time) {
	n := protocol.NormalizedEvent{
		Source:        protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:parent"},
		Action:        "stop",
		Seq:           2,
		TurnID:        "turn-parent",
		At:            now.Unix(),
		SessionID:     "parent",
		EndsSession:   true,
		Event:         "Stop",
		SourceEventID: "parent-stop",
	}
	req := protocol.Request{
		WindowID:     42,
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "codex",
		Normalized:   &n,
	}
	h.reduceStateUpdate(req, applyOutcome{}, protocol.Response{OK: true}, now)
}

func supervisorVisibleState(t *testing.T, h *Handler) state.EffectiveState {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[42]
	if st == nil || st.Reducer == nil {
		t.Fatal("missing supervisor fixture state")
	}
	return st.Reducer.Reduce(time.Now().Unix())
}

func supervisorMailboxReplyPayload(
	target aslSupervisorTarget,
	projectionSeq uint64,
	replySeq uint64,
) hookevent.HookPayload {
	return hookevent.HookPayload{
		Source:                    "agent_session_loop",
		Reason:                    aslMailboxReplyReason,
		RunID:                     target.candidate.RunID,
		StatePath:                 filepath.Join(target.candidate.StateDir, "state.json"),
		QuestionID:                supervisorQuestionID,
		SourceSequence:            fmt.Sprintf("%d", replySeq),
		MailboxProjectionSequence: fmt.Sprintf("%d", projectionSeq),
		SourceEventID: fmt.Sprintf(
			"%s:mailbox-reply:%d",
			target.candidate.RunID,
			replySeq,
		),
		EmittedTime: "1785256911",
	}
}

func deliverSupervisorMailboxReply(
	t *testing.T,
	h *Handler,
	payload hookevent.HookPayload,
) {
	t.Helper()
	resp := h.Handle(protocol.Request{
		Type:        protocol.TypeHookEvent,
		WindowID:    42,
		Event:       agentSessionLoopMailboxReply,
		PayloadJSON: hookPayloadJSON(t, payload),
	})
	if !resp.OK {
		t.Fatalf("mailbox reply response = %+v", resp)
	}
}

func supervisorMailboxQuestionPayload(
	projectDir string,
	statePath string,
	questionID string,
	questionSeq uint64,
	projectionSeq uint64,
	createdAt time.Time,
) hookevent.HookPayload {
	return hookevent.HookPayload{
		Source:                    "agent_session_loop",
		Reason:                    aslMailboxQuestionReason,
		CWD:                       projectDir,
		Harness:                   "claude",
		KittyWindowID:             42,
		RunID:                     supervisorRunID,
		StatePath:                 statePath,
		QuestionID:                questionID,
		QuestionCreatedAt:         createdAt.Format(time.RFC3339),
		UnansweredQuestions:       1,
		DeliveryState:             "open",
		SourceSequence:            fmt.Sprintf("%d", questionSeq),
		MailboxProjectionSequence: fmt.Sprintf("%d", projectionSeq),
		SourceEventID: fmt.Sprintf(
			"%s:mailbox-question:%d",
			supervisorRunID,
			questionSeq,
		),
		EmittedTime: fmt.Sprintf("%d", createdAt.Unix()),
	}
}

func deliverSupervisorMailboxQuestion(
	t *testing.T,
	h *Handler,
	payload hookevent.HookPayload,
) {
	t.Helper()
	resp := h.Handle(protocol.Request{
		Type:        protocol.TypeHookEvent,
		WindowID:    42,
		Event:       agentSessionLoopMailboxQuestion,
		PayloadJSON: hookPayloadJSON(t, payload),
	})
	if !resp.OK {
		t.Fatalf("mailbox question response = %+v", resp)
	}
}

func TestASLSupervisorMailboxQuestionRaisesImmediatelyAndRearmsOnlyForNewQuestion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		notifier, calls := newTestNotifier(&config.NotificationConfig{Permission: true})
		h, windows, projectDir := reconcileTestFixture(t)
		h.notifier = notifier
		h.SetKittyClient(&fakeKittyClient{windows: windows})
		statePath := filepath.Join(t.TempDir(), "state.json")
		aslLifecycleHookRequest(t, h, agentSessionLoopStarted, projectDir, hookevent.HookPayload{
			RunID:     supervisorRunID,
			StatePath: statePath,
		})

		now := time.Now().UTC().Truncate(time.Second)
		payload := supervisorMailboxQuestionPayload(
			projectDir,
			statePath,
			supervisorQuestionID,
			7,
			7,
			now,
		)
		deliverSupervisorMailboxQuestion(t, h, payload)
		synctest.Wait()
		if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
			t.Fatalf("direct mailbox handoff = %+v; want question + loop", got)
		}
		if calls.len() != 1 || calls.snapshot()[0].subtitle != "User input requested" {
			t.Fatalf("notifications = %+v; want one user-input request", calls.snapshot())
		}

		deliverSupervisorMailboxQuestion(t, h, payload)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatalf("duplicate handoff dispatched %d notifications; want 1", calls.len())
		}

		if resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "user_clear"}); !resp.OK {
			t.Fatalf("user clear response = %+v", resp)
		}
		payload.MailboxProjectionSequence = "8"
		deliverSupervisorMailboxQuestion(t, h, payload)
		if got := supervisorVisibleState(t, h); got.Icon != "" || !got.Looping {
			t.Fatalf("dismissed question set = %+v; want bare loop", got)
		}

		payload.QuestionID = "00000000-0000-0000-0000-000000000022"
		payload.QuestionCreatedAt = now.Add(time.Second).Format(time.RFC3339)
		payload.SourceSequence = "9"
		payload.MailboxProjectionSequence = "9"
		payload.SourceEventID = supervisorRunID + ":mailbox-question:9"
		deliverSupervisorMailboxQuestion(t, h, payload)
		synctest.Wait()
		if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
			t.Fatalf("newer question = %+v; want question + loop", got)
		}
		if calls.len() != 2 {
			t.Fatalf("newer question notifications = %d; want 2", calls.len())
		}
	})
}

func TestASLSupervisorQuestionProjectionBeforeStopRaisesQuestionAndNotifiesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		notifier, calls := newTestNotifier(&config.NotificationConfig{Permission: true})
		h, target, now := supervisorQuestionFixture(t, notifier)
		projection := supervisorProjection(now, 7)

		h.acceptASLSupervisorProjection(now, target, projection)
		if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
			t.Fatalf("pending projection while supervisor active = %+v; want bolt + loop", got)
		}

		applySupervisorStop(h, now)
		synctest.Wait()
		if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
			t.Fatalf("projection + supervisor stop = %+v; want question + loop", got)
		}
		if calls.len() != 1 || calls.snapshot()[0].subtitle != "User input requested" {
			t.Fatalf("notifications = %+v; want one user-input request", calls.snapshot())
		}

		h.acceptASLSupervisorProjection(now.Add(time.Second), target, projection)
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatalf("unchanged projection dispatched %d notifications; want 1", calls.len())
		}
	})
}

func TestASLSupervisorQuestionStopBeforeProjectionRaisesQuestion(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("masked supervisor stop = %+v; want bolt + loop", got)
	}

	h.acceptASLSupervisorProjection(now.Add(time.Second), target, supervisorProjection(now, 7))
	if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
		t.Fatalf("late projection = %+v; want question + loop", got)
	}
}

func TestASLSupervisorQuestionRejectsActiveAndPredatingStops(t *testing.T) {
	t.Run("supervisor still active", func(t *testing.T) {
		h, target, now := supervisorQuestionFixture(t, nil)
		h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
		if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
			t.Fatalf("active supervisor = %+v; want bolt + loop", got)
		}
	})

	t.Run("stop predates question", func(t *testing.T) {
		h, target, now := supervisorQuestionFixture(t, nil)
		applySupervisorStop(h, now)
		projection := supervisorProjection(now, 7)
		projection.LatestQuestionCreatedAt = now.Add(time.Second)
		h.acceptASLSupervisorProjection(now.Add(2*time.Second), target, projection)
		if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
			t.Fatalf("old stop + newer question = %+v; want bolt + loop", got)
		}
	})
}

func TestASLSupervisorQuestionRejectsUntrustedWorkerStops(t *testing.T) {
	cases := []struct {
		name  string
		event protocol.ReducerEvent
	}{
		{
			name: "anonymous legacy SessionEnd",
			event: protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceLegacy, Key: "win:42"},
				Action: "stop",
				Seq:    1,
			},
		},
		{
			name: "unidentified harness stop",
			event: protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "win:42"},
				Action: "stop",
				Seq:    1,
			},
		},
		{
			name: "identified pipe worker stop",
			event: protocol.ReducerEvent{
				Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:worker"},
				Action:    "stop",
				SessionID: "worker",
				PipeMode:  true,
				Seq:       1,
			},
		},
		{
			name: "untrusted synthetic stop",
			event: protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "other"},
				Action: "stop",
				Seq:    1,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, target, now := supervisorQuestionFixture(t, nil)
			tc.event.At = now.Unix()
			h.stateMu.Lock()
			st := h.store[42]
			st.Reducer.Apply(tc.event)
			h.publishEffectiveLocked(42, st, st.Reducer.Reduce(now.Unix()))
			h.stateMu.Unlock()

			h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
			if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
				t.Fatalf("untrusted stop = %+v; want active supervisor bolt + loop", got)
			}
		})
	}
}

func TestASLSupervisorQuestionAcceptsAuthoritativeStopProbe(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.Apply(protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: claudeScreenStopProbeSource},
		Action: "stop",
		Seq:    1,
		At:     now.Unix(),
	})
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(now.Unix()))
	h.stateMu.Unlock()

	h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
	if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
		t.Fatalf("authoritative stop probe + projection = %+v; want question + loop", got)
	}
}

func TestASLSupervisorQuestionSurvivesHeartbeatAndClearsOnReply(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))

	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.Apply(protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceASL, Key: supervisorRunID},
		RunID:  supervisorRunID,
		Phase:  state.ASLPhaseTurn,
		Seq:    2,
		At:     now.Add(time.Second).Unix(),
	})
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(now.Add(time.Second).Unix()))
	h.stateMu.Unlock()
	if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
		t.Fatalf("heartbeat over question = %+v; want question + loop", got)
	}

	clear := supervisorProjection(now, 8)
	clear.UnansweredQuestions = 0
	clear.LatestQuestionID = ""
	clear.LatestQuestionCreatedAt = time.Time{}
	h.acceptASLSupervisorProjection(now.Add(2*time.Second), target, clear)
	if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("reply projection = %+v; want underlying bolt + loop", got)
	}
}
