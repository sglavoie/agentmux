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
		func() {
			dir, err := os.MkdirTemp("/tmp", "amx-daemon-bench-*")
			if err != nil {
				b.Fatalf("MkdirTemp: %v", err)
			}
			defer func() { _ = os.RemoveAll(dir) }()
			if err := os.Setenv("HOME", dir); err != nil {
				b.Fatalf("Setenv: %v", err)
			}
			socketPath := SocketPath()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runErr := make(chan error, 1)
			go func() { runErr <- Run(ctx, cfg) }()

			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(socketPath); err == nil {
					break
				}
				if time.Now().After(deadline) {
					b.Fatalf("socket not created at %s", socketPath)
				}
				time.Sleep(time.Millisecond)
			}

			c := NewClientWithSocket(socketPath)
			resp, err := c.Send(Request{Type: TypeShutdown})
			if err != nil {
				b.Fatalf("shutdown Send: %v", err)
			}
			if !resp.OK {
				b.Fatalf("shutdown response: %+v", resp)
			}

			select {
			case err := <-runErr:
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
			case <-time.After(3 * time.Second):
				b.Fatal("Run did not return after shutdown")
			}

			if _, err := os.Stat(filepath.Join(dir, ".local", "share", "agent-mux", "transcripts.json")); err == nil {
				b.Fatal("daemon startup loaded or created transcript metadata unexpectedly")
			}
		}()
	}
}
