package daemon

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

// Stopping a daemon that is not running stays an error, so a script can tell
// it from a stop that happened, but it says so plainly and never shows the
// socket error.
func TestDaemonStopWithNothingRunningSaysSoPlainly(t *testing.T) {
	resetDaemonLifecycleTestState(t)
	client := &fakeDaemonLifecycleClient{
		sendErr: errors.New("connecting to daemon: dial unix /tmp/daemon.sock: connect: no such file or directory"),
		healths: []ipc.Health{{Alive: false}},
	}
	newDaemonLifecycleClient = func() daemonLifecycleClient { return client }
	daemonStopCmd.SetOut(&bytes.Buffer{})
	err := daemonStopCmd.RunE(daemonStopCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "daemon is not running; nothing to stop") || strings.Contains(err.Error(), "dial unix") {
		t.Fatalf("RunE = %v; want the plain not-running error", err)
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
