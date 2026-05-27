package daemon

import (
	"os"
	"testing"
)

// TestHandler_Health verifies that TypeHealth returns the daemon's PID and a
// non-negative uptime. The handler initializes startedAt in NewHandler, so even
// the very first health probe must report uptime >= 0 (never negative).
func TestHandler_Health(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeHealth})
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

// TestActionFromIconState exhaustively pins the icon -> action recovery map.
// The handler relies on this when the daemon restarts mid-flow: lastAction is
// empty, so we fall back to the icon currently displayed in the tab title.
// Drift between this map and palette icons would silently break post-restart
// transitions (e.g. idle would no longer clear a stale bolt).
func TestActionFromIconState(t *testing.T) {
	cases := []struct {
		iconState string
		want      string
	}{
		{"bolt", "active"},
		{"compact", "compact"},
		{"subagent", "subagent"},
		{"stop", "stop"},
		{"key", "permission"},
		{"warning", "failure"},
		{"error", "stop_failure"},
		{"question", "elicitation"},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range cases {
		if got := actionFromIconState(tc.iconState); got != tc.want {
			t.Errorf("actionFromIconState(%q) = %q, want %q", tc.iconState, got, tc.want)
		}
	}
}
