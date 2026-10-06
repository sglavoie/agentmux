package daemon

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

type fakeDaemonLifecycleClient struct {
	sendResp     protocol.Response
	sendErr      error
	sendCalls    []protocol.Request
	restartErr   error
	restartCalls []string
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
	t.Cleanup(func() {
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
	if got, want := out.String(), "daemon restarted\n"; got != want {
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
