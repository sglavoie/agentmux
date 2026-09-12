package daemon

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestOverviewPreferenceSurvivesClientsButNotDaemonLifetime(t *testing.T) {
	sock := startTestServer(t)
	query := func(socket string, set *bool, want bool) {
		t.Helper()
		// Each call uses a new client, as reopening the F2 program does.
		resp, err := ipc.NewClientWithSocket(socket).Send(protocol.Request{
			Type: protocol.TypeOverviewPreferences, OverviewCompact: set,
		})
		if err != nil || !resp.OK || resp.OverviewCompact == nil || *resp.OverviewCompact != want {
			t.Fatalf("preference want=%v response=%+v err=%v", want, resp, err)
		}
	}
	query(sock, nil, false)
	compact := true
	query(sock, &compact, true)
	query(sock, nil, true)
	// A newly constructed daemon has no persisted preference to restore.
	query(startTestServer(t), nil, false)
	query(sock, nil, true)
	compact = false
	query(sock, &compact, false)
	query(sock, nil, false)
}
