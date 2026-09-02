package daemon

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
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func fuzzOracleReduce(cfg ReducerConfig, facts []protocol.SourceFactSnapshot, now int64) (icons map[string]bool, looping bool) {
	stale := func(f protocol.SourceFactSnapshot) bool {
		var threshold int64
		switch f.Icon {
		case "bolt":
			threshold = cfg.StaleActive
		case "subagent":
			threshold = cfg.StaleSubagent
		case "compact":
			threshold = cfg.StaleCompact
		}
		return threshold > 0 && now-f.At >= threshold
	}
	for _, f := range facts {
		if f.Kind == protocol.SourceASL && f.LeaseLive {
			looping = true
		}
	}
	bestPrec := -1
	var bestAt int64
	var bestOrder uint64
	icons = map[string]bool{}
	for _, f := range facts {
		if f.Acked || f.Icon == "" || stale(f) {
			continue
		}
		if looping && f.Icon == "stop" {
			continue
		}
		prec := protocol.IconPrecedence(f.Icon)
		switch {
		case prec > bestPrec, prec == bestPrec && (f.At > bestAt || (f.At == bestAt && f.CausalOrder > bestOrder)):
			bestPrec, bestAt, bestOrder = prec, f.At, f.CausalOrder
			icons = map[string]bool{f.Icon: true}
		case prec == bestPrec && f.At == bestAt && f.CausalOrder == bestOrder:
			icons[f.Icon] = true // genuine tie: either icon is acceptable
		}
	}
	if bestPrec == -1 {
		icons[""] = true
	}
	return icons, looping
}

// TestFuzzReducerInvariantsEveryStep checks, after every applied event of every
// trace:
//
//   - the reduced icon matches the independent precedence oracle (this embeds
//     "active work never resolves to stopped": stop is the lowest non-idle
//     precedence, so any visible active-class fact outranks it);
//   - a visible 🤖 fact is never outranked by bolt/compact/stop/idle;
//   - the loop marker mirrors live ASL leases exactly;
//   - no event mutates another source's fact, except the designed cross-source
//     actions (prompt recovery-ack, session-end agent clearing, finished-ASL
//     retirement of older interactive active facts) and window-level user_clear.
func TestFuzzReducerInvariantsEveryStep(t *testing.T) {
	for cfgName, cfg := range fuzzCfgs {
		for _, seed := range fuzzSeeds {
			trace := newFuzzGen(seed).trace(fuzzTraceLen, true)
			r := NewReducer(cfg)

			for i, ev := range trace {
				before := factsBySource(r.Facts())
				r.Apply(ev)
				now := ev.At // reduce at receipt time so staleness is realistic
				got := r.Reduce(now)

				wantIcons, wantLooping := fuzzOracleReduce(cfg, r.Facts(), now)
				if !wantIcons[got.Icon] || got.Looping != wantLooping {
					t.Fatalf("cfg=%s seed=%d step=%d (%+v): reduce = %+v; oracle wants icons=%v looping=%v\nfacts=%+v",
						cfgName, seed, i, ev, got, wantIcons, wantLooping, r.Facts())
				}

				// 🤖 precedence: a visible subagent fact must never lose to
				// active-or-lower work.
				for _, f := range r.Facts() {
					if f.Icon == "subagent" && !f.Acked {
						if protocol.IconPrecedence(got.Icon) < protocol.IconPrecedence("subagent") {
							_, ok := fuzzCfgsStaleSubagent(cfg, f, now)
							if !ok {
								t.Fatalf("cfg=%s seed=%d step=%d: live 🤖 outranked by %q\nfacts=%+v",
									cfgName, seed, i, got.Icon, r.Facts())
							}
						}
					}
				}

				// Cross-source isolation.
				if ev.Action != "user_clear" && !ev.Prompt && !ev.EndsSession {
					after := factsBySource(r.Facts())
					for src, f := range before {
						if src == ev.Source {
							continue
						}
						if !reflect.DeepEqual(after[src], f) && !allowedASLFinishRetirement(ev, f, after[src]) {
							t.Fatalf("cfg=%s seed=%d step=%d: event %+v mutated foreign source %+v\nbefore=%+v\nafter =%+v",
								cfgName, seed, i, ev, src, f, after[src])
						}
					}
				}
			}
		}
	}
}

// fuzzCfgsStaleSubagent reports whether a subagent fact is stale at now (the
// only legitimate reason a visible 🤖 snapshot may not win precedence).
func fuzzCfgsStaleSubagent(cfg ReducerConfig, f protocol.SourceFactSnapshot, now int64) (int64, bool) {
	if cfg.StaleSubagent > 0 && now-f.At >= cfg.StaleSubagent {
		return cfg.StaleSubagent, true
	}
	return 0, false
}

func factsBySource(facts []protocol.SourceFactSnapshot) map[protocol.SourceID]protocol.SourceFactSnapshot {
	out := make(map[protocol.SourceID]protocol.SourceFactSnapshot, len(facts))
	for _, f := range facts {
		out[f.Source()] = f
	}
	return out
}

// TestFuzzReducerTerminalCrossSourceIsolation pins terminal isolation: native
// stop/stop_failure events leave every other source byte-identical, while an
// ASL finish may perform only its narrow retirement of an older interactive
// bolt/compact fact.
func TestFuzzReducerTerminalCrossSourceIsolation(t *testing.T) {
	for _, seed := range fuzzSeeds {
		trace := newFuzzGen(seed).trace(fuzzTraceLen, false)
		r := NewReducer(fuzzCfgs["production"])
		for i, ev := range trace {
			terminal := isTerminalAction(ev.Action) || ev.Phase == ASLPhaseFinished
			if !terminal || ev.EndsSession {
				r.Apply(ev)
				continue
			}
			before := factsBySource(r.Facts())
			r.Apply(ev)
			after := factsBySource(r.Facts())
			for src, f := range before {
				if src == ev.Source {
					continue
				}
				if !reflect.DeepEqual(after[src], f) && !allowedASLFinishRetirement(ev, f, after[src]) {
					t.Fatalf("seed=%d step=%d: terminal %+v mutated foreign source %+v", seed, i, ev, src)
				}
			}
		}
	}
}

// allowedASLFinishRetirement recognizes the one cross-source mutation owned by
// an ASL finish. It deliberately checks the complete snapshot delta so the fuzz
// invariant still rejects retirement of pipe/protected facts or any mutation
// beyond acknowledgement at the finish timestamp.
func allowedASLFinishRetirement(ev protocol.ReducerEvent, before, after protocol.SourceFactSnapshot) bool {
	if ev.Source.Kind != protocol.SourceASL || ev.Phase != ASLPhaseFinished ||
		before.Kind != protocol.SourceHarness || before.PipeMode || before.Acked ||
		(before.Icon != "bolt" && before.Icon != "compact") {
		return false
	}
	want := before
	want.Acked = true
	want.AckedAt = ev.At
	return reflect.DeepEqual(after, want)
}

// TestFuzzReducerProcessExitInvariants layers deterministic process settlement
// onto every generated mixed-source trace. It proves exact-fingerprint
// isolation, ASL exclusion, independent precedence-oracle agreement, and
// idempotence for both pipe and interactive policy.
func TestFuzzReducerProcessExitInvariants(t *testing.T) {
	for cfgName, cfg := range fuzzCfgs {
		for _, seed := range fuzzSeeds {
			trace := newFuzzGen(seed).trace(fuzzTraceLen, true)
			r := NewReducer(cfg)
			for _, ev := range trace {
				r.Apply(ev)
			}

			at := int64(2_000_000 + seed)
			pipeMode := seed%2 == 0
			targetSource := protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:exit-target"}
			target := processReducerEvent(targetSource, "active", 1, at, "exit-target", pipeMode, fuzzHarnessProcess)
			targetAgent := processReducerEvent(
				protocol.SourceID{Kind: protocol.SourceSubagent, Key: fmt.Sprintf("exit-agent-%d", seed)},
				"subagent", 1, at+1, "exit-target", pipeMode, fuzzHarnessProcess,
			)
			r.Apply(target)
			r.Apply(targetAgent)

			otherIdentity := fuzzHarnessProcess
			otherIdentity.PID++
			otherSource := protocol.SourceID{Kind: protocol.SourceHarness, Key: fmt.Sprintf("claude:other-%d", seed)}
			r.Apply(processReducerEvent(otherSource, "permission", 1, at+2, "other", true, otherIdentity))

			aslSource := protocol.SourceID{Kind: protocol.SourceASL, Key: fmt.Sprintf("exit-run-%d", seed)}
			r.Apply(protocol.ReducerEvent{Source: aslSource, RunID: aslSource.Key, Phase: ASLPhaseBetween, Seq: 1, At: at + 3})
			// The reducer operation must reject ASL even if restored/corrupt state
			// somehow carries an exact process fingerprint.
			r.facts[aslSource].harnessProcess = fuzzHarnessProcess

			otherBefore := *r.facts[otherSource]
			aslBefore := *r.facts[aslSource]
			before := r.Reduce(at + 4)
			result := r.SettleHarnessProcessExit(
				fuzzHarnessProcess,
				at+4,
				HarnessProcessExitOptions{PipeMode: pipeMode},
			)
			if !result.Changed || result.Before != before || result.After != r.Reduce(at+4) {
				t.Fatalf("cfg=%s seed=%d: inconsistent settlement result %+v", cfgName, seed, result)
			}
			if pipeMode {
				if result.SynthesizedStop || result.WithdrawnFacts != 2 || result.ClearedSubagents != 0 {
					t.Fatalf("cfg=%s seed=%d: pipe result = %+v", cfgName, seed, result)
				}
			} else if !result.SynthesizedStop || result.WithdrawnFacts != 0 || result.ClearedSubagents != 1 {
				t.Fatalf("cfg=%s seed=%d: interactive result = %+v", cfgName, seed, result)
			}
			if after := *r.facts[otherSource]; !reflect.DeepEqual(after, otherBefore) {
				t.Fatalf("cfg=%s seed=%d: unrelated process changed\nbefore=%+v\nafter =%+v", cfgName, seed, otherBefore, after)
			}
			if after := *r.facts[aslSource]; !reflect.DeepEqual(after, aslBefore) {
				t.Fatalf("cfg=%s seed=%d: ASL fact changed\nbefore=%+v\nafter =%+v", cfgName, seed, aslBefore, after)
			}

			wantIcons, wantLooping := fuzzOracleReduce(cfg, r.Facts(), at+4)
			if !wantIcons[result.After.Icon] || result.After.Looping != wantLooping {
				t.Fatalf("cfg=%s seed=%d: after=%+v; oracle wants icons=%v looping=%v", cfgName, seed, result.After, wantIcons, wantLooping)
			}

			afterFirst := cloneReducerSourceFacts(r)
			again := r.SettleHarnessProcessExit(
				fuzzHarnessProcess,
				at+5,
				HarnessProcessExitOptions{PipeMode: pipeMode},
			)
			if again.Changed || !reflect.DeepEqual(cloneReducerSourceFacts(r), afterFirst) {
				t.Fatalf("cfg=%s seed=%d: duplicate settlement mutated state: %+v", cfgName, seed, again)
			}
		}
	}
}
