package indicator

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

type fakeDaemonClient struct {
	reqs  []protocol.Request
	resp  protocol.Response
	err   error
	calls int
}

func (f *fakeDaemonClient) SendWithAutoStart(req protocol.Request, _ string) (protocol.Response, error) {
	f.calls++
	f.reqs = append(f.reqs, req)
	return f.resp, f.err
}

func TestRunWithDeps_DropsUnavailableDaemonEventWithinBudget(t *testing.T) {
	client := &fakeDaemonClient{err: errors.New("daemon unavailable")}

	started := time.Now()
	if err := runWithDeps("Stop", 42, "claude", "/tmp/amx", `{"reason":"end_turn"}`, true, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runWithDeps() took %v, want no longer than the auto-start budget", elapsed)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
	if got := client.reqs[0]; got.Type != protocol.TypeHookEvent || got.RuntimeSessionID != testRuntimeSession || got.WindowID != 0 || got.Event != "Stop" || got.Harness != "claude" || got.PayloadJSON != `{"reason":"end_turn"}` || !got.PipeMode {
		t.Fatalf("hook request = %+v, want TypeHookEvent with raw payload", got)
	}
}

func TestRunWithDeps_WithoutRuntimeDoesNotContactDaemon(t *testing.T) {
	t.Setenv(runtimesession.EnvVar, "")
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 0 {
		t.Fatalf("daemon calls = %d, want 0 without a runtime session", client.calls)
	}
}

func TestRunWithDeps_ManagedHookIgnoresInvalidWindow(t *testing.T) {
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 0, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 1 || client.reqs[0].WindowID != 0 {
		t.Fatalf("managed hook must use runtime identity: calls=%d requests=%+v", client.calls, client.reqs)
	}
}

func TestRunWithDeps_AttachesRequestID(t *testing.T) {
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
	if got := client.reqs[0].RequestID; got == "" {
		t.Fatal("RequestID is empty; hook requests must carry a dedup ID")
	}
}

func TestRunWithDeps_NormalizesKnownProducerHarness(t *testing.T) {
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, " Codex ", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if got := client.reqs[0].Harness; got != "codex" {
		t.Fatalf("forwarded Harness = %q, want codex", got)
	}
}

func TestRunWithDeps_ForwardsUnsupportedProducerAsUnknown(t *testing.T) {
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, "future-harness", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if got := client.reqs[0].Harness; got != "future-harness" {
		t.Fatalf("forwarded Harness = %q, want explicit unknown producer preserved", got)
	}
}

func TestNewRequestID_UniqueAndWellFormed(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := newRequestID()
		if len(id) != 36 { // 8-4-4-4-12 hex UUID
			t.Fatalf("request ID %q has length %d; want 36", id, len(id))
		}
		if seen[id] {
			t.Fatalf("duplicate request ID generated: %q", id)
		}
		seen[id] = true
	}
}

func TestRunWithDeps_DaemonRejectionStillExitsZero(t *testing.T) {
	client := &fakeDaemonClient{resp: protocol.Response{OK: false, Error: "bad event"}}

	if err := runWithDeps("Stop", 42, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
}
