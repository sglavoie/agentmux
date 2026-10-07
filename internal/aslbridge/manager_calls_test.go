package aslbridge

// The request surface: action validation and at-most-once delivery, event-driven
// refresh, the bounded body-free receipt window, and question bodies that are
// handed to the caller without ever being retained.

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestActionIsSentExactlyOnceOnRefusal(t *testing.T) {
	var aborts int
	var mu sync.Mutex
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			if f.answerHandshake(req) {
				return
			}
			switch reqMethod(req) {
			case MethodRunsSnapshot:
				f.respond(reqID(req), emptySnapshot())
			case MethodRunAbort:
				mu.Lock()
				aborts++
				mu.Unlock()
				f.refuse(reqID(req), Error{Code: "stale-action-token", Message: "token for plan /home/op/x is stale"})
			}
		})
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	_, err := m.Act(context.Background(), ActionRequest{
		Method: MethodRunAbort, PlanDir: "/p", RunID: "r", OperationID: "op", ActionToken: "tok",
	})
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Code != "stale-action-token" {
		t.Fatalf("Act error = %v, want a typed stale-action-token refusal", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if aborts != 1 {
		t.Fatalf("run.abort was sent %d times; a mutation must never be retried", aborts)
	}
}

func TestActionValidationRejectsIncompleteRequests(t *testing.T) {
	m := New(Options{Mode: ModeAuto, Command: "asl", Logger: discardLogger()})
	cases := map[string]ActionRequest{
		"missing plan_dir":     {Method: MethodRunAbort, OperationID: "op", ActionToken: "tok"},
		"missing operation_id": {Method: MethodRunAbort, PlanDir: "/p", ActionToken: "tok"},
		"missing action_token": {Method: MethodRunAbort, PlanDir: "/p", OperationID: "op"},
		"missing reply text":   {Method: MethodQuestionReply, PlanDir: "/p", OperationID: "op", ActionToken: "tok"},
		"missing resolution":   {Method: MethodQuestionResolve, PlanDir: "/p", OperationID: "op", ActionToken: "tok"},
		"unknown method":       {Method: "run.delete", PlanDir: "/p", OperationID: "op", ActionToken: "tok"},
	}
	for name, req := range cases {
		if _, err := m.Act(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: error = %v, want ErrInvalidRequest", name, err)
		}
	}
}

func TestOperationEventsRecordBoundedBodyFreeReceipts(t *testing.T) {
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		for i := 0; i < maxReceipts+10; i++ {
			f.emit(uint64(i+1), EventOperationUpdated, Operation{
				OperationID: string(rune('a'+i%26)) + string(rune('0'+i/26)),
				Method:      MethodQuestionReply,
				Status:      OperationConsumed,
			})
		}
		<-f.exited
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	waitFor(t, "receipt window to fill", func() bool { return len(m.Receipts()) == maxReceipts })
	for _, op := range m.Receipts() {
		if op.Status != OperationConsumed {
			t.Fatalf("receipt = %+v", op)
		}
	}
}

func TestRunsChangedTriggersRefresh(t *testing.T) {
	digests := make(chan string, 4)
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		generation := 0
		for {
			select {
			case req := <-f.requests:
				// An unanswered handshake would hold each refresh for the
				// full HandshakeTimeout before it reported the snapshot.
				if f.answerHandshake(req) || req["method"] != MethodRunsSnapshot {
					continue
				}
				generation++
				snapshot := emptySnapshot()
				snapshot["digest"] = "d" + string(rune('0'+generation))
				f.respond(req["id"].(string), snapshot)
				digests <- snapshot["digest"].(string)
			case <-f.exited:
				return
			}
		}
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})

	<-digests
	fake.emit(1, EventRunsChanged, map[string]any{"digest": "d2", "row_count": 0})
	<-digests

	waitFor(t, "second snapshot", func() bool {
		_, meta, err := m.Snapshot()
		return err == nil && meta.Generation >= 2
	})
}

func TestQuestionBodyIsFetchedAndNeverRetained(t *testing.T) {
	const body = "Should I delete the production bucket?"
	fake := newFakeBridge(t, func(f *fakeBridge) {
		f.hello()
		serveRequests(f, func(req map[string]any) {
			if f.answerHandshake(req) {
				return
			}
			switch reqMethod(req) {
			case MethodRunsSnapshot:
				f.respond(reqID(req), emptySnapshot())
			case MethodQuestionGet:
				f.respond(reqID(req), map[string]any{
					"plan_dir": "/p", "question_id": "q1", "body": body, "body_bytes": len(body),
					"action_token": "tok",
				})
			}
		})
	})
	m := startManager(t, Options{Mode: ModeAuto, Command: "asl", Launcher: fake.launcher()})
	waitFor(t, "mutations enabled", func() bool { return m.Status().MutationsEnabled })

	got, err := m.QuestionBody(context.Background(), QuestionRequest{PlanDir: "/p", QuestionID: "q1"})
	if err != nil {
		t.Fatalf("QuestionBody: %v", err)
	}
	if got.Body.Reveal() != body {
		t.Fatalf("body = %q", got.Body.Reveal())
	}
	if len(m.Receipts()) != 0 {
		t.Fatal("fetching a question body must not create a receipt")
	}
	for method := range m.Metrics().RequestsByMethod {
		switch method {
		case MethodSessionHandshake, MethodRunsSnapshot, MethodQuestionGet:
		default:
			t.Fatalf("metrics recorded an unexpected method key %q", method)
		}
	}
}
