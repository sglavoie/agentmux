package commands

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

func TestStateQuerySeamBoundsItsWaitAtOneSecond(t *testing.T) {
	dir, err := os.MkdirTemp("", "amxq")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	// Accept and hold every connection open without ever replying.
	accepted := make(chan net.Conn, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		close(accepted)
		for conn := range accepted {
			_ = conn.Close()
		}
	})

	start := time.Now()
	if _, err := ipc.NewClientWithSocket(socket).StateQuery(); err == nil {
		t.Fatal("silent daemon returned no error, want a deadline failure")
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("state query gave up after %v, want it to hold the full one-second budget", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("state query waited %v, want it bounded near one second", elapsed)
	}
}
