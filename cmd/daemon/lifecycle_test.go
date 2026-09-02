package daemon

import (
	"bytes"
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
	oldStopOut := daemonStopCmd.OutOrStdout()
	oldRestartOut := daemonRestartCmd.OutOrStdout()
	t.Cleanup(func() {
		newDaemonLifecycleClient = oldClient
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
