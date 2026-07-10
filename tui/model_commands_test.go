package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
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
