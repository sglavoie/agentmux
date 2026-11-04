package daemon

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

type fakeDaemonLifecycleClient struct {
	sendResp     protocol.Response
	sendErr      error
	sendCalls    []protocol.Request
	restartErr   error
	restartCalls []string
	// healths is answered in order, the last one repeating.
	healths     []ipc.Health
	healthCalls int
}

func (f *fakeDaemonLifecycleClient) Health() ipc.Health {
	f.healthCalls++
	if len(f.healths) == 0 {
		return ipc.Health{Alive: true, PID: 4242, Ready: true}
	}
	i := min(f.healthCalls, len(f.healths)) - 1
	return f.healths[i]
}

func (f *fakeDaemonLifecycleClient) Send(req protocol.Request) (protocol.Response, error) {
	f.sendCalls = append(f.sendCalls, req)
	return f.sendResp, f.sendErr
}

func (f *fakeDaemonLifecycleClient) RestartDetached(amxBin string) error {
	f.restartCalls = append(f.restartCalls, amxBin)
	return f.restartErr
}

func resetDaemonLifecycleTestState(t *testing.T) {
	t.Helper()
	oldClient := newDaemonLifecycleClient
	oldBinary := daemonRestartBinary
	oldStopOut := daemonStopCmd.OutOrStdout()
	oldRestartOut := daemonRestartCmd.OutOrStdout()
	oldWait, oldNow, oldNap := daemonRestartWait, daemonRestartNow, daemonRestartNap
	// A fake clock that each nap advances keeps the wait loop free of sleeps.
	clock := time.Unix(0, 0)
	daemonRestartNow = func() time.Time { return clock }
	daemonRestartNap = func(d time.Duration) { clock = clock.Add(d) }
	t.Cleanup(func() {
		daemonRestartWait, daemonRestartNow, daemonRestartNap = oldWait, oldNow, oldNap
		newDaemonLifecycleClient = oldClient
		daemonRestartBinary = oldBinary
		daemonStopCmd.SetOut(oldStopOut)
		daemonRestartCmd.SetOut(oldRestartOut)
	})
}

func TestDaemonStopCommandWritesToCommandOutput(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{
		sendResp: protocol.Response{OK: true},
	}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }

	var out bytes.Buffer
	daemonStopCmd.SetOut(&out)

	if err := daemonStopCmd.RunE(daemonStopCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), "daemon stopped\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
	if len(client.sendCalls) != 1 || client.sendCalls[0].Type != protocol.TypeShutdown {
		t.Fatalf("Send calls = %+v; want one shutdown request", client.sendCalls)
	}
}

func TestDaemonRestartCommandWritesToCommandOutput(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }

	var out bytes.Buffer
	daemonRestartCmd.SetOut(&out)

	if err := daemonRestartCmd.RunE(daemonRestartCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), "daemon restarted (pid 4242)\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
	if len(client.restartCalls) != 1 || client.restartCalls[0] == "" {
		t.Fatalf("RestartDetached calls = %+v; want one non-empty binary path", client.restartCalls)
	}
}

// A daemon re-exec'd from a `go run` build would be started from a file Go
// deletes as soon as the restarting command exits, so the restart is refused
// before the running daemon is asked to shut down.
func TestDaemonRestartRefusesAGoRunBuild(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }
	daemonRestartBinary = func() (string, error) { return "/tmp/go-build1/b001/exe/agentmux", nil }

	err := daemonRestartCmd.RunE(daemonRestartCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to restart the daemon") {
		t.Fatalf("RunE error = %v; want a go run refusal", err)
	}
	if len(client.restartCalls) != 0 || len(client.sendCalls) != 0 {
		t.Fatalf("daemon contacted: restarts=%v sends=%v", client.restartCalls, client.sendCalls)
	}
}

// The replacement daemon binds its socket a moment after it is spawned, so
// restart keeps asking until it answers rather than reporting success first.
func TestDaemonRestartWaitsForTheNewDaemonToAnswer(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{healths: []ipc.Health{
		{}, {}, {}, {Alive: true, PID: 77, DegradedReason: "initial snapshot timed out"},
	}}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }
	daemonRestartBinary = func() (string, error) { return "/usr/local/bin/amx", nil }

	var out bytes.Buffer
	daemonRestartCmd.SetOut(&out)
	if err := daemonRestartCmd.RunE(daemonRestartCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got, want := out.String(), "daemon restarted (pid 77; not ready: initial snapshot timed out)\n"; got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
	if client.healthCalls != 4 {
		t.Fatalf("health calls = %d; want 4", client.healthCalls)
	}
}

func TestDaemonRestartFailsWhenTheNewDaemonNeverAnswers(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{healths: []ipc.Health{{}}}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }
	daemonRestartBinary = func() (string, error) { return "/usr/local/bin/amx", nil }
	daemonRestartWait = time.Second

	var out bytes.Buffer
	daemonRestartCmd.SetOut(&out)
	err := daemonRestartCmd.RunE(daemonRestartCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "did not answer within 1s") {
		t.Fatalf("RunE error = %v; want a timeout naming the budget", err)
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q; want nothing claiming a restart", out.String())
	}
}
