package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

// startASLTestServer publishes a daemon socket backed by the given bridge, so
// the typed client helpers are exercised over real IPC rather than against the
// handler directly.
func startASLTestServer(t *testing.T, bridge asl.Bridge) string {
	t.Helper()
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "d.sock")
	handler := NewHandler(activity.NewStateGuard(0), nil)
	if bridge != nil {
		handler.SetASLBridge(bridge)
	}
	server, err := ipc.NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = os.RemoveAll(dir)
	})
	return socketPath
}

func TestClientASLRoundTrip(t *testing.T) {
	t.Parallel()

	const secret = "yes, roll it forward"
	bridge := &fakeASLBridge{
		status:    aslbridge.Status{Mode: string(aslbridge.ModeAuto), Available: true, MutationsEnabled: true},
		snapshot:  aslbridge.Snapshot{Protocol: 1, RowCount: 1, Digest: "d1"},
		meta:      aslbridge.SnapshotMeta{Generation: 3, AgeSeconds: 1},
		receipts:  []aslbridge.Operation{{OperationID: "op1", Status: aslbridge.OperationAccepted}},
		body:      aslbridge.QuestionBody{QuestionID: "q1", Body: aslbridge.SecretText("which region?")},
		operation: aslbridge.Operation{OperationID: "op2", Status: aslbridge.OperationAccepted},
		link:      aslbridge.WorkbenchLink{PlanDir: "/p", URL: "http://127.0.0.1:8765/plans/p"},
	}
	client := ipc.NewClientWithSocket(startASLTestServer(t, bridge))

	snapshot, meta, receipts, status, err := client.ASLSnapshot()
	if err != nil {
		t.Fatalf("ASLSnapshot: %v", err)
	}
	if snapshot.Digest != "d1" || meta.Generation != 3 || len(receipts) != 1 || !status.MutationsEnabled {
		t.Fatalf("snapshot=%+v meta=%+v receipts=%+v status=%+v", snapshot, meta, receipts, status)
	}

	body, err := client.ASLQuestionBody(aslbridge.QuestionRequest{PlanDir: "/p", QuestionID: "q1"})
	if err != nil {
		t.Fatalf("ASLQuestionBody: %v", err)
	}
	if body.Body.Reveal() != "which region?" {
		t.Fatalf("body = %q", body.Body.Reveal())
	}

	op, err := client.ASLAct(aslbridge.ActionRequest{
		Method: aslbridge.MethodQuestionReply, PlanDir: "/p", QuestionID: "q1",
		OperationID: "op2", ActionToken: "tok", Text: aslbridge.SecretText(secret),
	})
	if err != nil {
		t.Fatalf("ASLAct: %v", err)
	}
	if op.OperationID != "op2" {
		t.Fatalf("operation = %+v", op)
	}
	if bridge.lastAction.Text.Reveal() != secret {
		t.Fatalf("the reply text did not survive IPC: %q", bridge.lastAction.Text.Reveal())
	}
	if bridge.actions != 1 {
		t.Fatalf("bridge received %d actions; the client must not retry", bridge.actions)
	}

	link, err := client.ASLWorkbench(aslbridge.WorkbenchRequest{PlanDir: "/p", Focus: aslbridge.FocusActive})
	if err != nil {
		t.Fatalf("ASLWorkbench: %v", err)
	}
	if link.Focus != aslbridge.FocusActive {
		t.Fatalf("link = %+v", link)
	}
}

func TestClientASLReportsRefusals(t *testing.T) {
	t.Parallel()

	bridge := &fakeASLBridge{
		status: aslbridge.Status{Mode: string(aslbridge.ModeAuto)},
		err:    aslbridge.ErrUnavailable,
	}
	client := ipc.NewClientWithSocket(startASLTestServer(t, bridge))

	if _, _, _, _, err := client.ASLSnapshot(); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("ASLSnapshot error = %v, want the daemon's refusal", err)
	}
	if _, err := client.ASLAct(aslbridge.ActionRequest{
		Method: aslbridge.MethodRunAbort, PlanDir: "/p", OperationID: "op", ActionToken: "tok",
	}); err == nil {
		t.Fatal("ASLAct must surface the refusal")
	}
}

func TestClientASLDeadlinesOutlastTheDaemonBudget(t *testing.T) {
	t.Parallel()

	if asl.ReadDeadline <= aslbridge.ReadTimeout {
		t.Fatalf("client read deadline %s must outlast the daemon's %s", asl.ReadDeadline, aslbridge.ReadTimeout)
	}
	if asl.ActionDeadline <= aslbridge.ActionTimeout {
		t.Fatalf("client action deadline %s must outlast the daemon's %s", asl.ActionDeadline, aslbridge.ActionTimeout)
	}
}
