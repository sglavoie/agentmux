package aslbridge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Degraded paths that involve no process supervision: the child is healthy and
// the corpus underneath it is not. These stay in-process on fakeBridge so the
// hermetic suite covers every refusal a real ASL can return without needing one
// installed.

// scriptedBridge serves snapshots and hands every other request to reply, which
// returns the frame the fake should send back.
func scriptedBridge(t *testing.T, reply func(f *fakeBridge, req map[string]any)) *fakeBridge {
	t.Helper()
	return newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			if f.answerHandshake(req) {
				return
			}
			if reqMethod(req) == MethodRunsSnapshot {
				f.respond(reqID(req), emptySnapshot())
				return
			}
			reply(f, req)
		})
	})
}

// A plan, run, or question that disappears between the snapshot the operator
// read and the action they submitted is the normal race, not an exception. Each
// one must come back as its own code so the UI can say what moved, and the
// published reason must carry the code alone: an ASL-authored message is free
// to quote the plan it just failed to find.
func TestVanishedTargetsRefuseWithTheirOwnCode(t *testing.T) {
	cases := []struct {
		name    string
		request ActionRequest
		refusal Error
	}{
		{
			name: "the plan directory is gone",
			request: ActionRequest{
				Method: MethodRunStopAfterCurrent, PlanDir: "/gone", RunID: "r1",
				OperationID: "op-1", ActionToken: "tok",
			},
			refusal: Error{Code: CodeInvalidPlan, Message: "/gone/session-3.md is unreadable"},
		},
		{
			name: "the run finished first",
			request: ActionRequest{
				Method: MethodRunAbort, PlanDir: "/p", RunID: "r1",
				OperationID: "op-2", ActionToken: "tok",
			},
			refusal: Error{Code: CodeNoActiveRun, Message: "run r1 for /p is no longer active"},
		},
		{
			name: "a different run holds the plan",
			request: ActionRequest{
				Method: MethodRunAbort, PlanDir: "/p", RunID: "r1",
				OperationID: "op-3", ActionToken: "tok",
			},
			refusal: Error{Code: CodeRunMismatch, Message: "/p is running r2, not r1"},
		},
		{
			name: "the question was answered elsewhere",
			request: ActionRequest{
				Method: MethodQuestionReply, PlanDir: "/p", QuestionID: "q1",
				OperationID: "op-4", ActionToken: "tok", Text: SecretText("ship it"),
			},
			refusal: Error{Code: CodeNoOpenQuestion, Message: "/p has no open question"},
		},
		{
			name: "another question is open now",
			request: ActionRequest{
				Method: MethodQuestionResolve, PlanDir: "/p", QuestionID: "q1",
				OperationID: "op-5", ActionToken: "tok", Resolution: ResolutionContinue,
			},
			refusal: Error{Code: CodeQuestionMismatch, Message: "/p is asking q2, not q1"},
		},
		{
			name: "the run resumed while the overlay was open",
			request: ActionRequest{
				Method: MethodRunResume, PlanDir: "/p", OperationID: "op-6",
				ActionToken: "tok", Text: SecretText("carry on"),
			},
			refusal: Error{Code: CodeNotPaused, Message: "/p is not paused"},
		},
		{
			name: "the mailbox cannot take another record",
			request: ActionRequest{
				Method: MethodQuestionReply, PlanDir: "/p", QuestionID: "q1",
				OperationID: "op-7", ActionToken: "tok", Text: SecretText("ship it"),
			},
			refusal: Error{Code: CodeMutationFailed, Message: "mailbox for /p is full"},
		},
		{
			name: "resuming could not spawn the harness",
			request: ActionRequest{
				Method: MethodRunResume, PlanDir: "/p", OperationID: "op-8",
				ActionToken: "tok", Text: SecretText("carry on"),
			},
			refusal: Error{Code: CodeMutationFailed, Message: "spawning claude for /p failed"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refusal := tc.refusal
			sent := make(chan struct{}, 4)
			fake := scriptedBridge(t, func(f *fakeBridge, req map[string]any) {
				sent <- struct{}{}
				f.refuse(req["id"].(string), refusal)
			})
			m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})
			waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

			_, err := m.Act(context.Background(), tc.request)
			var got *Error
			if !errors.As(err, &got) {
				t.Fatalf("Act error = %v, want a typed refusal", err)
			}
			if got.Code != refusal.Code {
				t.Errorf("code = %q, want %q", got.Code, refusal.Code)
			}
			if reason := sanitizeReason(err); reason != "bridge refused: "+refusal.Code {
				t.Errorf("published reason = %q, want the bare code", reason)
			}
			if len(sent) != 1 {
				t.Errorf("the mutation was sent %d times; a refusal is never retried", len(sent))
			}
			if len(m.Receipts()) != 0 {
				t.Error("a refused mutation must not enter the receipt window")
			}
			if !m.Status().MutationsEnabled {
				t.Error("a service-layer refusal is not a connection failure")
			}
		})
	}
}

// A snapshot projects the whole corpus, so its cost scales with the number of
// plans; measured against the installed bridge on a few hundred plans it took
// roughly ten seconds. Held to the interactive read budget the daemon abandoned
// every attempt and stayed read-only forever while the child was perfectly
// healthy, so the projection gets its own budget.
func TestASlowFirstProjectionStillEnablesMutations(t *testing.T) {
	slower := ReadTimeout + 100*time.Millisecond
	if SnapshotTimeout <= slower {
		t.Fatalf("SnapshotTimeout %s leaves no room above the read budget", SnapshotTimeout)
	}
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			if f.answerHandshake(req) {
				return
			}
			if reqMethod(req) != MethodRunsSnapshot {
				return
			}
			time.Sleep(slower)
			f.respond(reqID(req), emptySnapshot())
		})
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	waitFor(t, "the slow first snapshot", func() bool { return m.Status().MutationsEnabled })
}

// Refreshes are event-driven, and the bridge emits runs.changed only when the
// corpus actually moves. So a healthy child whose first snapshot was dropped is
// owed nothing: without a retry it would sit connected and permanently
// read-only until something happened to change on disk. Found against a real
// installed bridge, where one slow first projection cost the whole session.
func TestADroppedFirstSnapshotIsRetriedWithoutACorpusChange(t *testing.T) {
	var attempts atomic.Int32
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			if f.answerHandshake(req) {
				return
			}
			if reqMethod(req) != MethodRunsSnapshot {
				return
			}
			if attempts.Add(1) == 1 {
				f.refuse(reqID(req), Error{
					Code: CodeInternalError, Message: "the projection raised",
				})
				return
			}
			f.respond(reqID(req), emptySnapshot())
		})
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	waitFor(t, "the handshake", func() bool { return m.Status().Available })
	if m.Status().MutationsEnabled {
		t.Fatal("a handshake alone enabled mutations; only a snapshot may")
	}
	waitFor(t, "the retried snapshot", func() bool { return m.Status().MutationsEnabled })
}

// The bridge owns the Workbench process. If it cannot bind or the server dies,
// the deep link is unavailable and nothing is cached: the next attempt asks
// again rather than handing the operator a URL that answers nothing.
func TestWorkbenchBindFailureLeavesNothingCached(t *testing.T) {
	attempts := make(chan struct{}, 4)
	fake := scriptedBridge(t, func(f *fakeBridge, req map[string]any) {
		attempts <- struct{}{}
		f.refuse(req["id"].(string), Error{
			Code:    CodeWorkbenchUnavailable,
			Message: "binding 127.0.0.1:0 for /home/op/plans/alpha failed",
		})
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	for i := 0; i < 2; i++ {
		link, err := m.Workbench(context.Background(), WorkbenchRequest{PlanDir: "/p", Focus: FocusReply})
		var refusal *Error
		if !errors.As(err, &refusal) || refusal.Code != CodeWorkbenchUnavailable {
			t.Fatalf("Workbench error = %v, want %s", err, CodeWorkbenchUnavailable)
		}
		if link.URL != "" {
			t.Errorf("a failed ensure returned a url: %q", link.URL)
		}
	}
	if len(attempts) != 2 {
		t.Errorf("the bridge saw %d ensure calls, want one per request", len(attempts))
	}
	if m.Status().Reason != "" {
		t.Errorf("a Workbench refusal marked the whole integration degraded: %q", m.Status().Reason)
	}
}

// A bridge that accepts a mutation and never answers is the one case where the
// outcome is genuinely unknown. The daemon must give up inside ActionTimeout,
// report the timeout, and leave no receipt implying the action settled.
func TestUnansweredMutationTimesOutWithoutAReceipt(t *testing.T) {
	fake := scriptedBridge(t, func(f *fakeBridge, req map[string]any) {})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := m.Act(ctx, ActionRequest{
		Method: MethodRunStopAfterCurrent, PlanDir: "/p", RunID: "r1",
		OperationID: "op-hang", ActionToken: "tok",
	})
	if err == nil {
		t.Fatal("an unanswered mutation reported success")
	}
	if elapsed := time.Since(started); elapsed > ActionTimeout {
		t.Errorf("Act blocked for %s, longer than the %s budget", elapsed, ActionTimeout)
	}
	if len(m.Receipts()) != 0 {
		t.Error("a timed-out mutation left a receipt")
	}
	var refusal *Error
	if errors.As(err, &refusal) {
		t.Errorf("a timeout was reported as a bridge refusal %q", refusal.Code)
	}
}

// Operation IDs are the client's own, so a repeated one is one operation being
// re-reported, not two. The window keys on the ID and updates in place, which
// is what lets a terminal status replace the accepted receipt that preceded it.
func TestRepeatedOperationIDUpdatesOneReceiptInPlace(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		<-f.exited
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})
	waitFor(t, "the handshake", func() bool { return m.Status().Available })

	progression := []string{OperationAccepted, OperationConsumed, OperationSuperseded}
	for i, status := range progression {
		fake.emit(uint64(i+1), EventOperationUpdated, Operation{
			OperationID: "op-1",
			Method:      MethodQuestionReply,
			Status:      status,
		})
		waitFor(t, "receipt status "+status, func() bool {
			receipts := m.Receipts()
			return len(receipts) == 1 && receipts[0].Status == status
		})
	}

	fake.emit(uint64(len(progression)+1), EventOperationUpdated, Operation{
		OperationID: "op-2",
		Method:      MethodRunResume,
		Status:      OperationRejected,
	})
	waitFor(t, "a second operation", func() bool { return len(m.Receipts()) == 2 })

	receipts := m.Receipts()
	if receipts[0].OperationID != "op-1" || receipts[0].Status != OperationSuperseded {
		t.Errorf("first receipt = %+v, want op-1 superseded", receipts[0])
	}
	if receipts[1].OperationID != "op-2" || receipts[1].Status != OperationRejected {
		t.Errorf("second receipt = %+v, want op-2 rejected", receipts[1])
	}
}

// An operation the daemon never submitted can still arrive: a previous Agent Mux
// process issued it, or the operator acted in the Workbench. It is recorded like
// any other receipt rather than dropped, because it is evidence about a run the
// operator is watching.
func TestUnknownOperationReceiptIsStillRecorded(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		f.emit(1, EventOperationUpdated, Operation{
			OperationID: "op-from-a-previous-daemon",
			Method:      MethodQuestionResolve,
			Status:      OperationExpired,
			Detail:      "the run finished before the closure was observed",
		})
		<-f.exited
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	waitFor(t, "the foreign receipt", func() bool { return len(m.Receipts()) == 1 })
	if got := m.Receipts()[0]; got.Status != OperationExpired {
		t.Errorf("receipt = %+v, want an expired status", got)
	}
}
