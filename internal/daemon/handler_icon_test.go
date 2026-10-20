package daemon

import (
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestHandler_Health verifies that TypeHealth returns the daemon's PID and a
// non-negative uptime. The handler initializes startedAt in NewHandler, so even
// the very first health probe must report uptime >= 0 (never negative).
func TestHandler_Health(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	resp := h.Handle(protocol.Request{Type: protocol.TypeHealth})
	if !resp.OK {
		t.Fatalf("want ok=true, got %+v", resp)
	}
	if resp.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", resp.PID, os.Getpid())
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("UptimeSeconds = %d, want >= 0", resp.UptimeSeconds)
	}
}
