package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

// The contract is deliberately the narrow pair the investigation surfaces need.
// Pinning that the real transport client satisfies it is what lets `amx daemon
// export-events`, `amx doctor`, and `amx daemon audit` share one seam and swap
// in a fixture without each declaring its own interface.
func TestDaemonExportClientIsSatisfiedByTheTransportClient(t *testing.T) {
	var _ DaemonExportClient = (*ipc.Client)(nil)

	client := NewDaemonExportClient()
	if client == nil {
		t.Fatal("NewDaemonExportClient returned nil")
	}
}

// Health is answerable with no daemon running, and answering it must not start
// one: that is how `amx daemon status` and `amx doctor` report "there is no
// daemon" instead of hanging or silently spawning one from a diagnostic
// command. HOME is redirected so the client dials a socket that cannot exist.
func TestDaemonHealthReportsAnAbsentDaemonWithoutStartingOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	socket := ipc.SocketPath()
	if !strings.HasPrefix(socket, home) {
		t.Fatalf("socket path %q escaped the test home %q", socket, home)
	}

	health := NewDaemonExportClient().Health()
	if health.Alive {
		t.Fatalf("reported a live daemon at %s", socket)
	}
	if health.Ready {
		t.Error("an absent daemon reported itself ready")
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Errorf("probing health created %s (stat err = %v)", socket, err)
	}
}
