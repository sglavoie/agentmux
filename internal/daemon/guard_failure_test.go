package daemon

import (
	"testing"
	"time"
)

func TestGuard_ActiveBypassesFailureGuard(t *testing.T) {
	// Failure is transient: any subsequent active event clears the guard,
	// regardless of isPrompt. Covers both the prompt-triggered active and
	// the post-tool active that follows a failed PostToolUse.
	for _, isPrompt := range []bool{false, true} {
		g := NewStateGuard(time.Hour)
		g.TryUpdate(1, "failure", false) // priority 4

		ok, eff := g.TryUpdate(1, "active", isPrompt)
		if !ok || eff != "active" {
			t.Fatalf("isPrompt=%v: want (true, active) for post-failure active, got (%v, %q)", isPrompt, ok, eff)
		}
		if cur := g.Current(1); cur != "active" {
			t.Fatalf("isPrompt=%v: want guard=active after failure bypass, got %q", isPrompt, cur)
		}
	}
}

func TestGuard_StopFailureNotClearedBySubagent(t *testing.T) {
	// stop_failure is terminal and must NOT be cleared by subagent (unlike
	// failure which is transient). The error icon must persist until the user
	// explicitly acknowledges it, even if a spurious subagent event fires.
	g := NewStateGuard(time.Hour)
	g.TryUpdate(1, "stop_failure", false)

	ok, eff := g.TryUpdate(1, "subagent", false)
	if ok || eff != "" {
		t.Fatalf("want stop_failure to block subagent, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "stop_failure" {
		t.Fatalf("want guard=stop_failure, got %q", cur)
	}
}

func TestGuard_StopFailureTerminal_NotClearedByActive(t *testing.T) {
	// stop_failure is terminal: a follow-up active must NOT clear the guard.
	// The clearing branch in TryUpdate is string-exact on "failure", so
	// stop_failure must fall through to the priority check and be blocked
	// (Priority(active)=1 < Priority(stop_failure)=4).
	for _, isPrompt := range []bool{false, true} {
		g := NewStateGuard(time.Hour)
		if ok, eff := g.TryUpdate(1, "stop_failure", false); !ok || eff != "stop_failure" {
			t.Fatalf("isPrompt=%v: want initial (true, stop_failure), got (%v, %q)", isPrompt, ok, eff)
		}
		if ok, eff := g.TryUpdate(1, "active", isPrompt); ok || eff != "" {
			t.Fatalf("isPrompt=%v: want active to be blocked by stop_failure, got (%v, %q)", isPrompt, ok, eff)
		}
		if cur := g.Current(1); cur != "stop_failure" {
			t.Fatalf("isPrompt=%v: want guard=stop_failure, got %q", isPrompt, cur)
		}
	}
}

func TestGuard_StopFailureNotDowngradedByFailure(t *testing.T) {
	// stop_failure and failure share priority 4, but stop_failure is terminal.
	// A later recoverable tool failure must not replace the sticky error state
	// with a transient warning before explicit user acknowledgement.
	g := NewStateGuard(time.Hour)
	if ok, eff := g.TryUpdate(1, "stop_failure", false); !ok || eff != "stop_failure" {
		t.Fatalf("want initial (true, stop_failure), got (%v, %q)", ok, eff)
	}

	if ok, eff := g.TryUpdate(1, "failure", false); ok || eff != "" {
		t.Fatalf("want failure to be blocked by stop_failure, got (%v, %q)", ok, eff)
	}
	if cur := g.Current(1); cur != "stop_failure" {
		t.Fatalf("want guard=stop_failure, got %q", cur)
	}
}
