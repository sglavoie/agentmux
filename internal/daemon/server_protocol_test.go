package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestServer_HandlesPartialWrites(t *testing.T) {
	w := &slowWriter{chunk: 1}
	resp := protocol.Response{OK: true, IconState: "bolt", Allowed: true}

	writeResponse(w, resp)

	out := w.buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("expected trailing newline, got: %q", out)
	}
	trimmed := strings.TrimRight(out, "\n")
	var decoded protocol.Response
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		t.Fatalf("response not valid JSON after partial writes: %v (raw: %q)", err, out)
	}
	if decoded.OK != resp.OK || decoded.Allowed != resp.Allowed || decoded.IconState != resp.IconState {
		t.Fatalf("response round-trip mismatch: got %+v want %+v", decoded, resp)
	}
}

// TestNewServer_SocketIsOwnerOnly pins the Unix socket file mode to 0600 so
// that other local users cannot connect to the daemon and inject state
// updates for arbitrary Kitty windows. net.Listen("unix") applies the
// process umask, which typically yields a world-accessible 0755 socket; the
// daemon must override that explicitly.
func TestNewServer_SocketIsOwnerOnly(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-srvperm-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")

	server, err := NewServer(socketPath, newTestHandler())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(server.Close)

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket perm = %o, want 0600", perm)
	}
}
