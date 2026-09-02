package process

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonSocketOlderThanBinary(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-client-socket-age-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "daemon.sock")
	if err := os.WriteFile(socketPath, []byte("socket"), 0o644); err != nil {
		t.Fatalf("WriteFile(socket): %v", err)
	}
	binPath := filepath.Join(dir, "amx")
	if err := os.WriteFile(binPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary): %v", err)
	}

	old := time.Now().Add(-2 * time.Hour)
	newer := old.Add(time.Hour)
	if err := os.Chtimes(socketPath, old, old); err != nil {
		t.Fatalf("Chtimes(socket): %v", err)
	}
	if err := os.Chtimes(binPath, newer, newer); err != nil {
		t.Fatalf("Chtimes(binary): %v", err)
	}

	if !SocketOlderThanBinary(socketPath, binPath) {
		t.Fatal("expected newer binary to trigger daemon restart check")
	}

	if err := os.Chtimes(binPath, old.Add(-time.Hour), old.Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes(binary older): %v", err)
	}
	if SocketOlderThanBinary(socketPath, binPath) {
		t.Fatal("expected older binary to skip daemon restart check")
	}
}
