package tui

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestHealthCmd_StartsDaemonWhenDead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldSpawn := spawnDaemonDetached
	spawned := ""
	spawnDaemonDetached = func(amxBin string) error {
		spawned = amxBin
		return nil
	}
	t.Cleanup(func() { spawnDaemonDetached = oldSpawn })

	msg, ok := healthCmd(&config.Config{}, "/tmp/amx", true, false)().(healthMsg)
	if !ok {
		t.Fatal("healthCmd returned an unexpected message type")
	}
	if msg.daemonAlive {
		t.Fatal("daemonAlive = true, want false before the detached daemon is ready")
	}
	if spawned != "/tmp/amx" {
		t.Fatalf("SpawnDetached path = %q, want /tmp/amx", spawned)
	}
}

// TestApplyDaemonStatesStampsBusySince asserts the busy anchor follows the
// daemon: a reported anchor lands as a real timestamp, an omitted one stays
// the zero time rather than the unix epoch, and a window the daemon has no
// entry for is cleared alongside State and Looping.
func TestApplyDaemonStatesStampsBusySince(t *testing.T) {
	anchor := time.Unix(1710000100, 0)
	sessions := []session.RunningSession{
		{WindowID: 1},
		{WindowID: 2},
		{WindowID: 3, State: "bolt", Looping: true, BusySince: anchor},
	}
	applyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{
		1: {IconState: "bolt", BusySinceAt: anchor.Unix()},
		2: {IconState: "key"},
	})

	if !sessions[0].BusySince.Equal(anchor) {
		t.Errorf("BusySince = %v, want the daemon's anchor %v", sessions[0].BusySince, anchor)
	}
	if !sessions[1].BusySince.IsZero() {
		t.Errorf("BusySince = %v for an idle window, want the zero time", sessions[1].BusySince)
	}
	if !sessions[2].BusySince.IsZero() {
		t.Errorf("BusySince = %v for a window the daemon does not know, want the zero time", sessions[2].BusySince)
	}
}
