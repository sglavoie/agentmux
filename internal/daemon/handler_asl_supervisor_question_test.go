package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
)

const (
	supervisorRunID      = "run-supervised"
	supervisorQuestionID = "00000000-0000-0000-0000-000000000011"
)

func supervisorQuestionFixture(t *testing.T, notifier *Notifier) (*Handler, aslSupervisorTarget, time.Time) {
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
	st.Reducer = NewReducer(ReducerConfig{})
	st.Reducer.Apply(ReducerEvent{
		Source: SourceID{Kind: SourceASL, Key: supervisorRunID},
		RunID:  supervisorRunID,
		Phase:  ASLPhaseTurn,
		Seq:    1,
		At:     now.Add(-10 * time.Second).Unix(),
	})
	st.Reducer.Apply(ReducerEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "codex:parent"},
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
	n := NormalizedEvent{
		Source:        SourceID{Kind: SourceHarness, Key: "codex:parent"},
		Action:        "stop",
		Seq:           2,
		TurnID:        "turn-parent",
		At:            now.Unix(),
		SessionID:     "parent",
		EndsSession:   true,
		Event:         "Stop",
		SourceEventID: "parent-stop",
	}
	req := Request{
		WindowID:     42,
		SessionTitle: "W:api",
		Account:      "work",
		Harness:      "codex",
		normalized:   &n,
	}
	h.reduceStateUpdate(req, applyOutcome{}, Response{OK: true}, now)
}

func supervisorVisibleState(t *testing.T, h *Handler) EffectiveState {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[42]
	if st == nil || st.Reducer == nil {
		t.Fatal("missing supervisor fixture state")
	}
	return st.Reducer.Reduce(time.Now().Unix())
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

func TestASLSupervisorQuestionSurvivesHeartbeatAndClearsOnReply(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))

	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.Apply(ReducerEvent{
		Source: SourceID{Kind: SourceASL, Key: supervisorRunID},
		RunID:  supervisorRunID,
		Phase:  ASLPhaseTurn,
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

func TestASLSupervisorQuestionUserClearIsNotReassertedBySameProjection(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	projection := supervisorProjection(now, 7)
	applySupervisorStop(h, now)
	h.acceptASLSupervisorProjection(now, target, projection)

	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.Apply(ReducerEvent{
		Source: SourceID{Kind: SourceProbe, Key: "user"},
		Action: "user_clear",
		Seq:    1,
		At:     now.Add(time.Second).Unix(),
	})
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(now.Add(time.Second).Unix()))
	h.stateMu.Unlock()
	h.acceptASLSupervisorProjection(now.Add(2*time.Second), target, projection)

	if got := supervisorVisibleState(t, h); got.Icon != "" || !got.Looping {
		t.Fatalf("same projection after user clear = %+v; want acknowledged bare loop", got)
	}
}

func TestASLSupervisorQuestionRunFinishRetiresProbeAndTarget(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
	h.finishASLSupervisorQuestion(42, supervisorRunID, now.Add(time.Second))

	if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("probe after finish cleanup = %+v; want underlying bolt + loop", got)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	ref := aslLeaseRef{windowID: 42, key: supervisorRunID}
	if _, ok := h.aslSupervisorTargets[ref]; ok {
		t.Fatal("finished run retained supervisor target")
	}
	if _, ok := h.aslSupervisorProjections[ref]; ok {
		t.Fatal("finished run retained supervisor projection")
	}
}
