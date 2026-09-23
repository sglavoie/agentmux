package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestOverviewPreferenceSurvivesClientsButNotDaemonLifetime(t *testing.T) {
	sock := startTestServer(t)
	query := func(socket string, set *bool, want, explicit bool) {
		t.Helper()
		// Each call uses a new client, as reopening the F2 program does.
		resp, err := ipc.NewClientWithSocket(socket).Send(protocol.Request{
			Type: protocol.TypeOverviewPreferences, OverviewCompact: set,
		})
		if err != nil || !resp.OK || resp.OverviewCompact == nil || *resp.OverviewCompact != want ||
			resp.OverviewCompactExplicit != explicit {
			t.Fatalf("preference want=%v explicit=%v response=%+v err=%v", want, explicit, resp, err)
		}
	}
	// Until a client writes it, the value is not a choice: the overview keeps
	// its configured opening mode.
	query(sock, nil, false, false)
	compact := true
	query(sock, &compact, true, true)
	query(sock, nil, true, true)
	// A newly constructed daemon has no persisted preference to restore.
	query(startTestServer(t), nil, false, false)
	query(sock, nil, true, true)
	compact = false
	query(sock, &compact, false, true)
	query(sock, nil, false, true)
}
