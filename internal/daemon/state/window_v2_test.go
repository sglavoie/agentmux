package state

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestUserVarV4RoundTripPersistsFactsProcessAndEffective(t *testing.T) {
	process := protocol.HarnessProcessIdentity{Harness: "claude", PID: 4321, StartToken: "Mon Jul 13 10:11:12 2026"}
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 3, TurnID: "t1", At: 1710000100, OriginWindowID: 91, HarnessProcess: process})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"}, Action: "stop_failure", Seq: 2, TurnID: "t1", At: 1710000200})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhaseTurn, RunID: "run-1", Seq: 5, At: 1710000300})

	now := int64(1710000400)
	busySince := time.Unix(1710000100, 0)
	state := WindowState{
		LastAction:   "active",
		Reducer:      r,
		Effective:    r.Reduce(now),
		EffectiveGen: 7,
		ShadowSeq:    9,
		BusySince:    busySince,
	}

	encoded, err := state.MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	if !strings.Contains(encoded, `"v":4`) {
		t.Fatalf("MarshalUserVar() missing schema discriminator: %s", encoded)
	}
	for _, fragment := range []string{`"pv":1`, `"ph":"claude"`, `"pp":4321`, `"pt":"Mon Jul 13 10:11:12 2026"`, `"bs":1710000100`} {
		if !strings.Contains(encoded, fragment) {
			t.Fatalf("MarshalUserVar() missing process field %s: %s", fragment, encoded)
		}
	}

	var got WindowState
	if err := got.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}
	if got.Reducer == nil {
		t.Fatalf("restored state has no reducer")
	}
	if !reflect.DeepEqual(got.Reducer.Facts(), r.Facts()) {
		t.Fatalf("restored facts = %+v, want %+v", got.Reducer.Facts(), r.Facts())
	}
	if got.Effective != state.Effective {
		t.Fatalf("Effective = %+v, want %+v", got.Effective, state.Effective)
	}
	if got.EffectiveGen != 7 || got.ShadowSeq != 9 {
		t.Fatalf("EffectiveGen/ShadowSeq = %d/%d, want 7/9", got.EffectiveGen, got.ShadowSeq)
	}
	if !got.BusySince.Equal(busySince) {
		t.Fatalf("BusySince = %v, want %v", got.BusySince, busySince)
	}
	if before, after := r.Reduce(now), got.Reducer.Reduce(now); after != before {
		t.Fatalf("restart equivalence broken: Reduce = %+v, want %+v", after, before)
	}
}

func TestUserVarV4RoundTripPreservesAcknowledgementCutoff(t *testing.T) {
	src := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	process := protocol.HarnessProcessIdentity{Harness: "claude", PID: 4321, StartToken: "Mon Jul 13 10:11:12 2026"}
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 1, At: 100, HarnessProcess: process})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceProbe, Key: "user"}, Action: "user_clear", Seq: 2, At: 101})

	encoded, err := (WindowState{Reducer: r}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	var got WindowState
	if err := got.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}

	facts := got.Reducer.Facts()
	if len(facts) != 1 || !facts[0].Acked || facts[0].AckedAt != 101 ||
		facts[0].HarnessProcessHarness != process.Harness || facts[0].HarnessProcessPID != process.PID ||
		facts[0].HarnessProcessStartToken != process.StartToken {
		t.Fatalf("restored facts = %+v, want one acknowledged fact", facts)
	}
	if eff := got.Reducer.Reduce(102); eff.Icon != "" {
		t.Fatalf("acked fact rendered %q after restore, want idle", eff.Icon)
	}
	// A newer event for the source re-arms it past the restored cutoff.
	got.Reducer.Apply(protocol.ReducerEvent{Source: src, Action: "active", Seq: 3, At: 103})
	if eff := got.Reducer.Reduce(104); eff.Icon != "bolt" {
		t.Fatalf("post-restore event rendered %q, want bolt", eff.Icon)
	}
}

func TestUserVarV4RestoresV2WithDeterministicAcknowledgementSeed(t *testing.T) {
	raw := `{"v":2,"f":[{"k":"harness","y":"claude:active","a":"active","i":"bolt","q":5,"t":123,"ak":true},{"k":"harness","y":"claude:terminal","a":"stop","i":"stop","q":6,"t":100,"ak":true,"tm":true,"ta":200},{"k":"probe","y":"zero","a":"active","i":"bolt","q":7,"ak":true}],"sq":7}`
	var st WindowState
	if err := st.UnmarshalUserVar(raw); err != nil {
		t.Fatalf("UnmarshalUserVar(v2) error: %v", err)
	}
	if st.Reducer == nil {
		t.Fatal("v2 restore produced no reducer")
	}
	facts := st.Reducer.Facts()
	if got := facts[0].AckedAt; got != 123 {
		t.Fatalf("active legacy AckedAt = %d; want At seed 123", got)
	}
	if got := facts[1].AckedAt; got != 200 {
		t.Fatalf("terminal legacy AckedAt = %d; want TerminalAt seed 200", got)
	}
	if got := facts[2].AckedAt; got != 1 {
		t.Fatalf("zero-time legacy AckedAt = %d; want deterministic floor 1", got)
	}
	for _, fact := range facts {
		if fact.OriginWindowID != 0 {
			t.Fatalf("v2 fact unexpectedly restored origin: %+v", fact)
		}
	}
}

func TestUserVarV4RestartPreservesRoutedOriginThroughTerminal(t *testing.T) {
	source := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{
		Source: source, Action: "active", SessionID: "s1", Seq: 1, At: 100,
		OriginWindowID: 91,
	})
	encoded, err := (WindowState{Reducer: r, ShadowSeq: 1}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	var restored WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}
	if !restored.Reducer.HasLiveOriginRoute(source, "s1", 91) {
		t.Fatalf("restored reducer lost routed origin: %+v", restored.Reducer.Facts())
	}
	restored.Reducer.Apply(protocol.ReducerEvent{
		Source: source, Action: "stop", SessionID: "s1", Seq: 2, At: 101,
		OriginWindowID: 91,
	})
	facts := restored.Reducer.Facts()
	if len(facts) != 1 || !facts[0].Terminated || facts[0].OriginWindowID != 91 {
		t.Fatalf("terminal continuation after restart = %+v", facts)
	}
}

func TestUserVarV4RestartEquivalenceAcrossSources(t *testing.T) {
	events := []protocol.ReducerEvent{
		{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 1, TurnID: "t1", At: 100},
		{Source: protocol.SourceID{Kind: protocol.SourceSubagent, Key: "agent-1"}, Action: "subagent", Seq: 1, TurnID: "t1", At: 110},
		{Source: protocol.SourceID{Kind: protocol.SourceASL, Key: "run-1"}, Phase: ASLPhasePaused, Action: "permission", RunID: "run-1", Seq: 4, At: 120},
		{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "stop", Seq: 2, TurnID: "t1", At: 130},
	}
	r := NewReducer(ReducerConfig{})
	for _, ev := range events {
		r.Apply(ev)
	}

	encoded, err := (WindowState{Reducer: r}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	var got WindowState
	if err := got.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}

	for _, now := range []int64{131, 1000, 100000} {
		before, after := r.Reduce(now), got.Reducer.Reduce(now)
		if before != after {
			t.Fatalf("Reduce(%d) after restart = %+v, want %+v", now, after, before)
		}
	}
	// The ASL pause holds attention after the loop process has ended.
	if eff := got.Reducer.Reduce(131); eff.Icon != "key" || eff.Looping {
		t.Fatalf("Reduce(131) = %+v, want key icon without live lease", eff)
	}
}

func TestUserVarV4MigratesV1IntoSyntheticLegacySource(t *testing.T) {
	// A v1 payload has no "v" discriminator; its scalar action and loop marker
	// migrate into synthetic legacy/ASL facts.
	raw := `{"a":"permission","l":true,"acct":"work","h":"codex","t":1710000001}`
	var st WindowState
	if err := st.UnmarshalUserVar(raw); err != nil {
		t.Fatalf("UnmarshalUserVar(v1) error: %v", err)
	}
	if st.LastAction != "permission" || st.DesiredIcon != "key" {
		t.Fatalf("legacy scalars = %q/%q, want permission/key", st.LastAction, st.DesiredIcon)
	}
	if st.Reducer == nil {
		t.Fatalf("v1 migration produced no reducer")
	}

	facts := st.Reducer.Facts()
	if len(facts) != 2 {
		t.Fatalf("migrated facts = %+v, want legacy action + ASL lease", facts)
	}
	asl, legacy := facts[0], facts[1] // sorted by kind: "asl" < "legacy"
	if asl.Kind != protocol.SourceASL || asl.Key != legacyV1MigrationKey || !asl.LeaseLive || asl.Phase != ASLPhaseBetween {
		t.Fatalf("ASL lease fact = %+v", asl)
	}
	if legacy.Kind != protocol.SourceLegacy || legacy.Key != legacyV1MigrationKey || legacy.Icon != "key" || legacy.Terminated {
		t.Fatalf("legacy fact = %+v", legacy)
	}
	if legacy.At != 1710000001 || legacy.Seq != 1 {
		t.Fatalf("legacy fact timing = at %d seq %d, want 1710000001/1", legacy.At, legacy.Seq)
	}

	if st.Effective.Icon != "key" || !st.Effective.Looping {
		t.Fatalf("Effective = %+v, want key + looping", st.Effective)
	}
	if st.ShadowSeq != 1 {
		t.Fatalf("ShadowSeq = %d, want 1", st.ShadowSeq)
	}
	if eff := st.Reducer.Reduce(1710000002); eff.Icon != "key" || !eff.Looping {
		t.Fatalf("Reduce after migration = %+v, want key + looping", eff)
	}
}

func TestUserVarV4MigratesV1TerminalAction(t *testing.T) {
	var st WindowState
	if err := st.UnmarshalUserVar(`{"a":"stop_failure","t":5}`); err != nil {
		t.Fatalf("UnmarshalUserVar(v1) error: %v", err)
	}
	facts := st.Reducer.Facts()
	if len(facts) != 1 {
		t.Fatalf("migrated facts = %+v, want one legacy fact", facts)
	}
	if !facts[0].Terminated || facts[0].TerminalAt != 5 || facts[0].Icon != "error" {
		t.Fatalf("terminal fact = %+v, want terminated error at 5", facts[0])
	}
}

func TestUserVarV4MigratesEmptyV1WithoutFacts(t *testing.T) {
	var st WindowState
	if err := st.UnmarshalUserVar(`{"acct":"work"}`); err != nil {
		t.Fatalf("UnmarshalUserVar(v1) error: %v", err)
	}
	if st.Reducer != nil {
		t.Fatalf("empty v1 payload migrated facts: %+v", st.Reducer.Facts())
	}
	if st.Effective != (EffectiveState{}) || st.ShadowSeq != 0 {
		t.Fatalf("empty v1 payload set effective state: %+v seq %d", st.Effective, st.ShadowSeq)
	}
}

func TestUserVarV4ShadowSeqNeverTrailsRestoredFacts(t *testing.T) {
	// A payload whose ShadowSeq lags its fact sequences (e.g. written by a
	// crashed daemon mid-update) must not hand out receipt sequences that the
	// reducer would discard as reordered.
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:s1"}, Action: "active", Seq: 40, At: 100})
	encoded, err := (WindowState{Reducer: r, ShadowSeq: 2}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	var got WindowState
	if err := got.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}
	if got.ShadowSeq != 40 {
		t.Fatalf("ShadowSeq = %d, want lifted to max fact seq 40", got.ShadowSeq)
	}
}

func TestUserVarV4DropsInvalidProcessMetadataWithoutLosingFacts(t *testing.T) {
	raw := `{"v":4,"pv":1,"f":[` +
		`{"k":"harness","y":"claude:valid","a":"active","i":"bolt","ph":"claude","pp":4321,"pt":"Mon Jul 13 10:11:12 2026"},` +
		`{"k":"harness","y":"claude:partial","a":"active","i":"bolt","ph":"claude","pp":4322},` +
		`{"k":"harness","y":"claude:mismatch","a":"active","i":"bolt","ph":"codex","pp":4323,"pt":"token"},` +
		`{"k":"asl","y":"run-1","p":"between","ll":true,"ph":"claude","pp":4324,"pt":"token"}]}`

	var got WindowState
	if err := got.UnmarshalUserVar(raw); err != nil {
		t.Fatalf("UnmarshalUserVar(v4) error: %v", err)
	}
	if got.Reducer == nil {
		t.Fatal("v4 restore produced no reducer")
	}
	facts := got.Reducer.Facts()
	if len(facts) != 4 {
		t.Fatalf("restored facts = %+v, want all four underlying facts", facts)
	}
	for _, fact := range facts {
		switch fact.Key {
		case "claude:valid":
			if fact.HarnessProcessHarness != "claude" || fact.HarnessProcessPID != 4321 || fact.HarnessProcessStartToken != "Mon Jul 13 10:11:12 2026" {
				t.Fatalf("valid process identity = %+v", fact)
			}
		default:
			if fact.HarnessProcessHarness != "" || fact.HarnessProcessPID != 0 || fact.HarnessProcessStartToken != "" {
				t.Fatalf("invalid process identity survived on %s: %+v", fact.Key, fact)
			}
		}
	}
}

func TestUserVarV4DropsPreUTCTokenOwnershipWithoutLosingFacts(t *testing.T) {
	// Early v4 writers persisted locale-stable but timezone-dependent lstart
	// values and had no payload-level token marker. Comparing one of those
	// opaque values with a newly UTC-rendered token could falsely report PID
	// reuse, so restore the contribution but deliberately leave it untracked.
	raw := `{"v":4,"f":[{"k":"harness","y":"claude:s1","a":"active","i":"bolt","ph":"claude","pp":4321,"pt":"Mon Jul 13 10:11:12 2026"}]}`
	var got WindowState
	if err := got.UnmarshalUserVar(raw); err != nil {
		t.Fatalf("UnmarshalUserVar(pre-marker v4) error: %v", err)
	}
	facts := got.Reducer.Facts()
	if len(facts) != 1 || facts[0].Icon != "bolt" {
		t.Fatalf("pre-marker v4 lost underlying fact: %+v", facts)
	}
	if facts[0].HarnessProcessHarness != "" || facts[0].HarnessProcessPID != 0 || facts[0].HarnessProcessStartToken != "" {
		t.Fatalf("pre-marker v4 retained unsafe lifecycle ownership: %+v", facts[0])
	}
}

func TestUserVarV2AndV3IgnoreInjectedProcessMetadata(t *testing.T) {
	for _, schema := range []string{"2", "3"} {
		t.Run("v"+schema, func(t *testing.T) {
			raw := `{"v":` + schema + `,"f":[{"k":"harness","y":"claude:s1","a":"active","i":"bolt","ph":"claude","pp":4321,"pt":"Mon Jul 13 10:11:12 2026"}]}`
			var got WindowState
			if err := got.UnmarshalUserVar(raw); err != nil {
				t.Fatalf("UnmarshalUserVar(v%s) error: %v", schema, err)
			}
			facts := got.Reducer.Facts()
			if len(facts) != 1 || facts[0].HarnessProcessHarness != "" || facts[0].HarnessProcessPID != 0 || facts[0].HarnessProcessStartToken != "" {
				t.Fatalf("v%s facts retained lifecycle fingerprint: %+v", schema, facts)
			}
		})
	}
}

func TestUserVarV4RestoredProcessCanSettleExit(t *testing.T) {
	process := protocol.HarnessProcessIdentity{Harness: "claude", PID: 4321, StartToken: "Mon Jul 13 10:11:12 2026"}
	r := NewReducer(ReducerConfig{})
	r.Apply(protocol.ReducerEvent{
		Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:pipe"}, Action: "active",
		Seq: 1, At: 100, PipeMode: true, HarnessProcess: process,
	})
	encoded, err := (WindowState{Reducer: r, Effective: r.Reduce(100), ShadowSeq: 1}).MarshalUserVar()
	if err != nil {
		t.Fatalf("MarshalUserVar() error: %v", err)
	}
	var restored WindowState
	if err := restored.UnmarshalUserVar(encoded); err != nil {
		t.Fatalf("UnmarshalUserVar() error: %v", err)
	}

	result := restored.Reducer.SettleHarnessProcessExit(process, 101, HarnessProcessExitOptions{PipeMode: true})
	if !result.Changed || result.WithdrawnFacts != 1 || result.After.Icon != "" {
		t.Fatalf("restored process settlement = %+v", result)
	}
}
