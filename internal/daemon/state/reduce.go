package state

import (
	"slices"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// AgentTurn projects the agent's facts independently of ASL. It uses the same
// precedence, acknowledgement and expiry rules as Reduce, but a live run cannot
// hide a completed turn or supply agent activity of its own. This is a read-only
// projection; tab icons and notification policy still use Reduce.
func (r *Reducer) AgentTurn(now int64) *protocol.AgentTurnSnapshot {
	if r == nil {
		return nil
	}
	var best *sourceFact
	bestPrec := -1
	owner, hasOwner := r.CurrentInteractiveConversation(now)
	for _, f := range r.facts {
		if f.source.Kind == protocol.SourceASL {
			continue
		}
		// A supervised pipe-mode worker can share this pane with the interactive
		// agent. Its activity must not replace that agent's completed turn.
		if hasOwner && f.source.Kind == protocol.SourceHarness && f.source.Key != owner.Harness+":"+owner.SessionID {
			continue
		}
		if hasOwner && f.source.Kind == protocol.SourceSubagent && f.sessionID != owner.SessionID {
			continue
		}
		icon := r.visibleIcon(f, now)
		if icon == "" {
			continue
		}
		prec := protocol.IconPrecedence(icon)
		if prec > bestPrec || (prec == bestPrec && best != nil && moreRecent(f, best)) {
			best, bestPrec = f, prec
		}
	}
	out := &protocol.AgentTurnSnapshot{}
	if best != nil {
		out.IconState = r.visibleIcon(best, now)
		if best.terminated {
			out.FinishedAt = best.terminalAt
		}
	}
	return out
}

// ASLPhases keeps all live runs visible, including paused runs and leases whose
// icon was acknowledged. Deterministic, deduplicated output stays bounded by the
// phase vocabulary rather than the number of runs attached to a window.
func (r *Reducer) ASLPhases() []string {
	if r == nil {
		return nil
	}
	var phases []string
	for _, f := range r.facts {
		if f.source.Kind == protocol.SourceASL && f.leaseLive && f.phase != "" && !slices.Contains(phases, f.phase) {
			phases = append(phases, f.phase)
		}
	}
	slices.Sort(phases)
	return phases
}

// moreRecent breaks a recency tie between two facts. Emitted timestamp is
// comparable across sources, so it decides first. Facts in the same Unix second
// use the reducer-wide causal application order; a per-source Seq is consulted
// only for the same source (the fact map normally makes that branch redundant).
// Legacy payloads written before causalOrder use a stable source-ID fallback,
// never an unrelated source's sequence.
func moreRecent(f, best *sourceFact) bool {
	if f.at != best.at {
		return f.at > best.at
	}
	if f.causalOrder != best.causalOrder {
		return f.causalOrder > best.causalOrder
	}
	if f.source == best.source {
		return f.seq > best.seq
	}
	if f.source.Kind != best.source.Kind {
		return f.source.Kind > best.source.Kind
	}
	return f.source.Key > best.source.Key
}

// Reduce collapses all facts into one EffectiveState at time `now`. The loop
// marker is set when any ASL run holds a live lease, independent of which icon
// wins. The icon is the highest-precedence visible fact, ties broken by emitted
// time and then source-independent causal application order.
//
// While any ASL lease is live, ⏹ contributions are masked: between turns the
// loop is still running, so the harness stop that ended the previous turn (and
// a finished sibling run's 🛑) must not claim the tab — the window shows only
// 🔁 (or the live run's own bolt/attention icon, which outranks stop anyway).
// Masking at reduce time keeps the outcome order-independent: a stop arriving
// before or after the between-turns event converges to the same icon, and the
// stop fact itself is untouched — once the last lease releases, an unmasked
// terminal ⏹/🛑 becomes visible again. Only "stop" is masked; terminal errors
// and attention icons always surface.
func (r *Reducer) Reduce(now int64) EffectiveState {
	var best *sourceFact
	bestPrec := -1
	looping := false

	for _, f := range r.facts {
		if f.source.Kind == protocol.SourceASL && f.leaseLive {
			looping = true
		}
	}

	for _, f := range r.facts {
		icon := r.visibleIcon(f, now)
		if icon == "" {
			continue
		}
		if looping && icon == "stop" {
			continue
		}
		prec := protocol.IconPrecedence(icon)
		if prec > bestPrec || (prec == bestPrec && best != nil && moreRecent(f, best)) {
			bestPrec = prec
			best = f
		}
	}

	out := EffectiveState{Looping: looping}
	if best != nil {
		out.Icon = r.visibleIcon(best, now)
		out.Source = best.source
	}
	return out
}

// Traces returns a copy of the audit trace accumulated so far.
func (r *Reducer) Traces() []ReducerTrace {
	out := make([]ReducerTrace, len(r.traces))
	copy(out, r.traces)
	return out
}

// LatestTrace returns the most recent Apply/ApplyNormalized audit outcome.
// The handler uses it to persist the exact reducer reason alongside the
// SessionEvent without copying the reducer's entire trace history per event.
func (r *Reducer) LatestTrace() (ReducerTrace, bool) {
	if r == nil || len(r.traces) == 0 {
		return ReducerTrace{}, false
	}
	return r.traces[len(r.traces)-1], true
}

// Replay applies a sequence of events to a fresh reducer and returns the final
// effective state. It is a convenience for deterministic replay/order tests.
func Replay(cfg ReducerConfig, now int64, events ...protocol.ReducerEvent) EffectiveState {
	r := NewReducer(cfg)
	for _, ev := range events {
		r.Apply(ev)
	}
	return r.Reduce(now)
}
