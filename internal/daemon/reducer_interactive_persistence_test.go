package daemon

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/conversation"
)

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
