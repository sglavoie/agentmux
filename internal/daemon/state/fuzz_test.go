package state

// Seeded reducer fuzz. Every test derives its event traces from a fixed set of
// seeds through math/rand, so a failure reproduces deterministically: rerun the
// same seed and the same trace replays. The generator produces the full source
// mix (harness turns, identified subagents, identity-less subagent buckets, ASL
// runs, probes, user_clear) with process fingerprints, missing replacements,
// duplicated, reordered, and delayed deliveries, and the assertions are the
// session's reliability invariants:
//
//   - duplicate redelivery is idempotent;
//   - restart (snapshot → restore through the real amx_state wire schema) after
//     EVERY prefix is equivalent to the uninterrupted run;
//   - unknown and malformed payloads never mutate facts;
//   - one source's event never mutates another source's fact except for the
//     designed cross-source actions: prompt recovery-ack, session-end subagent
//     clearing, and finished-ASL retirement of older interactive active facts;
//   - the reduced icon always matches an independently computed precedence
//     oracle (active work never resolves to stopped, live agent sets keep 🤖
//     precedence, looping mirrors live ASL leases).

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// fuzzSeeds is the fixed seed set. Each seed yields one deterministic trace per
// generator configuration; failures report the seed and event index.
var fuzzSeeds = []int64{1, 2, 3, 5, 8, 13, 21, 34, 55, 89}

// fuzzTraceLen bounds each generated trace. Kept modest so the prefix-restart
// test (quadratic in trace length) stays cheap under -race -count=10.
const fuzzTraceLen = 48

// fuzzSources is the source pool the generator draws from.
var fuzzSources = struct {
	harness  protocol.SourceID
	agents   []protocol.SourceID
	runs     []protocol.SourceID
	bucket   protocol.SourceID
	probe    protocol.SourceID
	userSrc  protocol.SourceID
	session  string
	runNames []string
}{
	harness:  protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:sess-0001"},
	agents:   []protocol.SourceID{{Kind: protocol.SourceSubagent, Key: "agent-a"}, {Kind: protocol.SourceSubagent, Key: "agent-b"}},
	runs:     []protocol.SourceID{{Kind: protocol.SourceASL, Key: "run-1"}, {Kind: protocol.SourceASL, Key: "run-2"}},
	bucket:   protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.LegacySubagentBucketKey("sess-0001")},
	probe:    protocol.ProbeSource(),
	userSrc:  protocol.SourceID{Kind: protocol.SourceProbe, Key: "user"},
	session:  "sess-0001",
	runNames: []string{"run-1", "run-2"},
}

var fuzzHarnessProcess = protocol.HarnessProcessIdentity{
	Harness: "claude", PID: 5001, StartToken: "Thu Jul 16 10:30:30 2026",
}

// fuzzGen produces one deterministic event trace. Per-source sequences are
// monotonic with occasional gaps; the shared clock advances with jitter and
// occasionally emits backdated timestamps (delayed events); a slice of the
// trace is redelivered out of order (reordered events).
type fuzzGen struct {
	rng   *rand.Rand
	seqs  map[protocol.SourceID]uint64
	clock int64
	turn  int
}

func newFuzzGen(seed int64) *fuzzGen {
	return &fuzzGen{
		rng:   rand.New(rand.NewSource(seed)),
		seqs:  make(map[protocol.SourceID]uint64),
		clock: 1_000_000,
	}
}

func (g *fuzzGen) nextSeq(src protocol.SourceID) uint64 {
	g.seqs[src] += 1 + uint64(g.rng.Intn(3)) // gaps model dropped deliveries
	return g.seqs[src]
}

func (g *fuzzGen) nextAt() int64 {
	g.clock += int64(g.rng.Intn(6))
	if g.rng.Intn(10) == 0 {
		return g.clock - int64(g.rng.Intn(30)) // delayed emit: At behind arrival
	}
	return g.clock
}

// trace generates n events. allowUserClear gates the window-level user_clear
// (excluded from duplicate-injection variants: user_clear is deliberately not
// sequence-protected — its at-most-once guarantee is the IPC dedupe layer's).
func (g *fuzzGen) trace(n int, allowUserClear bool) []protocol.ReducerEvent {
	events := make([]protocol.ReducerEvent, 0, n)
	for len(events) < n {
		ev := g.next(allowUserClear)
		events = append(events, ev)
		// Reordered redelivery: occasionally re-emit an earlier event verbatim.
		if len(events) >= 2 && g.rng.Intn(8) == 0 {
			events = append(events, events[g.rng.Intn(len(events)-1)])
		}
	}
	return events[:n]
}

func (g *fuzzGen) next(allowUserClear bool) protocol.ReducerEvent {
	roll := g.rng.Intn(100)
	switch {
	case roll < 35:
		return g.harnessEvent()
	case roll < 55:
		return g.subagentEvent()
	case roll < 75:
		return g.aslEvent()
	case roll < 85:
		return g.bucketEvent()
	case roll < 92 || !allowUserClear:
		return g.probeEvent()
	default:
		return protocol.ReducerEvent{
			Source: fuzzSources.userSrc,
			Action: "user_clear",
			Seq:    g.nextSeq(fuzzSources.userSrc),
			At:     g.nextAt(),
		}
	}
}

func (g *fuzzGen) harnessEvent() protocol.ReducerEvent {
	actions := []string{"active", "active", "compact", "stop", "permission", "elicitation", "failure", "stop_failure", "idle"}
	action := actions[g.rng.Intn(len(actions))]
	ev := protocol.ReducerEvent{
		Source:    fuzzSources.harness,
		Action:    action,
		Seq:       g.nextSeq(fuzzSources.harness),
		At:        g.nextAt(),
		TurnID:    fmt.Sprintf("turn-%d", g.turn),
		SessionID: fuzzSources.session,
	}
	if action == "active" {
		if g.rng.Intn(3) == 0 {
			ev.Prompt = true
			g.turn++
			ev.TurnID = fmt.Sprintf("turn-%d", g.turn)
		}
		if g.rng.Intn(2) == 0 {
			ev.ToolUse = fmt.Sprintf("tool-%d", g.rng.Intn(4))
		}
	}
	if action == "stop" && g.rng.Intn(4) == 0 {
		ev.EndsSession = true // SessionEnd: clears the session's remaining agents
	}
	if g.rng.Intn(4) != 0 {
		ev.HarnessProcess = fuzzHarnessProcess
	}
	return ev
}

func (g *fuzzGen) subagentEvent() protocol.ReducerEvent {
	src := fuzzSources.agents[g.rng.Intn(len(fuzzSources.agents))]
	action := "subagent"
	switch g.rng.Intn(4) {
	case 0:
		action = "stop" // clean completion: icon-less tombstone
	case 1:
		action = "stop_failure" // failure-carrying stop: sticky error
	}
	ev := protocol.ReducerEvent{
		Source:    src,
		Action:    action,
		Seq:       g.nextSeq(src),
		At:        g.nextAt(),
		SessionID: fuzzSources.session,
	}
	if g.rng.Intn(4) != 0 {
		ev.HarnessProcess = fuzzHarnessProcess
	}
	return ev
}

func (g *fuzzGen) aslEvent() protocol.ReducerEvent {
	i := g.rng.Intn(len(fuzzSources.runs))
	src := fuzzSources.runs[i]
	phases := []string{ASLPhaseTurn, ASLPhaseVerification, ASLPhaseBetween, ASLPhaseWaiting, ASLPhasePaused, ASLPhaseFinished}
	phase := phases[g.rng.Intn(len(phases))]
	ev := protocol.ReducerEvent{
		Source: src,
		Seq:    g.nextSeq(src),
		At:     g.nextAt(),
		RunID:  fuzzSources.runNames[i],
		Phase:  phase,
	}
	if phase == ASLPhasePaused {
		if g.rng.Intn(2) == 0 {
			ev.Action = "elicitation"
		} else {
			ev.Action = "failure"
		}
	}
	// Even direct/replay callers cannot attach process ownership to an ASL
	// lease; the reducer must discard this otherwise-valid fingerprint.
	if g.rng.Intn(3) == 0 {
		ev.HarnessProcess = fuzzHarnessProcess
	}
	return ev
}

func (g *fuzzGen) bucketEvent() protocol.ReducerEvent {
	delta := 1
	action := "subagent"
	if g.rng.Intn(2) == 0 {
		delta = -1
		action = "idle" // identity-less clean stop normalizes to idle
	}
	ev := protocol.ReducerEvent{
		Source:        fuzzSources.bucket,
		Action:        action,
		Seq:           g.nextSeq(fuzzSources.bucket),
		At:            g.nextAt(),
		SessionID:     fuzzSources.session,
		SubagentDelta: delta,
	}
	if g.rng.Intn(4) != 0 {
		ev.HarnessProcess = fuzzHarnessProcess
	}
	return ev
}

func (g *fuzzGen) probeEvent() protocol.ReducerEvent {
	action := "stop_failure"
	if g.rng.Intn(2) == 0 {
		action = "permission"
	}
	return protocol.ReducerEvent{
		Source: fuzzSources.probe,
		Action: action,
		Seq:    g.nextSeq(fuzzSources.probe),
		At:     g.nextAt(),
	}
}

// fuzzCfg mirrors production-shaped thresholds; the zero config disables
// staleness and the legacy grace so ordering effects dominate. Both run.
var fuzzCfgs = map[string]ReducerConfig{
	"zero":       {},
	"production": {GuardDuration: 5, StaleActive: 600, StaleSubagent: 1800, StaleCompact: 180},
}

// reduceView compares outcomes ignoring the winning Source: the visible icon
// and loop marker are what the user sees.
type reduceView struct {
	Icon    string
	Looping bool
}

func viewOf(s EffectiveState) reduceView { return reduceView{Icon: s.Icon, Looping: s.Looping} }

// TestFuzzReducerDuplicateRedeliveryIsIdempotent redelivers every event a
// second (and sometimes third) time immediately or later; facts and the reduced
// outcome must be identical to the single-delivery run.
func TestFuzzReducerDuplicateRedeliveryIsIdempotent(t *testing.T) {
	for cfgName, cfg := range fuzzCfgs {
		for _, seed := range fuzzSeeds {
			trace := newFuzzGen(seed).trace(fuzzTraceLen, false)

			base := NewReducer(cfg)
			for _, ev := range trace {
				base.Apply(ev)
			}

			dup := NewReducer(cfg)
			rng := rand.New(rand.NewSource(seed * 31))
			for i, ev := range trace {
				dup.Apply(ev)
				dup.Apply(ev) // immediate duplicate
				if i > 0 && rng.Intn(3) == 0 {
					dup.Apply(trace[rng.Intn(i)]) // late redelivery of an older event
				}
			}

			now := trace[len(trace)-1].At + 1
			if !reflect.DeepEqual(base.Facts(), dup.Facts()) {
				t.Fatalf("cfg=%s seed=%d: facts diverged under duplicate redelivery\nbase=%+v\ndup =%+v",
					cfgName, seed, base.Facts(), dup.Facts())
			}
			if got, want := viewOf(dup.Reduce(now)), viewOf(base.Reduce(now)); got != want {
				t.Fatalf("cfg=%s seed=%d: reduce diverged under duplicates: got %+v want %+v", cfgName, seed, got, want)
			}
		}
	}
}

// TestFuzzReducerRestartEquivalenceEveryPrefix snapshots after every prefix of
// each trace through the REAL amx_state wire schema (MarshalUserVar →
// UnmarshalUserVar → SetConfig, the same calls the daemon's restore path makes)
// and replays the remaining suffix. The restored run must be indistinguishable
// from the uninterrupted one.
func TestFuzzReducerRestartEquivalenceEveryPrefix(t *testing.T) {
	for cfgName, cfg := range fuzzCfgs {
		for _, seed := range fuzzSeeds {
			trace := newFuzzGen(seed).trace(fuzzTraceLen, true)
			now := trace[len(trace)-1].At + 1

			base := NewReducer(cfg)
			for _, ev := range trace {
				base.Apply(ev)
			}
			wantFacts := base.Facts()
			want := viewOf(base.Reduce(now))

			for k := 0; k <= len(trace); k++ {
				pre := NewReducer(cfg)
				for _, ev := range trace[:k] {
					pre.Apply(ev)
				}

				encoded, err := WindowState{
					Reducer:   pre,
					Effective: pre.Reduce(now),
				}.MarshalUserVar()
				if err != nil {
					t.Fatalf("cfg=%s seed=%d prefix=%d: MarshalUserVar: %v", cfgName, seed, k, err)
				}
				var restored WindowState
				if err := restored.UnmarshalUserVar(encoded); err != nil {
					t.Fatalf("cfg=%s seed=%d prefix=%d: UnmarshalUserVar: %v", cfgName, seed, k, err)
				}
				r := restored.Reducer
				if r == nil {
					r = NewReducer(cfg)
				}
				r.SetConfig(cfg) // the restore path re-binds daemon thresholds

				for _, ev := range trace[k:] {
					r.Apply(ev)
				}
				if !reflect.DeepEqual(r.Facts(), wantFacts) {
					t.Fatalf("cfg=%s seed=%d prefix=%d: facts diverged after restart\nrestored=%+v\nwant    =%+v",
						cfgName, seed, k, r.Facts(), wantFacts)
				}
				if got := viewOf(r.Reduce(now)); got != want {
					t.Fatalf("cfg=%s seed=%d prefix=%d: reduce diverged after restart: got %+v want %+v",
						cfgName, seed, k, got, want)
				}
			}
		}
	}
}

// TestFuzzReducerUnknownAndMalformedEventsPreserveState interleaves quarantined
// (malformed payload) and inert (unknown/ignored event) normalized events
// between every real event. They must be no-ops: identical facts, identical
// outcome, and no invalid-kind fact may ever appear.
func TestFuzzReducerUnknownAndMalformedEventsPreserveState(t *testing.T) {
	// The two non-deliverable shapes exactly as the daemon's hook normalization
	// emits them: a quarantined payload carries the synthetic invalid source and
	// a forced-empty action, while an unrecognized or intentionally ignored event
	// keeps its own source but resolves to no action and no ASL phase. That
	// normalization really produces these shapes for these payloads is pinned by
	// the daemon package's normalize tests; what this fuzz asserts is the other
	// half — that neither shape can move a fact.
	quarantined := func(event, reason string) protocol.NormalizedEvent {
		return protocol.NormalizedEvent{
			Source:  protocol.SourceID{Kind: protocol.SourceInvalid, Key: protocol.WindowKey(42)},
			Event:   event,
			At:      1710000000,
			Invalid: true,
			Reason:  reason,
		}
	}
	inert := func(event, reason string) protocol.NormalizedEvent {
		return protocol.NormalizedEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: protocol.WindowKey(42)},
			Event:  event,
			At:     1710000000,
			Reason: reason,
		}
	}
	junk := []protocol.NormalizedEvent{
		quarantined("Stop", `invalid_payload: source_sequence "not-a-number"`),
		quarantined("PreToolUse", `invalid_payload: emitted_time "NaN"`),
		quarantined("Stop", "invalid_payload: unexpected end of JSON input"),
		inert("TotallyUnknownEvent", "unknown_event"),
		inert("InstructionsLoaded", "ignored_event"),
		quarantined("AgentSessionLoopFinished", `invalid_payload: source_sequence "boom"`),
	}
	for i, n := range junk {
		if n.Deliverable() {
			t.Fatalf("junk[%d] unexpectedly deliverable: %+v", i, n)
		}
	}

	for _, seed := range fuzzSeeds {
		trace := newFuzzGen(seed).trace(fuzzTraceLen, true)
		now := trace[len(trace)-1].At + 1
		rng := rand.New(rand.NewSource(seed * 17))

		base := NewReducer(fuzzCfgs["production"])
		noisy := NewReducer(fuzzCfgs["production"])
		for _, ev := range trace {
			base.Apply(ev)
			if d := noisy.ApplyNormalized(junk[rng.Intn(len(junk))]); d != protocol.DecisionQuarantined {
				t.Fatalf("seed=%d: junk decision = %q, want quarantined", seed, d)
			}
			noisy.Apply(ev)
		}

		if !reflect.DeepEqual(base.Facts(), noisy.Facts()) {
			t.Fatalf("seed=%d: malformed/unknown events mutated facts\nbase =%+v\nnoisy=%+v",
				seed, base.Facts(), noisy.Facts())
		}
		if got, want := viewOf(noisy.Reduce(now)), viewOf(base.Reduce(now)); got != want {
			t.Fatalf("seed=%d: reduce diverged under junk events: got %+v want %+v", seed, got, want)
		}
		for _, f := range noisy.Facts() {
			if f.Kind == protocol.SourceInvalid {
				t.Fatalf("seed=%d: invalid-kind fact reached the fact map: %+v", seed, f)
			}
		}
	}
}

// fuzzOracleReduce independently recomputes the visible outcome from the
// exported fact snapshots: looping is any live ASL lease; each unacked,
// non-stale fact contributes its icon; ⏹ is masked while a lease is live; the
// highest precedence wins with (At, CausalOrder) recency breaking ties. It
// returns the candidate icons at the winning recency so legacy facts with an
// identical zero order accept either icon.
