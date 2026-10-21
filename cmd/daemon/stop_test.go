package daemon

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

// Stopping a daemon that is not running is what the operator wanted already:
// it succeeds, says so, and never shows the socket error.
func TestDaemonStopWithNothingRunningSucceeds(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{
		sendErr: errors.New("connecting to daemon: dial unix /tmp/daemon.sock: connect: no such file or directory"),
		healths: []ipc.Health{{Alive: false}},
	}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }
	var out bytes.Buffer
	daemonStopCmd.SetOut(&out)
	if err := daemonStopCmd.RunE(daemonStopCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if got := out.String(); got != "daemon is not running; nothing to stop\n" || strings.Contains(got, "dial unix") {
		t.Fatalf("output = %q", got)
	}
}

// A daemon that answers health but refuses the shutdown is still a failure.
func TestDaemonStopReportsAFailureOfARunningDaemon(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{sendErr: errors.New("write: broken pipe")}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }
	daemonStopCmd.SetOut(&bytes.Buffer{})
	if err := daemonStopCmd.RunE(daemonStopCmd, nil); err == nil || !strings.Contains(err.Error(), "stopping the daemon") {
		t.Fatalf("RunE = %v; want the stop failure", err)
	}
}
