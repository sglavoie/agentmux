package indicator

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
)

type fakeDaemonClient struct {
	reqs  []daemon.Request
	resp  daemon.Response
	err   error
	calls int
}

func (f *fakeDaemonClient) SendWithAutoStart(req daemon.Request, _ string) (daemon.Response, error) {
	f.calls++
	f.reqs = append(f.reqs, req)
	return f.resp, f.err
}

func TestRunWithDeps_DropsUnavailableDaemonEventWithinBudget(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
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
	if got := client.reqs[0]; got.Type != daemon.TypeHookEvent || got.WindowID != 42 || got.Event != "Stop" || got.Harness != "claude" || got.PayloadJSON != `{"reason":"end_turn"}` || !got.PipeMode {
		t.Fatalf("hook request = %+v, want TypeHookEvent with raw payload", got)
	}
}

func TestRunWithDeps_ForwardsNormalizedFDAddress(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "fd:34")
	t.Setenv("KITTY_PID", "4242")
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
	if got := client.reqs[0].KittyListenOn; got != "unix:/tmp/kitty-4242" {
		t.Fatalf("forwarded KittyListenOn = %q, want normalized unix:/tmp/kitty-4242", got)
	}
}

func TestRunWithDeps_ForwardsEmptyWhenFDUnresolvable(t *testing.T) {
	// Inside Kitty (raw KITTY_LISTEN_ON set) but the fd: cannot be normalized
	// without a PID: still contact the daemon, forwarding an empty address so
	// the daemon falls back to its own established client.
	t.Setenv("KITTY_LISTEN_ON", "fd:34")
	t.Setenv("KITTY_PID", "")
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1 (fd: is still inside Kitty)", client.calls)
	}
	if got := client.reqs[0].KittyListenOn; got != "" {
		t.Fatalf("forwarded KittyListenOn = %q, want empty for unresolvable fd:", got)
	}
}

func TestRunWithDeps_OutsideKittyDoesNotContactDaemon(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "")
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 0 {
		t.Fatalf("daemon calls = %d, want 0 outside Kitty", client.calls)
	}
}

func TestRunWithDeps_InvalidWindowExitsZero(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	t.Setenv("KITTY_WINDOW_ID", "not-a-number")
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 0, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 0 {
		t.Fatalf("daemon calls = %d, want 0 for invalid Kitty window", client.calls)
	}
}

func TestRunWithDeps_AttachesRequestID(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
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
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, " Codex ", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if got := client.reqs[0].Harness; got != "codex" {
		t.Fatalf("forwarded Harness = %q, want codex", got)
	}
}

func TestRunWithDeps_ForwardsUnsupportedProducerAsUnknown(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, "future-harness", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if got := client.reqs[0].Harness; got != "future-harness" {
		t.Fatalf("forwarded Harness = %q, want explicit unknown producer preserved", got)
	}
}

func TestNewRequestID_UniqueAndWellFormed(t *testing.T) {
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
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	client := &fakeDaemonClient{resp: daemon.Response{OK: false, Error: "bad event"}}

	if err := runWithDeps("Stop", 42, "", "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
}
