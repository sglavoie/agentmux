package kitty

import (
	"context"
	"errors"
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

// TestRealRunner_TimeoutErrorCarriesDeadlineCause pins that a deadline kill is
// distinguishable from any other failure. exec reports it as a bare
// "signal: killed" ExitError, so without the joined context error a wedged
// kitten would be indistinguishable from one that simply failed, and list
// coordination could not classify it as a timeout.
func TestRealRunner_TimeoutErrorCarriesDeadlineCause(t *testing.T) {
	prev := runTimeout
	runTimeout = 150 * time.Millisecond
	t.Cleanup(func() { runTimeout = prev })

	_, _, err := NewRealRunner().Run("sleep", "60")
	if err == nil {
		t.Fatal("expected error when subprocess exceeds runTimeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error must unwrap to context.DeadlineExceeded, got %v (%T)", err, err)
	}
	if classifyListError(err) != ListErrorTimeout {
		t.Fatalf("classify: got %q, want %q", classifyListError(err), ListErrorTimeout)
	}
}

// TestRealRunner_OrdinaryFailureIsNotATimeout keeps the deadline cause from
// being attached to failures that had nothing to do with it.
func TestRealRunner_OrdinaryFailureIsNotATimeout(t *testing.T) {
	_, _, err := NewRealRunner().Run("false")
	if err == nil {
		t.Fatal("expected error from a command exiting nonzero")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a nonzero exit must not be reported as a deadline: %v", err)
	}
	if classifyListError(err) != ListErrorCommand {
		t.Fatalf("classify: got %q, want %q", classifyListError(err), ListErrorCommand)
	}
}
