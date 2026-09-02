package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// supersede lets a new user prompt acknowledge stuck terminal facts on every
// other source. A prompt is the user explicitly starting a new turn — the
// authoritative recovery for ⏹/❗ icons whose source (e.g. a daemon probe)
// will never emit a follow-up of its own. Non-terminal facts are untouched:
// concurrent attention icons keep their plan precedence over new activity.
func (r *Reducer) supersede(applied *sourceFact, ev protocol.ReducerEvent, action, icon string) {
	if !ev.Prompt || isTerminalAction(action) {
		return
	}
	// Record the prompt so a later MergeDisjoint acknowledges any older persisted
	// terminal fact imported after the fact, exactly as this loop acks the
	// terminal facts already present.
	r.promptCutoff.record(ev.Source, ev.At, ev.Seq)
	for _, f := range r.facts {
		if f == applied || !f.set || f.acked {
			continue
		}
		if f.terminated {
			f.acknowledge(ev.At)
		}
	}
}

// supersedeInteractiveActive lets authoritative foreground completion retire an
// OLDER interactive harness session's stale active-class fact so it can no
// longer mask the current outcome. It runs when an interactive (non-pipe)
// harness source either starts a new turn (a direct prompt) or reaches a native
// terminal state, and when an ASL run reaches its finished phase. Without the
// ASL case, a harness that misses its native terminal hook leaves a bolt that
// outranks the run's stop (bolt > stop), so AgentSessionLoopFinished releases
// the loop marker but the tab never converges to 🛑.
//
// Only genuinely superseded facts are acknowledged: other interactive harness
// sources contributing an active-class icon (bolt/compact) that is not more
// recent than the applied fact. Pipe-mode `claude -p` work, subagents, ASL
// leases, attention icons (key/question), warnings, and terminal errors are
// concurrent state a stale-session cleanup must never retire, so they are left
// untouched.
func (r *Reducer) supersedeInteractiveActive(applied *sourceFact, ev protocol.ReducerEvent, action string) {
	harnessBoundary := applied.source.Kind == protocol.SourceHarness && !applied.pipeMode &&
		(ev.Prompt || isTerminalAction(action))
	aslBoundary := applied.source.Kind == protocol.SourceASL && ev.Phase == ASLPhaseFinished
	if !harnessBoundary && !aslBoundary {
		return
	}
	r.retireOlderInteractiveActive(applied)
}

// retireOlderInteractiveActive acknowledges every OTHER interactive harness
// active-class fact (bolt/compact) that is not more recent than `applied`, so a
// current interactive terminal/prompt event, an authoritative idle observation
// whose probe fact stands in for `applied`, or a finished ASL run can no longer
// be masked by a stale session's ⚡. Pipe-mode `claude -p` children, subagents,
// ASL leases, attention icons (key/question), warnings, and terminal errors are
// concurrent background/protected state a stale-session cleanup must never
// retire, so they are left untouched.
func (r *Reducer) retireOlderInteractiveActive(applied *sourceFact) {
	// Record the retirement so a later MergeDisjoint retires any older persisted
	// interactive active-class fact imported after the fact, exactly as this loop
	// retires the ones already present.
	r.interactiveCutoff.record(applied.source, applied.at, applied.seq)
	for _, f := range r.facts {
		if f == applied || !f.set || f.acked {
			continue
		}
		if f.source.Kind != protocol.SourceHarness || f.pipeMode {
			continue
		}
		switch f.icon {
		case "bolt", "compact":
		default:
			continue
		}
		// A newer interactive session is genuine current work, not a stale bolt:
		// never let an older session's terminal event retire it.
		if moreRecent(f, applied) {
			continue
		}
		f.acknowledge(applied.at)
	}
}

// SettleAuthoritativeIdle folds an authoritative idle observation into the
// reducer. The observation is external evidence (a stabilized screen read) that
// the interactive session is done even though no native terminal hook arrived.
// It applies a probe-sourced stop under `probe` and retires every eligible
// OLDER interactive harness active-class fact so a stale session's ⚡ can no
// longer mask the idle, reusing the exact exclusions of
// supersedeInteractiveActive: pipe-mode `claude -p` children, subagents, ASL
// leases, attention (key/question), warnings, and terminal errors are never
// retired. The probe stop is an ordinary terminal fact, so a live ASL lease
// masks it at reduce time, a higher-precedence attention/error icon outranks
// it, and a later prompt acknowledges it — the observation settles stale work
// without overwriting newer activity or any protected/background state. It
// reports the Apply decision: DecisionApplied when the probe fact advanced, and
// a duplicate/reordered decision (no retirement performed) when `seq` is not
// newer than the probe source's existing fact.
//
// sessionID is the interactive conversation the settle retires. It is stamped
// onto the probe fact (inert for a probe source, which CurrentConversation and
// clearSessionAgents never consult) purely as a memo: because the settle acks
// the interactive fact, CurrentConversation can no longer name the session, so
// the probe fact is the only remaining record of which transcript backs this
// synthetic stop. A same-source recovery (ProbeStopRecovery /
// RecoverInteractiveFromProbeStop) reads it to re-consult that transcript when
// a post-settle append may have stranded the stop.
func (r *Reducer) SettleAuthoritativeIdle(
	probe protocol.SourceID,
	seq uint64,
	at int64,
	sessionID string,
	originWindowID int,
) protocol.ReducerDecision {
	decision := r.Apply(protocol.ReducerEvent{
		Source: probe, Action: "stop", Seq: seq, At: at,
		SessionID: sessionID, OriginWindowID: originWindowID,
	})
	if decision != protocol.DecisionApplied {
		return decision
	}
	r.retireOlderInteractiveActive(r.facts[probe])
	return decision
}

// LoopStateKnown reports whether any ASL run has ever contributed a fact, so
// callers know whether the reduced Looping value carries an opinion. Windows
// whose 🔁 marker comes from discovery-side writers (restored titles, sibling
// routing) keep those writers until an ASL event actually reaches the reducer.
func (r *Reducer) LoopStateKnown() bool {
	for id := range r.facts {
		if id.Kind == protocol.SourceASL {
			return true
		}
	}
	return false
}

// canRetract reports whether Retract would withdraw a live contribution.
func (r *Reducer) canRetract(source protocol.SourceID) bool {
	f := r.facts[source]
	return f != nil && f.set && !f.terminated && f.icon != "" && !f.acked
}

// Retract withdraws a source's non-terminal contribution, bypassing the
// idle-preserve semantics. Screen probes use it to take back their own
// key/question observation once the prompt is no longer visible, so an
// answered prompt cannot strand an attention icon over the session's real
// activity. Terminal facts are not retractable: they stay until
// acknowledgement or same-source recovery. Reports whether a live
// contribution was actually withdrawn.
func (r *Reducer) Retract(source protocol.SourceID, seq uint64, at int64) bool {
	f := r.facts[source]
	if f == nil || !f.set || f.terminated {
		return false
	}
	withdrawn := f.icon != "" && !f.acked
	if seq > f.seq {
		f.seq = seq
	}
	f.action = "idle"
	f.icon = ""
	f.at = at
	r.stampCausalOrder(f)
	r.traces = append(r.traces, ReducerTrace{
		Source:   source,
		Seq:      f.seq,
		Action:   "idle",
		Decision: protocol.DecisionApplied,
		Reason:   "probe retracted its own observation",
	})
	return withdrawn
}

// AckActiveClass acknowledges every fact currently contributing an
// active-class icon (bolt/subagent/compact). The stale sweep uses it so a
// wedged session's self-heal is reflected in the authoritative facts; leases
// and attention/terminal facts are untouched, and the next real event from
// the source clears the acknowledgement.
func (r *Reducer) AckActiveClass(at int64) {
	for _, f := range r.facts {
		if !f.set || f.acked {
			continue
		}
		switch f.icon {
		case "bolt", "subagent", "compact":
			f.acknowledge(at)
			// A swept 🤖 also rebalances the identity-less agent multiset: the
			// self-heal must not leave a stale count that would inflate the
			// next real start.
			f.agents = 0
		}
	}
}

// ExpireFact acknowledges one source's active-class contribution after the
// watchdog judged it stale. seq pins the observation: if the fact advanced
// since the caller looked at it, nothing expires — fresh activity always
// outranks a queued expiry decision. Only active-class icons
// (bolt/subagent/compact) are expirable; attention and terminal facts persist
// until the user acts. ASL loop leases survive, mirroring AckActiveClass: an
// expired turn fact loses its ⚡ but keeps 🔁 until a finish event or
// reconciliation releases the run. Reports whether the fact changed.
func (r *Reducer) ExpireFact(source protocol.SourceID, seq uint64, at int64) bool {
	f := r.facts[source]
	if f == nil || !f.set || f.acked || f.seq != seq {
		return false
	}
	switch f.icon {
	case "bolt", "subagent", "compact":
	default:
		return false
	}
	f.acknowledge(at)
	// An expired 🤖 also rebalances the identity-less agent multiset so the
	// stale count cannot inflate the next real start.
	f.agents = 0
	return true
}

// acknowledgeAll marks every fact acknowledged so no icon renders, while
// leaving ASL loop leases untouched. A subsequent higher-Seq event clears the
// ack for its source. It also rebalances every identity-less agent multiset:
// the 🤖 the user just dismissed must not reappear from a stale count.
func (r *Reducer) acknowledgeAll(at int64) {
	for _, f := range r.facts {
		f.acknowledge(at)
		f.agents = 0
	}
}

// staleThreshold returns the self-heal age for a fact's active-class icon, or 0
// when the icon never goes stale.
func (r *Reducer) staleThreshold(icon string) int64 {
	switch icon {
	case "bolt":
		return r.cfg.StaleActive
	case "subagent":
		return r.cfg.StaleSubagent
	case "compact":
		return r.cfg.StaleCompact
	}
	return 0
}

// visibleIcon returns the icon a fact contributes at time `now`, applying
// acknowledgement and active-class staleness. A stale or acknowledged fact
// contributes "" (idle).
func (r *Reducer) visibleIcon(f *sourceFact, now int64) string {
	if !f.set || f.acked {
		return ""
	}
	if t := r.staleThreshold(f.icon); t > 0 && now-f.at >= t {
		return ""
	}
	return f.icon
}
