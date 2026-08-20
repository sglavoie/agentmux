package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// fakeASLBridge is a scripted stand-in for the bridge manager. The handler is
// an adapter, so its tests assert on what it forwards and refuses rather than
// on protocol behaviour, which internal/aslbridge covers.
type fakeASLBridge struct {
	status    aslbridge.Status
	snapshot  aslbridge.Snapshot
	meta      aslbridge.SnapshotMeta
	receipts  []aslbridge.Operation
	body      aslbridge.QuestionBody
	operation aslbridge.Operation
	link      aslbridge.WorkbenchLink
	err       error

	lastAction   aslbridge.ActionRequest
	lastQuestion aslbridge.QuestionRequest
	actions      int
}

func (f *fakeASLBridge) Status() aslbridge.Status { return f.status }

func (f *fakeASLBridge) Snapshot() (aslbridge.Snapshot, aslbridge.SnapshotMeta, error) {
	if f.err != nil {
		return aslbridge.Snapshot{}, aslbridge.SnapshotMeta{}, f.err
	}
	return f.snapshot, f.meta, nil
}

func (f *fakeASLBridge) Receipts() []aslbridge.Operation { return f.receipts }

func (f *fakeASLBridge) QuestionBody(_ context.Context, req aslbridge.QuestionRequest) (aslbridge.QuestionBody, error) {
	f.lastQuestion = req
	if f.err != nil {
		return aslbridge.QuestionBody{}, f.err
	}
	return f.body, nil
}

func (f *fakeASLBridge) Act(_ context.Context, req aslbridge.ActionRequest) (aslbridge.Operation, error) {
	f.actions++
	f.lastAction = req
	if f.err != nil {
		return aslbridge.Operation{}, f.err
	}
	return f.operation, nil
}

func (f *fakeASLBridge) Workbench(_ context.Context, req aslbridge.WorkbenchRequest) (aslbridge.WorkbenchLink, error) {
	if f.err != nil {
		return aslbridge.WorkbenchLink{}, f.err
	}
	f.link.Focus = req.Focus
	return f.link, nil
}

func newASLHandler(bridge ASLBridge) *Handler {
	h := NewHandler(NewStateGuard(0), nil)
	if bridge != nil {
		h.SetASLBridge(bridge)
	}
	return h
}

func TestASLRequestsAreRefusedWithoutABridge(t *testing.T) {
	h := newASLHandler(nil)
	for _, msgType := range []MessageType{TypeASLSnapshot, TypeASLQuestion, TypeASLAction, TypeASLWorkbench} {
		resp := h.Handle(Request{Type: msgType, ASL: &ASLRequest{PlanDir: "/p"}})
		if resp.OK {
			t.Fatalf("%s succeeded without a bridge", msgType)
		}
		if resp.ASL == nil || resp.ASL.Status.Mode != string(aslbridge.ModeDisabled) {
			t.Fatalf("%s response = %+v, want a disabled capability block", msgType, resp.ASL)
		}
		if resp.Retryable {
			t.Fatalf("%s must not be retryable when ASL is disabled", msgType)
		}
	}
}

func TestASLUnavailabilityIsRetryable(t *testing.T) {
	h := newASLHandler(&fakeASLBridge{
		status: aslbridge.Status{Mode: string(aslbridge.ModeAuto)},
		err:    aslbridge.ErrUnavailable,
	})
	resp := h.Handle(Request{Type: TypeASLSnapshot})
	if resp.OK || !resp.Retryable {
		t.Fatalf("response = %+v, want a retryable refusal", resp)
	}
}

func TestASLSnapshotCarriesProvenanceAndReceipts(t *testing.T) {
	bridge := &fakeASLBridge{
		status:   aslbridge.Status{Mode: string(aslbridge.ModeAuto), Available: true, MutationsEnabled: true},
		snapshot: aslbridge.Snapshot{Protocol: 1, RowCount: 2, Digest: "d1"},
		meta:     aslbridge.SnapshotMeta{Generation: 7, AgeSeconds: 3},
		receipts: []aslbridge.Operation{{OperationID: "op1", Status: aslbridge.OperationAccepted}},
	}
	resp := newASLHandler(bridge).Handle(Request{Type: TypeASLSnapshot})
	if !resp.OK || resp.ASL == nil {
		t.Fatalf("response = %+v", resp)
	}
	if resp.ASL.Snapshot.Digest != "d1" || resp.ASL.Meta.Generation != 7 {
		t.Fatalf("snapshot/meta = %+v %+v", resp.ASL.Snapshot, resp.ASL.Meta)
	}
	if len(resp.ASL.Receipts) != 1 || resp.ASL.Receipts[0].OperationID != "op1" {
		t.Fatalf("receipts = %+v", resp.ASL.Receipts)
	}
}

func TestASLActionForwardsTypedParametersOnce(t *testing.T) {
	bridge := &fakeASLBridge{
		status:    aslbridge.Status{Mode: string(aslbridge.ModeAuto), Available: true, MutationsEnabled: true},
		operation: aslbridge.Operation{OperationID: "op9", Status: aslbridge.OperationAccepted},
	}
	resp := newASLHandler(bridge).Handle(Request{Type: TypeASLAction, ASL: &ASLRequest{
		Method:      aslbridge.MethodQuestionReply,
		PlanDir:     "/p",
		RunID:       "r1",
		QuestionID:  "q1",
		OperationID: "op9",
		ActionToken: "tok",
		Text:        aslbridge.SecretText("ship it"),
	}})
	if !resp.OK || resp.ASL == nil || resp.ASL.Operation == nil {
		t.Fatalf("response = %+v", resp)
	}
	if bridge.actions != 1 {
		t.Fatalf("bridge received %d actions; the handler must never retry", bridge.actions)
	}
	if bridge.lastAction.Text.Reveal() != "ship it" || bridge.lastAction.ActionToken != "tok" {
		t.Fatalf("forwarded action = %+v", bridge.lastAction)
	}
}

func TestASLActionWithoutParametersIsInvalid(t *testing.T) {
	bridge := &fakeASLBridge{status: aslbridge.Status{Mode: string(aslbridge.ModeAuto)}}
	resp := newASLHandler(bridge).Handle(Request{Type: TypeASLAction})
	if resp.OK || bridge.actions != 0 {
		t.Fatalf("response = %+v, actions = %d", resp, bridge.actions)
	}
}

func TestASLQuestionBodyIsNotLoggedOrCaptured(t *testing.T) {
	const secret = "the deploy key is abc123"
	bridge := &fakeASLBridge{
		status: aslbridge.Status{Mode: string(aslbridge.ModeAuto), Available: true},
		body:   aslbridge.QuestionBody{QuestionID: "q1", Body: aslbridge.SecretText(secret)},
	}
	h := newASLHandler(bridge)
	resp := h.Handle(Request{Type: TypeASLQuestion, ASL: &ASLRequest{PlanDir: "/p", QuestionID: "q1"}})
	if !resp.OK || resp.ASL == nil || resp.ASL.Question == nil {
		t.Fatalf("response = %+v", resp)
	}
	if resp.ASL.Question.Body.Reveal() != secret {
		t.Fatalf("body = %q", resp.ASL.Question.Body.Reveal())
	}
	// The caller receives the prose; every rendering path must not.
	if rendered := formatResponseForDiagnostics(resp); strings.Contains(rendered, "abc123") {
		t.Fatalf("a formatted response leaked the question body: %s", rendered)
	}
	// It stays out of the daemon's own per-window evidence too.
	for _, events := range h.Handle(Request{Type: TypeEventsExport}).EventsByWindow {
		for _, e := range events {
			if strings.Contains(e.QuestionPreview, "abc123") {
				t.Fatal("the question body reached the event export")
			}
		}
	}
	// It must still survive JSON transport to the one client that asked.
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), secret) {
		t.Fatal("the wire payload must carry the requested body")
	}
}

func TestASLWorkbenchForwardsFocus(t *testing.T) {
	bridge := &fakeASLBridge{
		status: aslbridge.Status{Mode: string(aslbridge.ModeAuto), Available: true},
		link:   aslbridge.WorkbenchLink{PlanDir: "/p", URL: "http://127.0.0.1:1/x"},
	}
	resp := newASLHandler(bridge).Handle(Request{Type: TypeASLWorkbench, ASL: &ASLRequest{
		PlanDir: "/p", Focus: aslbridge.FocusReply,
	}})
	if !resp.OK || resp.ASL == nil || resp.ASL.Workbench == nil {
		t.Fatalf("response = %+v", resp)
	}
	if resp.ASL.Workbench.Focus != aslbridge.FocusReply {
		t.Fatalf("workbench = %+v", resp.ASL.Workbench)
	}
}

func TestHealthPublishesASLCapabilityIndependentlyOfReadiness(t *testing.T) {
	bridge := &fakeASLBridge{status: aslbridge.Status{
		Mode: string(aslbridge.ModeRequired), Available: false, Reason: "bridge exited",
	}}
	h := newASLHandler(bridge)
	h.MarkReady()

	resp := h.handleHealth()
	if !resp.Ready {
		t.Fatal("an unavailable ASL bridge must not make the daemon unready")
	}
	if resp.ASLCapability == nil || resp.ASLCapability.Available {
		t.Fatalf("asl capability = %+v", resp.ASLCapability)
	}
	if resp.ASLCapability.Reason != "bridge exited" {
		t.Fatalf("reason = %q", resp.ASLCapability.Reason)
	}
}

func TestHealthOmitsASLCapabilityWithoutABridge(t *testing.T) {
	h := newASLHandler(nil)
	h.MarkReady()
	if resp := h.handleHealth(); resp.ASLCapability != nil {
		t.Fatalf("asl capability = %+v, want it omitted", resp.ASLCapability)
	}
}

func TestASLFailureKeepsTypedRefusalText(t *testing.T) {
	bridge := &fakeASLBridge{
		status: aslbridge.Status{Mode: string(aslbridge.ModeAuto), Available: true, MutationsEnabled: true},
		err:    &aslbridge.Error{Code: "stale-action-token", Message: "token is stale"},
	}
	resp := newASLHandler(bridge).Handle(Request{Type: TypeASLAction, ASL: &ASLRequest{
		Method: aslbridge.MethodRunAbort, PlanDir: "/p", OperationID: "op", ActionToken: "tok",
	}})
	if resp.OK || !strings.Contains(resp.Error, "stale-action-token") {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Retryable {
		t.Fatal("a refused mutation must not be advertised as retryable")
	}
}

// formatResponseForDiagnostics mimics a careless diagnostic rendering of a
// whole response value.
func formatResponseForDiagnostics(resp Response) string {
	return fmt.Sprintf("%v|%+v|%#v|%v|%+v|%#v",
		resp, resp, resp, *resp.ASL.Question, *resp.ASL.Question, *resp.ASL.Question)
}
