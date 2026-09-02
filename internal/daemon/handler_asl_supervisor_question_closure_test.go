package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

func TestASLSupervisorMailboxReplyClearsMatchingProbeImmediately(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
	h.renderQueue = rendering.NewQueue()

	deliverSupervisorMailboxReply(
		t,
		h,
		supervisorMailboxReplyPayload(target, 7, 8),
	)

	if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("matching mailbox reply = %+v; want underlying bolt + loop", got)
	}
	if got := h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[42]; got.IconState != "bolt" || !got.Looping {
		t.Fatalf("dashboard snapshot after reply = %+v; want bolt + loop", got)
	}
	if h.renderQueue.Empty() {
		t.Fatal("matching mailbox reply did not queue Kitty title reconciliation")
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	ref := aslLeaseRef{windowID: 42, key: supervisorRunID}
	if _, ok := h.aslSupervisorProjections[ref]; ok {
		t.Fatal("matching mailbox reply retained cached projection")
	}
	if got := h.aslSupervisorClosureCutoffs[ref]; got != 8 {
		t.Fatalf("closure cutoff = %d; want 8", got)
	}
}

func TestASLSupervisorMailboxResolutionClearsMatchingProbeImmediately(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
	payload := supervisorMailboxReplyPayload(target, 7, 7)
	payload.Reason = aslMailboxResolutionReason
	payload.ResolutionAction = "continue"
	payload.SourceEventID = supervisorRunID + ":mailbox-resolution:7"

	resp := h.Handle(protocol.Request{
		Type:        protocol.TypeHookEvent,
		WindowID:    42,
		Event:       events.AgentSessionLoopMailboxResolution,
		PayloadJSON: hookPayloadJSON(t, payload),
	})
	if !resp.OK {
		t.Fatalf("mailbox resolution response = %+v", resp)
	}
	if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("matching mailbox resolution = %+v; want underlying bolt + loop", got)
	}
}

func TestASLSupervisorMailboxReplyRejectsMismatchedEvidence(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*hookevent.HookPayload)
	}{
		{"source", func(p *hookevent.HookPayload) { p.Source = "other" }},
		{"reason", func(p *hookevent.HookPayload) { p.Reason = "other" }},
		{"run", func(p *hookevent.HookPayload) { p.RunID = "other-run" }},
		{"state", func(p *hookevent.HookPayload) { p.StatePath = filepath.Join(t.TempDir(), "state.json") }},
		{"projection after closure", func(p *hookevent.HookPayload) { p.MailboxProjectionSequence = "9" }},
		{"event identity", func(p *hookevent.HookPayload) { p.SourceEventID = "wrong" }},
		{"malformed sequence", func(p *hookevent.HookPayload) { p.SourceSequence = "not-a-sequence" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, target, now := supervisorQuestionFixture(t, nil)
			applySupervisorStop(h, now)
			h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
			payload := supervisorMailboxReplyPayload(target, 7, 8)
			tc.mutate(&payload)
			deliverSupervisorMailboxReply(t, h, payload)
			if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
				t.Fatalf("mismatched reply = %+v; want question + loop", got)
			}
		})
	}
}

func TestASLSupervisorMailboxReplyClearsAuthoritativelyClosedMultipleQuestionSet(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	projection := supervisorProjection(now, 7)
	projection.UnansweredQuestions = 2
	h.acceptASLSupervisorProjection(now, target, projection)

	deliverSupervisorMailboxReply(
		t,
		h,
		supervisorMailboxReplyPayload(target, 7, 8),
	)

	if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("authoritative final reply = %+v; want underlying bolt + loop", got)
	}
}

func TestASLSupervisorMailboxReplyCutoffBlocksStaleProjection(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)

	deliverSupervisorMailboxReply(
		t,
		h,
		supervisorMailboxReplyPayload(target, 7, 8),
	)
	h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
	if got := supervisorVisibleState(t, h); got.Icon != "bolt" || !got.Looping {
		t.Fatalf("stale projection after reply = %+v; want bolt + loop", got)
	}

	newQuestion := supervisorProjection(now.Add(time.Second), 9)
	newQuestion.LatestQuestionID = "00000000-0000-0000-0000-000000000022"
	h.acceptASLSupervisorProjection(now.Add(time.Second), target, newQuestion)
	if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
		t.Fatalf("newer question after reply = %+v; want question + loop", got)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if _, ok := h.aslSupervisorClosureCutoffs[aslLeaseRef{windowID: 42, key: supervisorRunID}]; ok {
		t.Fatal("newer projection did not retire closure cutoff")
	}
}

func TestASLSupervisorStaleMailboxClosureDoesNotClearNewerQuestion(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	newQuestion := supervisorProjection(now.Add(time.Second), 9)
	newQuestion.LatestQuestionID = "00000000-0000-0000-0000-000000000022"
	h.acceptASLSupervisorProjection(now.Add(time.Second), target, newQuestion)

	deliverSupervisorMailboxReply(
		t,
		h,
		supervisorMailboxReplyPayload(target, 7, 8),
	)
	if got := supervisorVisibleState(t, h); got.Icon != "question" || !got.Looping {
		t.Fatalf("stale closure after newer question = %+v; want question + loop", got)
	}
}

func TestASLSupervisorMailboxReplyLeavesRunnerWaitingLoopBare(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	applySupervisorStop(h, now)
	h.acceptASLSupervisorProjection(now, target, supervisorProjection(now, 7))
	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.Apply(protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceASL, Key: supervisorRunID},
		RunID:  supervisorRunID,
		Phase:  state.ASLPhaseWaiting,
		Seq:    2,
		At:     now.Unix(),
	})
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(now.Unix()))
	h.stateMu.Unlock()

	deliverSupervisorMailboxReply(
		t,
		h,
		supervisorMailboxReplyPayload(target, 7, 8),
	)

	if got := supervisorVisibleState(t, h); got.Icon != "" || !got.Looping {
		t.Fatalf("runner waiting state after supervisor clear = %+v; want bare ASL loop", got)
	}
}

func TestASLSupervisorQuestionUserClearIsNotReassertedBySameProjection(t *testing.T) {
	h, target, now := supervisorQuestionFixture(t, nil)
	projection := supervisorProjection(now, 7)
	applySupervisorStop(h, now)
	h.acceptASLSupervisorProjection(now, target, projection)

	h.stateMu.Lock()
	st := h.store[42]
	st.Reducer.Apply(protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "user"},
		Action: "user_clear",
		Seq:    1,
		At:     now.Add(time.Second).Unix(),
	})
	h.publishEffectiveLocked(42, st, st.Reducer.Reduce(now.Add(time.Second).Unix()))
	h.stateMu.Unlock()
	projection.ProjectionSeq = 8
	projection.UpdatedAt = now.Add(2 * time.Second)
	h.acceptASLSupervisorProjection(now.Add(2*time.Second), target, projection)

	if got := supervisorVisibleState(t, h); got.Icon != "" || !got.Looping {
		t.Fatalf("same question after user clear = %+v; want acknowledged bare loop", got)
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
	if _, ok := h.aslSupervisorClosureCutoffs[ref]; ok {
		t.Fatal("finished run retained supervisor closure cutoff")
	}
}
