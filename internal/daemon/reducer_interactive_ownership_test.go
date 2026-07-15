package daemon

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/conversation"
)

// harnessEvent builds a parent-harness ReducerEvent for the ownership tests.
func harnessEvent(session, action string, seq uint64, at int64, pipe bool) ReducerEvent {
	return ReducerEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "claude:" + session},
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
	if got.Source != (SourceID{Kind: SourceHarness, Key: "claude:new"}) {
		t.Fatalf("source = %+v, want claude:new", got.Source)
	}
	if f := r.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]; f == nil || !f.acked {
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

	old := r.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]
	current := r.facts[SourceID{Kind: SourceHarness, Key: "claude:current"}]
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

	if old := r.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]; old == nil || !old.acked {
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

	current := r.facts[SourceID{Kind: SourceHarness, Key: "claude:current"}]
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
	r.Apply(ReducerEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "codex:new"},
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
			source := SourceID{Kind: SourceHarness, Key: harnessName + ":session-1"}
			r.Apply(ReducerEvent{
				Source: source, Action: "active", SessionID: "session-1",
				Seq: 1, At: 100,
			})
			r.Apply(ReducerEvent{
				Source: SourceID{Kind: SourceASL, Key: "run-1"},
				RunID:  "run-1", Phase: ASLPhaseFinished, Seq: 2, At: 200,
			})

			if got := r.Reduce(200); got.Icon != "stop" || got.Source.Kind != SourceASL {
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
		seed     ReducerEvent
		wantIcon string
	}{
		{
			name: "newer interactive activity",
			seed: ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:new"},
				Action: "active", Seq: 1, At: 300},
			wantIcon: "bolt",
		},
		{
			name: "pipe-mode child",
			seed: ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:pipe"},
				Action: "active", Seq: 1, At: 100, PipeMode: true},
			wantIcon: "bolt",
		},
		{
			name: "subagent",
			seed: ReducerEvent{Source: SourceID{Kind: SourceSubagent, Key: "agent-1"},
				Action: "subagent", Seq: 1, At: 100},
			wantIcon: "subagent",
		},
		{
			name: "attention",
			seed: ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:prompt"},
				Action: "permission", Seq: 1, At: 100},
			wantIcon: "key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReducer(zeroCfg)
			r.Apply(tc.seed)
			r.Apply(ReducerEvent{
				Source: SourceID{Kind: SourceASL, Key: "run-1"},
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
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceSubagent, Key: "agent-1"}, Action: "subagent", Seq: 1, At: 95})
	// A live ASL lease.
	r.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, Seq: 1, At: 96})
	// Another interactive session holding a permission prompt (attention).
	r.Apply(harnessEvent("perm", "permission", 1, 97, false))
	// The current interactive session reaches stop.
	r.Apply(harnessEvent("cur", "stop", 1, 200, false))

	for _, id := range []SourceID{
		{Kind: SourceHarness, Key: "claude:pipe"},
		{Kind: SourceSubagent, Key: "agent-1"},
		{Kind: SourceHarness, Key: "claude:perm"},
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
	if f := r.facts[SourceID{Kind: SourceHarness, Key: "claude:owner"}]; f == nil || f.acked {
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

	if f := r.facts[SourceID{Kind: SourceHarness, Key: "claude:new"}]; f == nil || f.acked {
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

	if f := r.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]; f == nil || !f.acked {
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
func TestReducerEqualTimestampUsesCrossSourceCausalOrder(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(ReducerEvent{
		Source: SourceID{Kind: SourceProbe, Key: "old-permission"},
		Action: "permission", Seq: 900, At: 100,
	})
	r.Apply(ReducerEvent{
		Source: SourceID{Kind: SourceProbe, Key: "current-question"},
		Action: "question", Seq: 1, At: 100,
	})

	if got := r.Reduce(100); got.Icon != "question" || got.Source.Key != "current-question" {
		t.Fatalf("Reduce = %+v, want later equal-time question", got)
	}
}

// TestCurrentConversationInteractiveOnlyRejectsPipeOnlyChild proves the
// interactive-only lookup never falls back to a sole pipe-mode child. A window
// whose only live harness fact is a `copilot -p` child resolves to a current
// conversation (the pipe fallback) but to NO interactive conversation, so the
// Copilot transcript probe bails before ever resolving or reading the pipe
// child's transcript. When an interactive owner coexists, both lookups agree on
// it.
func TestCurrentConversationInteractiveOnlyRejectsPipeOnlyChild(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("child", "active", 1, 100, true)) // pipe-mode only

	if _, ok := r.CurrentInteractiveConversation(200); ok {
		t.Fatalf("CurrentInteractiveConversation() resolved a pipe-only child; want no interactive owner")
	}
	// The general lookup still names the pipe child (descriptive ownership).
	if got, ok := r.CurrentConversation(200); !ok || got != (conversation.Identity{Harness: "claude", SessionID: "child"}) {
		t.Fatalf("CurrentConversation() = %+v, %v; want the pipe child as descriptive owner", got, ok)
	}

	// With an interactive owner present, both lookups select it over the pipe child.
	r.Apply(harnessEvent("parent", "active", 2, 150, false))
	if got, ok := r.CurrentInteractiveConversation(200); !ok || got != (conversation.Identity{Harness: "claude", SessionID: "parent"}) {
		t.Fatalf("CurrentInteractiveConversation() = %+v, %v; want the interactive parent", got, ok)
	}
}

// TestPipeModeSurvivesDurableRoundTrip pins the persistence contract: the pm
// flag round-trips through MarshalUserVar/UnmarshalUserVar and the restored
// reducer still prefers the interactive owner.
func TestPipeModeSurvivesDurableRoundTrip(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("parent", "active", 1, 100, false))
	r.Apply(harnessEvent("child", "active", 2, 200, true))

	encoded, err := (WindowState{Reducer: r}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar: %v", err)
	}
	if !strings.Contains(encoded, `"pm":true`) {
		t.Fatalf("durable payload omitted pipe-mode flag: %s", encoded)
	}

	var restored WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar: %v", err)
	}
	child := restored.Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:child"}]
	if child == nil || !child.pipeMode {
		t.Fatalf("restored child fact lost pipe-mode: %+v", child)
	}
	parent := restored.Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:parent"}]
	if parent == nil || parent.pipeMode {
		t.Fatalf("restored parent fact gained pipe-mode: %+v", parent)
	}
	if got, ok := restored.CurrentConversation(200); !ok || got.SessionID != "parent" {
		t.Fatalf("restored CurrentConversation = %+v, %v; want the interactive parent", got, ok)
	}
}

// TestWindowStateCrossSourceCausalOrderSurvivesDurableRoundTrip pins the new
// fact metadata through the real amx_state wire path. Restart must retain both
// ownership and equal-precedence selection even when the old source's Seq is
// numerically much larger.
func TestWindowStateCrossSourceCausalOrderSurvivesDurableRoundTrip(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "active", 900, 100, false))
	r.Apply(harnessEvent("current", "active", 1, 100, false))

	encoded, err := (WindowState{Reducer: r, Effective: r.Reduce(100)}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar: %v", err)
	}
	if !strings.Contains(encoded, `"o":1`) || !strings.Contains(encoded, `"o":2`) {
		t.Fatalf("durable payload omitted causal orders: %s", encoded)
	}

	var restored WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar: %v", err)
	}
	old := restored.Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]
	current := restored.Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:current"}]
	if old == nil || current == nil || old.causalOrder != 1 || current.causalOrder != 2 {
		t.Fatalf("restored causal order old/current = %+v/%+v", old, current)
	}
	if got, ok := restored.CurrentConversation(100); !ok || got.SessionID != "current" {
		t.Fatalf("restored CurrentConversation = %+v, %v; want current", got, ok)
	}
	if got := restored.Reducer.Reduce(100); got.Source.Key != "claude:current" {
		t.Fatalf("restored Reduce = %+v, want current source", got)
	}
}

// TestReducerCrossSourceCausalOrderSurvivesDegradedMerge proves that a hook
// accepted into a fresh reducer remains causally newer than disjoint persisted
// facts imported afterward. The merge rebases the in-process order above the
// durable range instead of letting a persisted high Seq or order win the tie.
func TestReducerCrossSourceCausalOrderSurvivesDegradedMerge(t *testing.T) {
	persisted := NewReducer(zeroCfg)
	persisted.Apply(harnessEvent("old", "active", 900, 100, false))

	live := NewReducer(zeroCfg)
	live.Apply(harnessEvent("current", "active", 1, 100, false))
	if got := live.MergeDisjoint(persisted.Facts()); got != 1 {
		t.Fatalf("MergeDisjoint merged %d facts, want 1", got)
	}

	old := live.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]
	current := live.facts[SourceID{Kind: SourceHarness, Key: "claude:current"}]
	if old == nil || current == nil || current.causalOrder <= old.causalOrder {
		t.Fatalf("merged causal order old/current = %+v/%+v; live fact must remain newer", old, current)
	}
	if got, ok := live.CurrentConversation(100); !ok || got.SessionID != "current" {
		t.Fatalf("merged CurrentConversation = %+v, %v; want current", got, ok)
	}
	if got := live.Reduce(100); got.Icon != "bolt" || got.Source.Key != "claude:current" {
		t.Fatalf("merged Reduce = %+v, want current live bolt", got)
	}

	encoded, err := (WindowState{Reducer: live, Effective: live.Reduce(100)}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar after merge: %v", err)
	}
	var restored WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar after merge: %v", err)
	}
	if got, ok := restored.CurrentConversation(100); !ok || got.SessionID != "current" {
		t.Fatalf("restored merged CurrentConversation = %+v, %v; want current", got, ok)
	}
	if got := restored.Reducer.Reduce(100); got.Source.Key != "claude:current" {
		t.Fatalf("restored merged Reduce = %+v, want current source", got)
	}
}

// TestOldPayloadRestoresAsInteractive proves a state-v2 fact written before the
// pm field decodes as interactive (false), and behaves as an interactive owner:
// a current interactive stop can retire it.
func TestOldPayloadRestoresAsInteractive(t *testing.T) {
	// A schema-v2 payload with a harness bolt fact and no pm key.
	payload := `{"v":2,"f":[{"k":"harness","y":"claude:old","a":"active","i":"bolt","q":1,"t":100,"sid":"old"}]}`
	var st WindowState
	if err := st.UnmarshalUserVar(payload); err != nil {
		t.Fatalf("UnmarshalUserVar: %v", err)
	}
	old := st.Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]
	if old == nil || old.pipeMode {
		t.Fatalf("absent pm did not restore as interactive: %+v", old)
	}

	// A current interactive session's stop retires the restored interactive bolt.
	st.Reducer.SetConfig(zeroCfg)
	st.Reducer.Apply(harnessEvent("new", "stop", 5, 200, false))
	if got := st.Reducer.Reduce(200); got.Icon != "stop" {
		t.Fatalf("reduce = %+v, want stop after retiring the legacy interactive bolt", got)
	}
}

// TestInteractiveOwnershipSurvivesRestart proves the supersession still fires
// after a daemon restart: a restored older interactive bolt and a restored pipe
// bolt are treated correctly by the next interactive stop.
func TestInteractiveOwnershipSurvivesRestart(t *testing.T) {
	r := NewReducer(zeroCfg)
	r.Apply(harnessEvent("old", "active", 1, 100, false))
	r.Apply(harnessEvent("pipe", "active", 2, 110, true))

	encoded, err := (WindowState{Reducer: r}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar: %v", err)
	}
	var restored WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar: %v", err)
	}
	restored.Reducer.SetConfig(zeroCfg)
	restored.Reducer.Apply(harnessEvent("cur", "stop", 1, 200, false))

	if f := restored.Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]; f == nil || !f.acked {
		t.Fatalf("restored older interactive bolt not retired after restart: %+v", f)
	}
	if f := restored.Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:pipe"}]; f == nil || f.acked {
		t.Fatalf("restored pipe bolt wrongly retired after restart: %+v", f)
	}
	if got := restored.Reducer.Reduce(200); got.Icon != "bolt" || got.Source.Key != "claude:pipe" {
		t.Fatalf("reduce = %+v, want the surviving pipe bolt", got)
	}
}

// snapshot is a terse SourceFactSnapshot builder for the merge-cutoff test.
func snapshot(kind SourceKind, key, icon string, seq uint64, at int64) SourceFactSnapshot {
	return SourceFactSnapshot{Kind: kind, Key: key, Icon: icon, Seq: seq, At: at}
}

// TestMergeDisjointHonorsSupersessionCutoffs is the reducer-level core of
// F2-degraded-startup-merge-bypasses-window-supersession: a window-level
// transition accepted before MergeDisjoint runs must be replayed against the
// facts the merge imports, scoped by recency and by the same exclusions the live
// supersession paths use.
func TestMergeDisjointHonorsSupersessionCutoffs(t *testing.T) {
	acked := func(t *testing.T, r *Reducer, id SourceID, want bool) {
		t.Helper()
		f := r.facts[id]
		if f == nil {
			t.Fatalf("merged fact %+v missing", id)
		}
		if f.acked != want {
			t.Fatalf("merged fact %+v acked=%v, want %v", id, f.acked, want)
		}
	}

	t.Run("interactive terminal retires only older interactive active", func(t *testing.T) {
		r := NewReducer(zeroCfg)
		// In-process interactive terminal at t=200 records the interactive cutoff.
		r.Apply(harnessEvent("cur", "stop", 1, 200, false))
		imports := []SourceFactSnapshot{
			snapshot(SourceHarness, "claude:old", "bolt", 1, 100),                                      // retired
			{Kind: SourceHarness, Key: "claude:pipe", Icon: "bolt", Seq: 1, At: 100, PipeMode: true},   // preserved
			snapshot(SourceSubagent, "a1", "subagent", 1, 100),                                         // preserved
			snapshot(SourceHarness, "claude:att", "key", 1, 100),                                       // preserved (attention)
			{Kind: SourceHarness, Key: "claude:err", Icon: "error", Seq: 1, At: 100, Terminated: true}, // preserved (no prompt)
			snapshot(SourceHarness, "claude:new", "bolt", 1, 300),                                      // newer than cutoff: survives
		}
		if got := r.MergeDisjoint(imports); got != len(imports) {
			t.Fatalf("merged %d facts, want %d", got, len(imports))
		}
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:old"}, true)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:pipe"}, false)
		acked(t, r, SourceID{Kind: SourceSubagent, Key: "a1"}, false)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:att"}, false)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:err"}, false)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:new"}, false)
	})

	t.Run("ASL finish retires an older interactive active imported later", func(t *testing.T) {
		r := NewReducer(zeroCfg)
		// The run finish lands while startup restore is still unavailable. Its
		// cutoff must cover the stale provider bolt when MergeDisjoint imports it.
		r.Apply(ReducerEvent{
			Source: SourceID{Kind: SourceASL, Key: "run-1"}, RunID: "run-1",
			Phase: ASLPhaseFinished, Seq: 2, At: 200,
		})
		imports := []SourceFactSnapshot{
			snapshot(SourceHarness, "codex:old", "bolt", 1, 100),
			{Kind: SourceHarness, Key: "codex:pipe", Icon: "bolt", Seq: 1, At: 100, PipeMode: true},
			snapshot(SourceHarness, "codex:new", "bolt", 1, 300),
		}
		if got := r.MergeDisjoint(imports); got != len(imports) {
			t.Fatalf("merged %d facts, want %d", got, len(imports))
		}
		acked(t, r, SourceID{Kind: SourceHarness, Key: "codex:old"}, true)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "codex:pipe"}, false)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "codex:new"}, false)
	})

	t.Run("prompt acknowledges older terminals and retires older interactive active", func(t *testing.T) {
		r := NewReducer(zeroCfg)
		// In-process prompt at t=200 records both the prompt and interactive cutoffs.
		r.Apply(ReducerEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:cur"}, Action: "active", Prompt: true, SessionID: "cur", Seq: 1, At: 200})
		imports := []SourceFactSnapshot{
			{Kind: SourceHarness, Key: "claude:s0", Icon: "stop", Seq: 1, At: 100, Terminated: true},   // acked (prompt recovers terminals)
			{Kind: SourceHarness, Key: "claude:err", Icon: "error", Seq: 1, At: 100, Terminated: true}, // acked (prompt recovers ❗ too)
			snapshot(SourceHarness, "claude:old", "bolt", 1, 100),                                      // retired
			snapshot(SourceSubagent, "a1", "subagent", 1, 100),                                         // preserved
			{Kind: SourceHarness, Key: "claude:s3", Icon: "stop", Seq: 1, At: 300, Terminated: true},   // newer terminal: survives
		}
		r.MergeDisjoint(imports)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:s0"}, true)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:err"}, true)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:old"}, true)
		acked(t, r, SourceID{Kind: SourceSubagent, Key: "a1"}, false)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:s3"}, false)
	})

	t.Run("user_clear acknowledges every older fact but preserves a live lease", func(t *testing.T) {
		r := NewReducer(zeroCfg)
		// In-process user_clear at t=200 records the clear cutoff (creates no fact).
		r.Apply(ReducerEvent{Source: userActionSource(), Action: "user_clear", Seq: 9, At: 200})
		imports := []SourceFactSnapshot{
			snapshot(SourceHarness, "claude:old", "bolt", 1, 100),                                      // acked
			snapshot(SourceSubagent, "a1", "subagent", 1, 100),                                         // acked
			{Kind: SourceHarness, Key: "claude:err", Icon: "error", Seq: 1, At: 100, Terminated: true}, // acked
			{Kind: SourceASL, Key: "run-1", Seq: 1, At: 100, LeaseLive: true},                          // acked but LeaseLive kept
			snapshot(SourceHarness, "claude:new", "bolt", 1, 300),                                      // newer: survives the clear
		}
		r.MergeDisjoint(imports)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:old"}, true)
		acked(t, r, SourceID{Kind: SourceSubagent, Key: "a1"}, true)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:err"}, true)
		acked(t, r, SourceID{Kind: SourceASL, Key: "run-1"}, true)
		acked(t, r, SourceID{Kind: SourceHarness, Key: "claude:new"}, false)
		// The acknowledged live lease still loops: a clear preserves a live loop.
		if _, live := r.LeaseState("run-1"); !live {
			t.Fatal("user_clear cutoff cleared the live ASL lease's LeaseLive")
		}
		if got := r.Reduce(300); !got.Looping {
			t.Fatalf("reduce = %+v; want the live lease preserved through the clear merge", got)
		}
	})
}
