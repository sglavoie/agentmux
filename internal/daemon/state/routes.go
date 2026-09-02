package state

import (
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// HasLiveOriginRoute reports whether this reducer still owns the identified
// harness session from originWindowID. A terminal or acknowledged fact is not
// a route lease: the terminal event itself is admitted while the preceding
// live fact exists, then its application closes ordinary continuation.
func (r *Reducer) HasLiveOriginRoute(source protocol.SourceID, sessionID string, originWindowID int) bool {
	if r == nil || source.Kind != protocol.SourceHarness || sessionID == "" || originWindowID <= 0 {
		return false
	}
	f := r.facts[source]
	return f != nil && f.set && !f.acked && !f.terminated &&
		f.sessionID == sessionID && f.originWindowID == originWindowID
}

// CurrentLiveProducerRoute returns the newest live interactive harness route
// for producerHarness. originWindowID is the pane that emitted the producer
// fact; legacy/direct facts that predate origin stamping resolve to
// targetWindowID. Unlike CurrentInteractiveConversation, terminal facts are
// deliberately excluded: a screen probe may observe and settle only a producer
// binding that still owns live work.
func (r *Reducer) CurrentLiveProducerRoute(producerHarness string, targetWindowID int) (conversation.Identity, int, bool) {
	if r == nil || producerHarness == "" || targetWindowID <= 0 {
		return conversation.Identity{}, 0, false
	}

	var current *sourceFact
	var identity conversation.Identity
	originWindowID := 0
	for _, fact := range r.facts {
		if fact.source.Kind != protocol.SourceHarness || !fact.set || fact.acked || fact.terminated || fact.pipeMode {
			continue
		}
		candidate, ok := conversation.New(HarnessFromSourceKey(fact.source.Key, fact.sessionID), fact.sessionID)
		if !ok || candidate.Harness != producerHarness || fact.source.Key != candidate.Harness+":"+candidate.SessionID {
			continue
		}
		if current == nil || moreRecent(fact, current) {
			current = fact
			identity = candidate
			originWindowID = fact.originWindowID
			if originWindowID <= 0 {
				originWindowID = targetWindowID
			}
		}
	}
	return identity, originWindowID, current != nil
}

// HasLiveProducerRoute reports whether the exact producer/session/source pane
// binding still owns live interactive work on this reducer. targetWindowID is
// used only for legacy direct facts whose runtime origin was not stamped.
func (r *Reducer) HasLiveProducerRoute(identity conversation.Identity, sourceWindowID, targetWindowID int) bool {
	if r == nil || !identity.Valid() || sourceWindowID <= 0 || targetWindowID <= 0 {
		return false
	}
	f := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: identity.Harness + ":" + identity.SessionID}]
	if f == nil || !f.set || f.acked || f.terminated || f.pipeMode || f.sessionID != identity.SessionID {
		return false
	}
	originWindowID := f.originWindowID
	if originWindowID <= 0 {
		originWindowID = targetWindowID
	}
	return originWindowID == sourceWindowID
}

// CurrentTerminalProducerRoute returns the newest unacknowledged interactive
// terminal fact for producerHarness and its runtime origin. It is intentionally
// separate from CurrentLiveProducerRoute: terminal facts do not authorize
// ordinary continuation, but a probe may use this exact binding for bounded
// post-terminal evidence such as a routed Codex /clear observation.
func (r *Reducer) CurrentTerminalProducerRoute(producerHarness string, targetWindowID int, now int64) (conversation.Identity, int, bool) {
	if r == nil || producerHarness == "" || targetWindowID <= 0 {
		return conversation.Identity{}, 0, false
	}

	var current *sourceFact
	var identity conversation.Identity
	originWindowID := 0
	for _, fact := range r.facts {
		if fact.source.Kind != protocol.SourceHarness || !conversationFactLive(fact, now) || !fact.terminated || fact.pipeMode {
			continue
		}
		candidate, ok := conversation.New(HarnessFromSourceKey(fact.source.Key, fact.sessionID), fact.sessionID)
		if !ok || candidate.Harness != producerHarness || fact.source.Key != candidate.Harness+":"+candidate.SessionID {
			continue
		}
		if current == nil || moreRecent(fact, current) {
			current = fact
			identity = candidate
			originWindowID = fact.originWindowID
			if originWindowID <= 0 {
				originWindowID = targetWindowID
			}
		}
	}
	return identity, originWindowID, current != nil
}

// HasTerminalProducerRoute revalidates an exact post-terminal producer route.
func (r *Reducer) HasTerminalProducerRoute(identity conversation.Identity, sourceWindowID, targetWindowID int, now int64) bool {
	if r == nil || !identity.Valid() || sourceWindowID <= 0 || targetWindowID <= 0 {
		return false
	}
	f := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: identity.Harness + ":" + identity.SessionID}]
	if !conversationFactLive(f, now) || !f.terminated || f.pipeMode || f.sessionID != identity.SessionID {
		return false
	}
	originWindowID := f.originWindowID
	if originWindowID <= 0 {
		originWindowID = targetWindowID
	}
	return originWindowID == sourceWindowID
}

// ProbeStopRecovery reports whether `probe`'s own synthetic terminal stop is the
// SOLE visible contribution to the window at time `now` — the exact state a
// post-settle transcript append can strand — and, when so, returns the
// interactive session the stop was settled against (the memo stamped by
// SettleAuthoritativeIdle). It is the guard for a same-source recovery: the
// probe stop may be taken back only when nothing else is on screen, so recovery
// can never overwrite a native terminal, a newer interactive bolt, a pipe-mode
// child, a subagent, an attention/warning/error icon, or a live ASL lease. Any
// such fact contributing a visible icon (or a live lease) makes the window's
// state more than this probe's synthetic stop, and ok is false. It also returns
// false when the probe holds no live terminated stop or carries no session memo.
func (r *Reducer) ProbeStopRecovery(probe protocol.SourceID, now int64) (sessionID string, ok bool) {
	f := r.facts[probe]
	if f == nil || !f.set || f.acked || !f.terminated || r.visibleIcon(f, now) != "stop" || f.sessionID == "" {
		return "", false
	}
	for _, other := range r.facts {
		if other == f {
			continue
		}
		if r.visibleIcon(other, now) != "" {
			return "", false
		}
		if other.source.Kind == protocol.SourceASL && other.leaseLive {
			return "", false
		}
	}
	return f.sessionID, true
}

// ProbeStopRecoveryRoute resolves the exact interactive producer fact retired
// by a probe-owned synthetic stop. The returned source pane comes from that
// fact's runtime origin, so post-settle transcript recovery keeps the same
// producer/source/target binding and cannot select a pipe child or another
// harness's conversation.
func (r *Reducer) ProbeStopRecoveryRoute(
	probe protocol.SourceID,
	producerHarness string,
	targetWindowID int,
	now int64,
) (conversation.Identity, int, bool) {
	sessionID, ok := r.ProbeStopRecovery(probe, now)
	if !ok || producerHarness == "" || targetWindowID <= 0 {
		return conversation.Identity{}, 0, false
	}
	identity, ok := conversation.New(producerHarness, sessionID)
	if !ok {
		return conversation.Identity{}, 0, false
	}
	f := r.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: identity.Harness + ":" + identity.SessionID}]
	if f == nil || !f.set || !f.acked || f.terminated || f.pipeMode || f.sessionID != sessionID {
		return conversation.Identity{}, 0, false
	}
	switch f.icon {
	case "bolt", "compact":
	default:
		return conversation.Identity{}, 0, false
	}
	originWindowID := f.originWindowID
	if originWindowID <= 0 {
		originWindowID = targetWindowID
	}
	return identity, originWindowID, true
}

// RecoverInteractiveFromProbeStop takes back this probe's own synthetic terminal
// stop after fresh transcript evidence re-classified the interactive session
// Active — the post-settle-append race the two-sample gate and the apply-time
// freshness check bound but cannot make atomic. It is the counterpart to
// SettleAuthoritativeIdle: rather than fabricating a new probe icon, it undoes
// the exact supersession the settle performed — the probe stop fact is
// acknowledged (withdrawn, not deleted, so a later re-settle re-terminates it
// cleanly) and the same-session interactive harness fact the settle retired is
// reinstated to its live ⚡, refreshing only its timestamp so it neither
// self-heals as stale nor jumps ahead of a future settle. The session then owns
// the tab again and CurrentConversation can name it, so the ordinary bolt→stop
// probe cycle resumes with no external hook or prompt.
//
// Callers hold the ProbeStopRecovery guard, so this acts only when the probe
// stop is the sole visible contribution. It reinstates only a retired (acked),
// interactive (non-pipe) harness active-class bolt/compact fact for the memo
// session — never a pipe-mode child, subagent, ASL, attention, warning, or
// terminal fact. Reports whether it recovered.
func (r *Reducer) RecoverInteractiveFromProbeStop(probe protocol.SourceID, sessionID string, at int64) bool {
	pf := r.facts[probe]
	if pf == nil || !pf.set || pf.acked || !pf.terminated || pf.icon != "stop" {
		return false
	}
	var reinstate *sourceFact
	for _, f := range r.facts {
		if f == pf || f.source.Kind != protocol.SourceHarness || f.pipeMode {
			continue
		}
		if !f.set || !f.acked || f.terminated || f.sessionID != sessionID {
			continue
		}
		switch f.icon {
		case "bolt", "compact":
		default:
			continue
		}
		if reinstate == nil || moreRecent(f, reinstate) {
			reinstate = f
		}
	}
	if reinstate == nil {
		return false
	}
	reinstate.reactivate()
	reinstate.at = at
	r.stampCausalOrder(reinstate)
	pf.acknowledge(at)
	r.traces = append(r.traces, ReducerTrace{
		Source:   probe,
		Seq:      pf.seq,
		Action:   "idle",
		Icon:     "",
		Decision: protocol.DecisionApplied,
		Reason:   "probe recovered its synthetic stop; interactive session reinstated after newer active evidence",
	})
	return true
}
