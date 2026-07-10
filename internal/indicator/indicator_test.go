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
	if err := runWithDeps("Stop", 42, "/tmp/amx", `{"reason":"end_turn"}`, true, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runWithDeps() took %v, want no longer than the auto-start budget", elapsed)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
	if got := client.reqs[0]; got.Type != daemon.TypeHookEvent || got.WindowID != 42 || got.Event != "Stop" || got.PayloadJSON != `{"reason":"end_turn"}` || !got.PipeMode {
		t.Fatalf("hook request = %+v, want TypeHookEvent with raw payload", got)
	}
}

func TestRunWithDeps_OutsideKittyDoesNotContactDaemon(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "")
	client := &fakeDaemonClient{}

	if err := runWithDeps("Stop", 42, "/tmp/amx", `{}`, false, client); err != nil {
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

	if err := runWithDeps("Stop", 0, "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 0 {
		t.Fatalf("daemon calls = %d, want 0 for invalid Kitty window", client.calls)
	}
}

func TestRunWithDeps_DaemonRejectionStillExitsZero(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	client := &fakeDaemonClient{resp: daemon.Response{OK: false, Error: "bad event"}}

	if err := runWithDeps("Stop", 42, "/tmp/amx", `{}`, false, client); err != nil {
		t.Fatalf("runWithDeps() error = %v, want nil", err)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
}
