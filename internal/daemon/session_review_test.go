package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func reviewFixture(t *testing.T) (*Handler, *fakeRuntimeDirectory, runtimesession.ID, int) {
	t.Helper()
	h, d := newRuntimeTestHandler(t)
	id := newRuntimeID(t)
	d.put(id, runtimesession.StateLive, runtimesession.BackendTmux, "/tmp/review-test.sock", "generation", "%0")
	requireAccepted(t, h.Handle(runtimeHook(t, id, "UserPromptSubmit", hookevent.HookPayload{SessionID: "conversation"})))
	slot := slotOf(t, h, id)
	h.runtimeLabels = map[string]runtimeLabel{string(id): {project: "Example"}}
	h.SetRuntimeCheckpointPath(filepath.Join(t.TempDir(), runtimeCheckpointFileName))
	h.loadSessionReviews()
	return h, d, id, slot
}
func reviewSnapshot(h *Handler, slot int) protocol.WindowStateSnapshot {
	return h.Handle(protocol.Request{Type: protocol.TypeStateQuery}).States[slot]
}
func editReview(h *Handler, r protocol.SessionReviewRequest) protocol.Response {
	return h.Handle(protocol.Request{Type: protocol.TypeSessionReview, Review: &r})
}
func markReview(t *testing.T, h *Handler, id runtimesession.ID, slot int) protocol.Response {
	t.Helper()
	yes := true
	r := editReview(h, protocol.SessionReviewRequest{Targets: map[string]string{string(id): reviewSnapshot(h, slot).Review.Epoch}, Accounted: &yes})
	if !r.OK || r.Review == nil {
		t.Fatalf("mark: %+v", r)
	}
	return r
}

func TestSessionReviewPersistsAndResurfacesWithoutChangingAgentState(t *testing.T) {
	h, d, id, slot := reviewFixture(t)
	note := "Implementation running; reviewer parked."
	before := reviewSnapshot(h, slot)
	yes := true
	response := editReview(h, protocol.SessionReviewRequest{Targets: map[string]string{string(id): before.Review.Epoch}, Accounted: &yes, Note: &note})
	if !response.OK {
		t.Fatal(response.Error)
	}
	got := reviewSnapshot(h, slot)
	if !got.Review.Accounted || got.Review.Note != note || got.IconState != before.IconState || got.Unseen != before.Unseen {
		t.Fatalf("annotation changed state or was lost: %+v", got)
	}
	next := checkpointHandler(t, h.runtimeCheckpointPath, d)
	next.loadSessionReviews()
	next.restoreRuntimeCheckpoint(time.Now())
	next.runtimeLabels = map[string]runtimeLabel{string(id): {project: "Example"}}
	nextSlot := slotOf(t, next, id)
	if got := reviewSnapshot(next, nextSlot).Review; !got.Accounted || got.Note != note {
		t.Fatalf("restart: %+v", got)
	}
	// Routine state transitions never invalidate operator judgement.
	next.stateMu.Lock()
	st, _ := next.windowStateLocked(nextSlot)
	for _, icon := range []string{"compact", "subagent", "bolt", ""} {
		next.publishEffectiveLocked(nextSlot, st, state.EffectiveState{Icon: icon})
	}
	next.stateMu.Unlock()
	if !reviewSnapshot(next, nextSlot).Review.Accounted {
		t.Fatal("routine churn cleared acknowledgement")
	}
	requireAccepted(t, next.Handle(runtimeHook(t, id, "Stop", hookevent.HookPayload{SessionID: "conversation"})))
	next.stateMu.Lock()
	pending := next.pendingStopHolds[nextSlot]
	next.stateMu.Unlock()
	next.commitStopHold(nextSlot, pending.seq)

	got = reviewSnapshot(next, nextSlot)
	if got.Review.Accounted || !got.Review.Reviewed || got.Review.Note != note {
		t.Fatalf("completion did not resurface while preserving note: %+v; events=%+v", got, next.Handle(protocol.Request{Type: protocol.TypeSessionDetail, RuntimeSessionID: string(id)}))
	}
	info, err := os.Stat(h.reviews.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private notes: %v %v", info, err)
	}
}

func TestSessionReviewBatchIsExactAndStaleAcknowledgementIsAtomic(t *testing.T) {
	h, d, id, slot := reviewFixture(t)
	second := newRuntimeID(t)
	d.put(second, runtimesession.StateLive, runtimesession.BackendTmux, "/tmp/review-test.sock", "generation", "%1")
	requireAccepted(t, h.Handle(runtimeHook(t, second, "UserPromptSubmit", hookevent.HookPayload{SessionID: "second"})))
	slot2 := slotOf(t, h, second)
	h.runtimeLabels[string(second)] = runtimeLabel{project: "Example"}
	targets := map[string]string{string(id): reviewSnapshot(h, slot).Review.Epoch, string(second): reviewSnapshot(h, slot2).Review.Epoch}
	yes := true
	note := "Both accounted for"
	req := protocol.SessionReviewRequest{Targets: targets, Project: "Example", Accounted: &yes, Note: &note}
	// A newer permission request must not be swallowed by an old project menu.
	requireAccepted(t, h.Handle(runtimeHook(t, second, "PermissionRequest", hookevent.HookPayload{SessionID: "second"})))
	if r := editReview(h, req); r.OK {
		t.Fatal("stale batch accepted")
	}
	if reviewSnapshot(h, slot).Review.Accounted || reviewSnapshot(h, slot).Review.ProjectNote != "" {
		t.Fatal("partial batch applied")
	}
	targets[string(second)] = reviewSnapshot(h, slot2).Review.Epoch
	if r := editReview(h, req); !r.OK {
		t.Fatal(r.Error)
	}
	if got := reviewSnapshot(h, slot2); !got.Review.Accounted || got.IconState != "key" || got.Review.ProjectNote != note {
		t.Fatalf("permission hidden or note absent: %+v", got)
	}
	// A newly arrived member is not covered by the project's earlier action.
	third := newRuntimeID(t)
	d.put(third, runtimesession.StateLive, runtimesession.BackendTmux, "/tmp/review-test.sock", "generation", "%2")
	requireAccepted(t, h.Handle(runtimeHook(t, third, "UserPromptSubmit", hookevent.HookPayload{SessionID: "third"})))
	slot3 := slotOf(t, h, third)
	h.runtimeLabels[string(third)] = runtimeLabel{project: "Example"}
	if got := reviewSnapshot(h, slot3).Review; got.Accounted || got.ProjectNote != note {
		t.Fatalf("new session: %+v", got)
	}
}

func TestSessionReviewUndoPreservesNewActivityAndRefusesConflicts(t *testing.T) {
	h, _, id, slot := reviewFixture(t)
	markReview(t, h, id, slot)
	note := "Waiting for implementation"
	saved := editReview(h, protocol.SessionReviewRequest{Targets: map[string]string{string(id): ""}, Note: &note})
	if !saved.OK {
		t.Fatal(saved.Error)
	}
	requireAccepted(t, h.Handle(runtimeHook(t, id, "PermissionRequest", hookevent.HookPayload{SessionID: "conversation"})))
	if r := editReview(h, protocol.SessionReviewRequest{UndoToken: saved.Review.UndoToken}); !r.OK {
		t.Fatal(r.Error)
	}
	if got := reviewSnapshot(h, slot).Review; got.Accounted || got.Note != "" {
		t.Fatalf("undo hid newer attention or retained note: %+v", got)
	}
	first := markReview(t, h, id, slot)
	markReview(t, h, id, slot)
	if r := editReview(h, protocol.SessionReviewRequest{UndoToken: first.Review.UndoToken}); r.OK {
		t.Fatal("undo overwrote later annotation")
	}
}

func TestSessionReviewSaveAndLoadFailuresRetainNotes(t *testing.T) {
	h, _, id, slot := reviewFixture(t)
	markReview(t, h, id, slot)
	fileBefore, err := os.ReadFile(h.reviews.path)
	if err != nil {
		t.Fatal(err)
	}
	h.reviews.path = filepath.Join(h.reviews.path, "cannot-write")
	no := false
	r := editReview(h, protocol.SessionReviewRequest{Targets: map[string]string{string(id): ""}, Accounted: &no})
	if r.OK || !reviewSnapshot(h, slot).Review.Accounted {
		t.Fatalf("failed save changed state: %+v", r)
	}
	data, err := os.ReadFile(filepath.Dir(h.reviews.path))
	if err != nil || string(data) != string(fileBefore) {
		t.Fatal("existing notes changed")
	}
	h.reviews.path = filepath.Dir(h.reviews.path)
	if err := os.WriteFile(h.reviews.path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	h.loadSessionReviews()
	if r := editReview(h, protocol.SessionReviewRequest{Targets: map[string]string{string(id): ""}, Accounted: &no}); r.OK {
		t.Fatal("corrupt notes replaced")
	}
}

func TestSessionReviewRejectsUnsafeNotes(t *testing.T) {
	h, _, id, _ := reviewFixture(t)
	for _, note := range []string{"line\nbreak", "escape\x1b[2J", strings.Repeat("界", 501), string([]byte{0xff})} {
		if r := editReview(h, protocol.SessionReviewRequest{Targets: map[string]string{string(id): ""}, Note: &note}); r.OK {
			t.Fatalf("accepted invalid note")
		}
	}
}

func TestSessionReviewSeesAgentCompletionMaskedByASL(t *testing.T) {
	h, _, id, slot := reviewFixture(t)
	markReview(t, h, id, slot)
	h.stateMu.Lock()
	st := h.store[slot]
	st.Reducer.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:conversation"}, Action: "stop", Seq: st.ShadowSeq + 10, At: time.Now().Unix(), SessionID: "conversation"})
	h.publishEffectiveLocked(slot, st, state.EffectiveState{Icon: "bolt", Looping: true})
	h.stateMu.Unlock()
	got := reviewSnapshot(h, slot)
	if got.Review.Accounted || got.IconState != "bolt" {
		t.Fatalf("hidden agent completion not surfaced: %+v", got)
	}
	markReview(t, h, id, slot)
	h.stateMu.Lock()
	h.publishEffectiveLocked(slot, st, state.EffectiveState{Icon: "bolt", Looping: true})
	h.stateMu.Unlock()
	if !reviewSnapshot(h, slot).Review.Accounted {
		t.Fatal("unchanged completion expired a new acknowledgement")
	}
}

func TestSessionReviewDistinguishesNewSameKindAttentionFromReplay(t *testing.T) {
	h, _, id, slot := reviewFixture(t)
	question := runtimeHook(t, id, "PermissionRequest", hookevent.HookPayload{SessionID: "conversation", SourceSequence: "10"})
	requireAccepted(t, h.Handle(question))
	markReview(t, h, id, slot)
	// Re-delivery of the same producer generation is not a new request.
	requireAccepted(t, h.Handle(question))
	if !reviewSnapshot(h, slot).Review.Accounted {
		t.Fatal("replay expired acknowledgement")
	}
	question = runtimeHook(t, id, "PermissionRequest", hookevent.HookPayload{SessionID: "conversation", SourceSequence: "11"})
	requireAccepted(t, h.Handle(question))
	if got := reviewSnapshot(h, slot); got.Review.Accounted || got.IconState != "key" {
		t.Fatalf("new same-kind request did not resurface: %+v", got)
	}
}

func TestSessionReviewExpiresWhenInteractiveProcessExitsToIdle(t *testing.T) {
	h, _, id, slot := reviewFixture(t)
	identity := protocol.HarnessProcessIdentity{Harness: "claude", PID: 5101, StartToken: "Thu Jul 16 10:20:30 2026"}
	applyLifecycleEvent(t, h, slot, identity, "conversation", "active", 20, false)
	markReview(t, h, id, slot)
	h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{statuses: map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection{identity: probes.HarnessProcessMissing}})
	if h.processLifecycleTick(context.Background(), time.Now()) != 1 {
		t.Fatal("process exit was not settled")
	}
	if got := reviewSnapshot(h, slot).Review; got.Accounted || !got.Reviewed {
		t.Fatalf("exited session stayed accounted for: %+v", got)
	}
}
