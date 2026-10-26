package state

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// harnessEvent builds a parent-harness ReducerEvent for the ownership tests.
func harnessEvent(session, action string, seq uint64, at int64, pipe bool) protocol.ReducerEvent {
	return protocol.ReducerEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:" + session},
		Action:    action,
		SessionID: session,
		Seq:       seq,
		At:        at,
		PipeMode:  pipe,
	}
}

// TestInteractiveTerminalRetiresOlderInteractiveActive is the core defect: the
// current interactive session's stop must win over an older interactive
// session's stale bolt (bolt outranks stop), rather than being masked by it.
func TestInteractiveTerminalRetiresOlderInteractiveActive(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "active", 1, 100, false))
	r.Apply(harnessEvent("new", "stop", 2, 200, false))

	got := r.Reduce(200)
	if got.Icon != "stop" {
		t.Fatalf("icon = %q, want stop (older interactive bolt must not mask the current stop)", got.Icon)
	}
	if got.Source != (protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:new"}) {
		t.Fatalf("source = %+v, want claude:new", got.Source)
	}
	if f := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}]; f == nil || !f.acked {
		t.Fatalf("older interactive active fact was not retired: %+v", f)
	}
}

// TestInteractiveTerminalRetiresOlderInteractiveActiveAtEqualTimestampAcrossSources
// pins the live cross-source tie from F2: the old session's large source-local
// sequence is not comparable to the current session's small one. Because the
// terminal is accepted later by the same reducer, its causal application order
// retires the old bolt even though both events land in one Unix second.
func TestInteractiveTerminalRetiresOlderInteractiveActiveAtEqualTimestampAcrossSources(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "active", 900, 100, false))
	r.Apply(harnessEvent("current", "stop", 1, 100, false))

	old := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}]
	current := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:current"}]
	if old == nil || current == nil || !old.acked {
		t.Fatalf("equal-time retirement failed: old=%+v current=%+v", old, current)
	}
	if old.causalOrder >= current.causalOrder {
		t.Fatalf("causal order old/current = %d/%d; want current accepted later", old.causalOrder, current.causalOrder)
	}
	if got := r.Reduce(100); got.Icon != "stop" || got.Source != current.source {
		t.Fatalf("Reduce = %+v, want current session stop", got)
	}
}

// TestInteractivePromptRetiresOlderInteractiveActiveAtEqualTimestampAcrossSources
// covers the other interactive supersession trigger at the same timestamp.
func TestInteractivePromptRetiresOlderInteractiveActiveAtEqualTimestampAcrossSources(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "active", 900, 100, false))
	prompt := harnessEvent("current", "active", 1, 100, false)
	prompt.Prompt = true
	r.Apply(prompt)

	if old := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}]; old == nil || !old.acked {
		t.Fatalf("equal-time prompt did not retire old high-sequence bolt: %+v", old)
	}
	if got := r.Reduce(100); got.Icon != "bolt" || got.Source.Key != "claude:current" {
		t.Fatalf("Reduce = %+v, want current prompt bolt", got)
	}
}

// TestInteractiveTerminalDoesNotRetireNewerInteractiveActiveAtEqualTimestampAcrossSources
// proves the inverse ordering: activity accepted after an equal-time terminal
// is causally newer and remains live, regardless of the terminal source's much
// higher sequence.
func TestInteractiveTerminalDoesNotRetireNewerInteractiveActiveAtEqualTimestampAcrossSources(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "stop", 900, 100, false))
	r.Apply(harnessEvent("current", "active", 1, 100, false))

	current := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:current"}]
	if current == nil || current.acked {
		t.Fatalf("newer equal-time activity was retired: %+v", current)
	}
	if got := r.Reduce(100); got.Icon != "bolt" || got.Source != current.source {
		t.Fatalf("Reduce = %+v, want newer current activity", got)
	}
}

// TestCrossHarnessTerminalRetiresOlderInteractiveActive covers a pane reused by
// a different harness: a codex stop retires the stranded claude bolt.
func TestCrossHarnessTerminalRetiresOlderInteractiveActive(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "active", 1, 100, false))
	r.Apply(protocol.ReducerEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:new"},
		Action:    "stop",
		SessionID: "new",
		Seq:       2,
		At:        200,
	})

	if got := r.Reduce(200); got.Icon != "stop" || got.Source.Key != "codex:new" {
		t.Fatalf("reduce = %+v, want the codex stop", got)
	}
}

// TestASLFinishRetiresOlderInteractiveActive is the missing-terminal-hook
// regression: an ASL finish is authoritative foreground completion even when
// the provider never emitted its native Stop/agentStop. The run's stop must
// retire the provider's older bolt, which otherwise wins by icon precedence.
func TestASLFinishRetiresOlderInteractiveActive(t *testing.T) {
	for _, harnessName := range []string{"codex", "claude", "copilot"} {
		t.Run(harnessName, func(t *testing.T) {
			r := NewReducer(zeroCfg)
			source := protocol.SourceID{Kind: protocol.SourceHarness, Key: harnessName + ":session-1"}
			r.Apply(protocol.ReducerEvent{
				Source: source, Action: "active", SessionID: "session-1",
				Seq: 1, At: 100,
			})
			r.Apply(protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"},
				RunID:  "run-1", Phase: ASLPhaseFinished, Seq: 2, At: 200,
			})

			if got := r.Reduce(200); got.Icon != "stop" || got.Source.Kind != protocol.SourceASL {
				t.Fatalf("Reduce = %+v, want the finished run's stop", got)
			}
			if active := r.facts[source]; active == nil || !active.acked {
				t.Fatalf("older %s bolt was not retired: %+v", harnessName, active)
			}
		})
	}
}

// TestASLFinishPreservesNewerAndProtectedWork keeps the finish boundary narrow:
// it may retire only older interactive bolt/compact facts. Genuine newer work,
// pipe-mode children, subagents, and attention states remain authoritative.
func TestASLFinishPreservesNewerAndProtectedWork(t *testing.T) {
	tests := []struct {
		name     string
		seed     protocol.ReducerEvent
		wantIcon string
	}{
		{
			name: "newer interactive activity",
			seed: protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:new"},
				Action: "active", Seq: 1, At: 300},
			wantIcon: "bolt",
		},
		{
			name: "pipe-mode child",
			seed: protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:pipe"},
				Action: "active", Seq: 1, At: 100, PipeMode: true},
			wantIcon: "bolt",
		},
		{
			name: "subagent",
			seed: protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"},
				Action: "subagent", Seq: 1, At: 100},
			wantIcon: "subagent",
		},
		{
			name: "attention",
			seed: protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:prompt"},
				Action: "permission", Seq: 1, At: 100},
			wantIcon: "key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReducer(zeroCfg)
			r.Apply(tc.seed)
			r.Apply(protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"},
				RunID:  "run-1", Phase: ASLPhaseFinished, Seq: 1, At: 200,
			})

			if got := r.Reduce(300); got.Icon != tc.wantIcon {
				t.Fatalf("Reduce = %+v, want preserved %q", got, tc.wantIcon)
			}
			if fact := r.facts[tc.seed.Source]; fact == nil || fact.acked {
				t.Fatalf("protected fact was retired: %+v", fact)
			}
		})
	}
}

// TestInteractiveTerminalPreservesProtectedFacts proves the supersession is
// narrow: pipe-mode work, subagents, ASL leases, and interactive attention /
// warning facts all survive the current session's stop.
func TestInteractiveTerminalPreservesProtectedFacts(t *testing.T) {
	r := NewReducer(zeroCfg)
	// A concurrent pipe-mode child doing real work.
	r.Apply(harnessEvent("pipe", "active", 1, 90, true))
	// A subagent still running.
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"}, Action: "subagent", Seq: 1, At: 95})
	// A live ASL lease.
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, Seq: 1, At: 96})
	// Another interactive session holding a permission prompt (attention).
	r.Apply(harnessEvent("perm", "permission", 1, 97, false))
	// The current interactive session reaches stop.
	r.Apply(harnessEvent("cur", "stop", 1, 200, false))

	for _, id := range []protocol.SourceID{
		{Kind: protocol.SourceHarness, Key: "claude:pipe"},
		{Kind: protocol.SourceSubagent, Key: "agent-1"},
		{Kind: protocol.SourceHarness, Key: "claude:perm"},
	} {
		if f := r.facts[id]; f == nil || f.acked {
			t.Fatalf("protected fact %+v was retired: %+v", id, f)
		}
	}
	if got := r.Reduce(200); !got.Looping {
		t.Fatalf("reduce = %+v, want the ASL lease preserved (looping)", got)
	}
}

// TestInteractiveTerminalDoesNotRetirePipeActive isolates pipe preservation:
// genuine `claude -p` work outranks a sibling interactive stop.
func TestInteractiveTerminalDoesNotRetirePipeActive(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("pipe", "active", 1, 100, true))
	r.Apply(harnessEvent("cur", "stop", 1, 200, false))

	if got := r.Reduce(200); got.Icon != "bolt" || got.Source.Key != "claude:pipe" {
		t.Fatalf("reduce = %+v, want the pipe bolt to still win", got)
	}
}

// TestInteractiveTerminalRetiresProbeBusyActive is the stuck ⚡ seen live: a
// clear emptied the tab mid-turn, the busy-title probe restored ⚡ under its own
// fact, and the turn's real Stop then lost to it (bolt > stop) until the probe
// fact went stale ten minutes later.
func TestInteractiveTerminalRetiresProbeBusyActive(t *testing.T) {
	r := NewReducer(zeroCfg)
	probe := protocol.SourceID{Kind: protocol.SourceProbe, Key: "claude_screen"}
	r.Apply(harnessEvent("cur", "active", 1, 100, false))
	r.Apply(harnessEvent("cur", "clear", 2, 110, false))
	r.Apply(protocol.ReducerEvent{Source: probe, Action: "active", Prompt: true, Seq: 1, At: 113})
	r.Apply(harnessEvent("cur", "stop", 3, 400, false))

	if got := r.Reduce(400); got.Icon != "stop" || got.Source.Key != "claude:cur" {
		t.Fatalf("reduce = %+v, want the turn's stop over the probe's busy bolt", got)
	}
	if f := r.facts[probe]; f == nil || !f.acked {
		t.Fatalf("probe busy fact was not retired: %+v", f)
	}
}

// TestInteractiveTerminalKeepsNewerProbeBusyActive holds recency for the probe
// stand-in too: a busy title promoted after an older stop is current work.
func TestInteractiveTerminalKeepsNewerProbeBusyActive(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("cur", "stop", 1, 100, false))
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "claude_screen"}, Action: "active", Prompt: true, Seq: 1, At: 200})

	if got := r.Reduce(200); got.Icon != "bolt" {
		t.Fatalf("reduce = %+v, want the newer probe bolt", got)
	}
}

// TestInteractiveTerminalLeavesUserActiveToItsOwnRetirement keeps the
// operator's forced ⚡ out of the stand-in rule: RetireUserActive owns it.
func TestInteractiveTerminalLeavesUserActiveToItsOwnRetirement(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(protocol.ReducerEvent{Source: protocol.UserActionSource(), Action: "user_active", Seq: 1, At: 100})
	r.Apply(harnessEvent("cur", "stop", 1, 200, false))

	if f := r.facts[protocol.UserActiveSource()]; f == nil || f.acked {
		t.Fatalf("user_active retired by an interactive stop: %+v", f)
	}
}

// TestPipeTerminalDoesNotRetireInteractiveActive proves only interactive owners
// supersede: a pipe-mode child's stop must never retire the interactive
// session's active work.
func TestPipeTerminalDoesNotRetireInteractiveActive(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("owner", "active", 1, 100, false))
	r.Apply(harnessEvent("pipe", "stop", 1, 200, true))

	if got := r.Reduce(200); got.Icon != "bolt" || got.Source.Key != "claude:owner" {
		t.Fatalf("reduce = %+v, want the interactive bolt preserved", got)
	}
	if f := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:owner"}]; f == nil || f.acked {
		t.Fatalf("interactive active fact retired by a pipe stop: %+v", f)
	}
}

// TestInteractiveTerminalDoesNotRetireNewerInteractiveActive proves recency is
// respected: an older session's stop must not retire a genuinely newer
// interactive session's active work.
func TestInteractiveTerminalDoesNotRetireNewerInteractiveActive(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("new", "active", 1, 200, false))
	r.Apply(harnessEvent("old", "stop", 1, 100, false))

	if f := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:new"}]; f == nil || f.acked {
		t.Fatalf("newer interactive active fact was retired by an older stop: %+v", f)
	}
	if got := r.Reduce(200); got.Icon != "bolt" || got.Source.Key != "claude:new" {
		t.Fatalf("reduce = %+v, want the newer bolt", got)
	}
}

// TestInteractivePromptRetiresOlderInteractiveActive covers the prompt trigger:
// starting a new turn also retires an older interactive session's stale bolt.
func TestInteractivePromptRetiresOlderInteractiveActive(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "active", 1, 100, false))
	ev := harnessEvent("new", "active", 1, 200, false)
	ev.Prompt = true
	r.Apply(ev)

	if f := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:old"}]; f == nil || !f.acked {
		t.Fatalf("older interactive active fact not retired on prompt: %+v", f)
	}
}

// TestCurrentConversationPrefersInteractiveOverNewerPipeChild proves the
// current-conversation lookup selects the interactive owner even when a
// pipe-mode child fact is newer.
func TestCurrentConversationPrefersInteractiveOverNewerPipeChild(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("parent", "active", 1, 100, false))
	r.Apply(harnessEvent("child", "active", 2, 200, true)) // newer, but pipe-mode

	got, ok := r.CurrentConversation(200)
	if !ok || got != (conversation.Identity{Harness: "claude", SessionID: "parent"}) {
		t.Fatalf("CurrentConversation() = %+v, %v; want the interactive parent", got, ok)
	}
}

// TestCurrentConversationEqualTimestampUsesCrossSourceCausalOrder proves that
// ownership follows accepted application order, not unrelated source-local
// sequences, when interactive sessions share a second-resolution timestamp.
func TestCurrentConversationEqualTimestampUsesCrossSourceCausalOrder(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "active", 900, 100, false))
	r.Apply(harnessEvent("current", "active", 1, 100, false))

	got, ok := r.CurrentConversation(100)
	if !ok || got != (conversation.Identity{Harness: "claude", SessionID: "current"}) {
		t.Fatalf("CurrentConversation = %+v, %v; want current equal-time owner", got, ok)
	}
}

// TestReducerEqualTimestampUsesCrossSourceCausalOrder pins the same ordering in
// equal-precedence Reduce selection. Question and permission share one tier, so
// only the source-independent causal generation may select the later fact.
