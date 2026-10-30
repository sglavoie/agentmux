package tmux

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// A successful command acknowledgement is not proof that shutdown finished.
// A server that keeps accepting connections must not be reported as stopped.
func TestKillRefusesToReportAStillListeningServerAsStopped(t *testing.T) {
	server, err := NewServer(Options{Dir: filepath.Join(shortDir(t), "server"), Binary: fakeExecutable(t), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", server.Socket())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	if err := server.Kill(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("kill acknowledged while socket still listens = %v, want a shutdown timeout", err)
	}
}

func TestKillAcceptsAStaleSocketAsStopped(t *testing.T) {
	server, err := NewServer(Options{Dir: filepath.Join(shortDir(t), "server"), Binary: fakeExecutable(t)})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", server.Socket())
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()
	if err := server.Kill(); err != nil {
		t.Fatalf("kill over a stale socket: %v", err)
	}
}
