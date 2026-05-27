package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func BenchmarkRun_StartupAndShutdown(b *testing.B) {
	prevHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", prevHome) }()

	cfg := &config.Config{}
	cfg.Defaults.GuardDuration = 1

	for i := 0; i < b.N; i++ {
		dir, err := os.MkdirTemp("/tmp", "amx-daemon-bench-*")
		if err != nil {
			b.Fatalf("MkdirTemp: %v", err)
		}
		if err := os.Setenv("HOME", dir); err != nil {
			_ = os.RemoveAll(dir)
			b.Fatalf("Setenv: %v", err)
		}
		socketPath := SocketPath()

		ctx, cancel := context.WithCancel(context.Background())
		runErr := make(chan error, 1)
		go func() { runErr <- Run(ctx, cfg) }()

		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(socketPath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				cancel()
				_ = os.RemoveAll(dir)
				b.Fatalf("socket not created at %s", socketPath)
			}
			time.Sleep(time.Millisecond)
		}

		c := NewClientWithSocket(socketPath)
		resp, err := c.Send(Request{Type: TypeShutdown})
		if err != nil {
			cancel()
			_ = os.RemoveAll(dir)
			b.Fatalf("shutdown Send: %v", err)
		}
		if !resp.OK {
			cancel()
			_ = os.RemoveAll(dir)
			b.Fatalf("shutdown response: %+v", resp)
		}

		select {
		case err := <-runErr:
			if err != nil {
				_ = os.RemoveAll(dir)
				b.Fatalf("Run: %v", err)
			}
		case <-time.After(3 * time.Second):
			cancel()
			_ = os.RemoveAll(dir)
			b.Fatal("Run did not return after shutdown")
		}

		if _, err := os.Stat(filepath.Join(dir, ".local", "share", "agent-mux", "transcripts.json")); err == nil {
			_ = os.RemoveAll(dir)
			b.Fatal("daemon startup loaded or created transcript metadata unexpectedly")
		}
		_ = os.RemoveAll(dir)
	}
}
