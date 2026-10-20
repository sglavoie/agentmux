package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// TestMaintenanceTick_MergesPersistedFactsWithHookState pins
// F1-degraded-startup-restore-fact-loss. During a degraded startup a real hook
// creates a window's in-process state (for its own source only) before the
// first successful restore runs. That restore must MERGE the window's disjoint
// still-live persisted facts — here a separate interactive harness fact and a
// live between-turns ASL lease — into the hook state instead of skipping the
// whole window, while the accepted same-source hook fact wins its own slot and
// a late-completing startup restore can neither duplicate nor roll any of it
// back.
func TestMaintenanceTick_MergesPersistedFactsWithHookState(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	now := time.Now()
	process := protocol.HarnessProcessIdentity{Harness: "claude", PID: 4321, StartToken: "Mon Jul 13 10:11:12 2026"}

	// Persisted multi-source durable state for window 42: an interactive harness
	// "active" fact, a live between-turns ASL lease, and an OLDER legacy
	// window-keyed fact that collides with the source the degraded-window hook
	// will create.
	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 1, At: now.Unix(), HarnessProcess: process})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: state.ASLPhaseBetween, RunID: "run-1", Seq: 1, At: now.Unix()})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.WindowKey(42)}, Action: "active", Seq: 1, At: now.Add(-time.Minute).Unix()})
	encoded, err := state.WindowState{
		LastAction: "active",
		Reducer:    r,
		Effective:  r.Reduce(now.Unix()),
		ShadowSeq:  1,
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	windows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:    100,
			Title: "api",
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
			}},
		}},
	}}

	client := &gatedStartupListKittyClient{
		windows:         windows,
		firstEntered:    make(chan struct{}, 1),
		releaseFirst:    make(chan struct{}),
		restoreRendered: make(chan struct{}),
	}
	h.useKittyClient(client)
	t.Cleanup(func() {
		select {
		case <-client.releaseFirst:
		default:
			close(client.releaseFirst)
		}
	})

	// The bounded startup restore blocks on the first list and comes up degraded.
	ready, reason := h.restoreStateFromKittyBounded(now, 30*time.Millisecond)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded with a reason", ready, reason)
	}
	h.MarkDegraded(reason)
	select {
	case <-client.firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded restore never parked on the blocked first list")
	}

	// A real hook lands FIRST, before any restore, creating the window's
	// in-process state for the legacy window source only. The disjoint harness
	// fact and ASL lease live solely in the persisted amx_state.
	if resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "permission"}); !resp.OK {
		t.Fatalf("degraded-window hook = %+v; want OK", resp)
	}
	h.stateMu.Lock()
	hookSt, hookOK := h.store[42]
	h.stateMu.Unlock()
	if !hookOK || hookSt.Reducer == nil {
		t.Fatal("degraded-window hook did not create in-process reducer state")
	}

	// Maintenance recovery restores the persisted amx_state BEFORE marking ready.
	// The window already holds hook state, so the restore MERGES the disjoint
	// persisted facts into it rather than skipping the window.
	h.maintenanceTick(now)

	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged, mergedOK := h.store[42]
	h.stateMu.Unlock()
	if !mergedOK || merged != hookSt {
		t.Fatal("maintenance merge replaced the in-process state instead of merging into it")
	}

	legacyFact := func(facts []protocol.SourceFactSnapshot) *protocol.SourceFactSnapshot {
		for i := range facts {
			if facts[i].Kind == protocol.SourceLegacy && facts[i].Key == protocol.WindowKey(42) {
				return &facts[i]
			}
		}
		return nil
	}
	harnessFact := func(facts []protocol.SourceFactSnapshot) *protocol.SourceFactSnapshot {
		for i := range facts {
			if facts[i].Kind == protocol.SourceHarness && facts[i].Key == "claude:s1" {
				return &facts[i]
			}
		}
		return nil
	}

	// The new hook fact wins the same-source conflict: the legacy window source
	// still holds the accepted "permission", NOT the older persisted "active".
	mergedFacts := merged.Reducer.Facts()
	if lf := legacyFact(mergedFacts); lf == nil || lf.Action != "permission" || lf.Icon != "key" {
		t.Fatalf("same-source hook fact lost the conflict: %+v", lf)
	}
	// The disjoint persisted facts survived the merge.
	if hf := harnessFact(mergedFacts); hf == nil || hf.HarnessProcessHarness != process.Harness ||
		hf.HarnessProcessPID != process.PID || hf.HarnessProcessStartToken != process.StartToken {
		t.Fatalf("disjoint persisted harness process fact was not merged intact: %+v", hf)
	}
	if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("durable ASL lease not merged: exists=%v live=%v", exists, live)
	}
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "key" || !eff.Looping {
		t.Fatalf("merged Reduce = %+v, want key with a live lease", eff)
	}

	// Release the blocked first list. Its late startup restore re-enters the
	// merge path, finds every source already present, and must neither duplicate
	// facts nor roll the hook state back — proving idempotency.
	beforeCount := len(mergedFacts)
	close(client.releaseFirst)
	select {
	case <-client.restoreRendered:
	case <-time.After(2 * time.Second):
		t.Fatal("late startup restore never completed its render")
	}

	h.stateMu.Lock()
	after, afterOK := h.store[42]
	h.stateMu.Unlock()
	if !afterOK || after != merged {
		t.Fatal("late startup restore replaced the merged in-process state")
	}
	afterFacts := after.Reducer.Facts()
	if len(afterFacts) != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, len(afterFacts))
	}
	if lf := legacyFact(afterFacts); lf == nil || lf.Action != "permission" {
		t.Fatalf("late startup restore rolled back the hook fact: %+v", lf)
	}
	if hf := harnessFact(afterFacts); hf == nil || hf.HarnessProcessPID != process.PID || hf.HarnessProcessStartToken != process.StartToken {
		t.Fatalf("late startup restore changed durable process identity: %+v", hf)
	}
	if exists, live := after.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("late startup restore dropped the merged ASL lease: exists=%v live=%v", exists, live)
	}
}

// TestMaintenanceTick_MergesAcknowledgedPersistedFactsWithHookState pins
// F1-degraded-startup-merge-drops-acknowledged-facts. When a window is cleared
// with /clear, user_clear acknowledges every visible fact but deliberately
// leaves a live ASL loop lease's LeaseLive set (Reduce still derives 🔁 from it)
// and keeps acknowledged non-terminal facts as durable source-sequence cutoffs.
// If a real hook then creates the window's in-process state during a degraded
// startup (before the first successful restore), the recovery merge must import
// those ACKNOWLEDGED disjoint facts — not skip them — so the live loop marker
// survives and pre-clear duplicate/reordered events stay no-ops. The accepted
// same-source hook fact still wins its own slot, and a late-completing startup
// restore neither duplicates nor rolls any of it back.
func TestMaintenanceTick_MergesAcknowledgedPersistedFactsWithHookState(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	now := time.Now()

	// Persisted /clear state for window 42: an interactive harness "active" fact
	// and a live between-turns ASL lease, both acknowledged by a user_clear that
	// preserved the loop lease. The harness fact's Seq 5 is the persisted cutoff.
	r := state.NewReducer(state.ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 5, At: now.Unix()})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: state.ASLPhaseBetween, RunID: "run-1", Seq: 1, At: now.Unix()})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "user"}, Action: "user_clear", Seq: 0, At: now.Unix()})
	if eff := r.Reduce(now.Unix()); eff.Icon != "" || !eff.Looping {
		t.Fatalf("persisted /clear Reduce = %+v; want icon-less with a live lease", eff)
	}
	encoded, err := state.WindowState{
		LastAction: "clear",
		Reducer:    r,
		Effective:  r.Reduce(now.Unix()),
		ShadowSeq:  5,
	}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	windows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:    100,
			Title: "api",
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
			}},
		}},
	}}

	client := &gatedStartupListKittyClient{
		windows:         windows,
		firstEntered:    make(chan struct{}, 1),
		releaseFirst:    make(chan struct{}),
		restoreRendered: make(chan struct{}),
	}
	h.useKittyClient(client)
	t.Cleanup(func() {
		select {
		case <-client.releaseFirst:
		default:
			close(client.releaseFirst)
		}
	})

	// The bounded startup restore blocks on the first list and comes up degraded.
	ready, reason := h.restoreStateFromKittyBounded(now, 30*time.Millisecond)
	if ready || reason == "" {
		t.Fatalf("bounded restore = (%v, %q); want degraded with a reason", ready, reason)
	}
	h.MarkDegraded(reason)
	select {
	case <-client.firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded restore never parked on the blocked first list")
	}

	// A real hook lands FIRST, before any restore, creating the window's
	// in-process state for the legacy window source only. The acknowledged
	// harness fact and live ASL lease live solely in the persisted amx_state.
	if resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "permission"}); !resp.OK {
		t.Fatalf("degraded-window hook = %+v; want OK", resp)
	}
	h.stateMu.Lock()
	hookSt, hookOK := h.store[42]
	h.stateMu.Unlock()
	if !hookOK || hookSt.Reducer == nil {
		t.Fatal("degraded-window hook did not create in-process reducer state")
	}

	// Maintenance recovery merges the ACKNOWLEDGED persisted facts into the hook
	// state before marking ready.
	h.maintenanceTick(now)

	if r2, _ := h.Readiness(); !r2 {
		t.Fatal("daemon should be ready after the maintenance merge repopulated the store")
	}
	h.stateMu.Lock()
	merged, mergedOK := h.store[42]
	h.stateMu.Unlock()
	if !mergedOK || merged != hookSt {
		t.Fatal("maintenance merge replaced the in-process state instead of merging into it")
	}

	aslFact := func(facts []protocol.SourceFactSnapshot) *protocol.SourceFactSnapshot {
		for i := range facts {
			if facts[i].Kind == protocol.SourceASL && facts[i].Key == "run-1" {
				return &facts[i]
			}
		}
		return nil
	}
	harnessFact := func(facts []protocol.SourceFactSnapshot) *protocol.SourceFactSnapshot {
		for i := range facts {
			if facts[i].Kind == protocol.SourceHarness && facts[i].Key == "claude:s1" {
				return &facts[i]
			}
		}
		return nil
	}

	// The acknowledged live ASL lease was merged: it stays icon-less and
	// acknowledged, but keeps LeaseLive and drives the reduced 🔁.
	if af := aslFact(merged.Reducer.Facts()); af == nil || !af.Acked || !af.LeaseLive || af.Icon != "" {
		t.Fatalf("acknowledged ASL lease not merged intact: %+v", af)
	}
	if exists, live := merged.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("durable acknowledged ASL lease not merged: exists=%v live=%v", exists, live)
	}
	// The acknowledged non-terminal harness fact was merged and keeps its Seq 5
	// cutoff.
	if hf := harnessFact(merged.Reducer.Facts()); hf == nil || !hf.Acked || hf.Seq != 5 {
		t.Fatalf("acknowledged harness cutoff fact not merged intact: %+v", hf)
	}
	// The hook fact renders 🔑 while the acked ASL lease supplies 🔁.
	if eff := merged.Reducer.Reduce(now.Unix()); eff.Icon != "key" || !eff.Looping {
		t.Fatalf("merged Reduce = %+v, want key with a live lease", eff)
	}

	// The retained acknowledged cutoff keeps a pre-clear duplicate AND a reordered
	// event no-ops for the harness source.
	if dec := merged.Reducer.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 5, At: now.Unix()}); dec != protocol.DecisionDuplicate {
		t.Fatalf("pre-clear replay decision = %s, want duplicate (cutoff lost)", dec)
	}
	if dec := merged.Reducer.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 3, At: now.Unix()}); dec != protocol.DecisionReordered {
		t.Fatalf("pre-clear reorder decision = %s, want reordered (cutoff lost)", dec)
	}

	// Release the blocked first list. Its late startup restore re-enters the merge
	// path, finds every source already present, and must neither duplicate facts
	// nor roll the merged state back — proving idempotency across recovery paths.
	beforeCount := len(merged.Reducer.Facts())
	close(client.releaseFirst)
	select {
	case <-client.restoreRendered:
	case <-time.After(2 * time.Second):
		t.Fatal("late startup restore never completed its render")
	}

	h.stateMu.Lock()
	after, afterOK := h.store[42]
	h.stateMu.Unlock()
	if !afterOK || after != merged {
		t.Fatal("late startup restore replaced the merged in-process state")
	}
	if got := len(after.Reducer.Facts()); got != beforeCount {
		t.Fatalf("late startup restore changed fact count %d -> %d (non-idempotent merge)", beforeCount, got)
	}
	if af := aslFact(after.Reducer.Facts()); af == nil || !af.Acked || !af.LeaseLive {
		t.Fatalf("late startup restore dropped the acknowledged ASL lease: %+v", af)
	}
	if exists, live := after.Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("late startup restore dropped the merged ASL lease: exists=%v live=%v", exists, live)
	}
	if eff := after.Reducer.Reduce(now.Unix()); eff.Icon != "key" || !eff.Looping {
		t.Fatalf("post-late-restore Reduce = %+v, want key with a live lease", eff)
	}
}

// encodeMergeWindow builds the single-window OSWindow list a degraded-startup
// merge test serves from its gated Kitty client: window 42 (Kitty ID) carries
// r's durable facts encoded as amx_state.
func encodeMergeWindow(t *testing.T, r *state.Reducer, shadowSeq uint64, now time.Time) []kitty.OSWindow {
	t.Helper()
	encoded, err := state.WindowState{Reducer: r, Effective: r.Reduce(now.Unix()), ShadowSeq: shadowSeq}.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	return []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{{
			ID:    100,
			Title: "api",
			Windows: []kitty.Window{{
				ID:       42,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: encoded},
			}},
		}},
	}}
}

// mergedFactBySource returns the merged snapshot for a source (nil when absent),
// so a test can assert acknowledgement and presence after a merge.
func mergedFactBySource(facts []protocol.SourceFactSnapshot, id protocol.SourceID) *protocol.SourceFactSnapshot {
	for i := range facts {
		if facts[i].Source() == id {
			return &facts[i]
		}
	}
	return nil
}
