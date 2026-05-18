package kitty

import (
	"testing"
	"time"
)

// TestRealRunner_KillsHangingSubprocess pins the contract that a hanging
// kitten subprocess (modeled here by `sleep 60`) is killed by the runTimeout
// rather than blocking the caller indefinitely. The hook indicator invokes
// kitten synchronously on every Claude/Copilot event, so a wedged kitten
// must surface as an error within seconds — not hang the hook script and
// stall the harness.
func TestRealRunner_KillsHangingSubprocess(t *testing.T) {
	prev := runTimeout
	runTimeout = 150 * time.Millisecond
	t.Cleanup(func() { runTimeout = prev })

	r := NewRealRunner()
	start := time.Now()
	_, _, err := r.Run("sleep", "60")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error when subprocess exceeds runTimeout")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("RealRunner blocked beyond timeout: elapsed=%v, timeout=%v", elapsed, runTimeout)
	}
}
