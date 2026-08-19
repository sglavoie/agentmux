package daemon

// ApplyNormalized is the only production entry point into the reducer: the live
// path normalizes a raw hook delivery into a NormalizedEvent and hands it here.
// It enforces the schema boundary — a non-deliverable event (quarantined
// malformed payload, or an inert unknown/ignored event) is recorded in the audit
// trace but never folded into a fact, so malformed or unknown input can never
// mutate the visible icon or reach a terminal state. Deliverable events project
// onto a ReducerEvent and flow through Apply. The returned decision lets the
// shadow path tell whether the fact map changed without re-reading the trace.
func (r *Reducer) ApplyNormalized(n NormalizedEvent) ReducerDecision {
	if !n.Deliverable() {
		reason := n.Reason
		if reason == "" {
			reason = "non-deliverable event"
		}
		r.traces = append(r.traces, ReducerTrace{
			Source:   n.Source,
			Seq:      n.Seq,
			Action:   n.Action,
			Decision: DecisionQuarantined,
			Reason:   reason,
		})
		return DecisionQuarantined
	}
	return r.Apply(n.ReducerEvent())
}

// Apply folds one event into the reducer and records an audit trace entry.
// Ordering rules:
//   - user_clear acknowledges every currently visible fact but leaves ASL loop
//     leases live; only a later higher-Seq event or heartbeat reasserts activity.
//   - An event whose Seq is not newer than the source's current fact is a no-op
//     (duplicate or reordered-older), which makes replay order-independent and
//     duplicate deliveries idempotent.
//   - A terminated source suppresses later non-terminal activity: for identified
//     sources only within the same turn; for legacy sources only within the
//     guard_duration grace, after which activity self-heals.
func (r *Reducer) Apply(ev ReducerEvent) ReducerDecision {
	if ev.Action == "user_clear" {
		r.acknowledgeAll(ev.At)
		// Record the clear so a later MergeDisjoint acknowledges any pre-clear
		// persisted fact imported after the fact (the facts present now were just
		// acknowledged; ones still unseen leave no fact to collide with).
		r.clearCutoff.record(ev.Source, ev.At, ev.Seq)
		r.traces = append(r.traces, ReducerTrace{
			Source:   ev.Source,
			Seq:      ev.Seq,
			Action:   ev.Action,
			Decision: DecisionAcknowledged,
			Reason:   "user acknowledged visible facts; ASL leases preserved",
		})
		return DecisionAcknowledged
	}

	f := r.facts[ev.Source]
	if f == nil {
		f = &sourceFact{source: ev.Source}
		r.facts[ev.Source] = f
	}

	if f.set && ev.Seq <= f.seq {
		decision := DecisionDuplicate
		reason := "duplicate sequence"
		if ev.Seq < f.seq {
			decision = DecisionReordered
			reason = "older sequence arrived after a newer one"
		}
		r.traces = append(r.traces, ReducerTrace{
			Source:   ev.Source,
			Seq:      ev.Seq,
			Action:   ev.Action,
			Icon:     f.icon,
			Decision: decision,
			Reason:   reason,
		})
		return decision
	}

	// Process ownership and invocation mode follow every accepted source
	// sequence, including a suppressed idle/trailing event. A missing or invalid
	// replacement clears stale ownership so a resumed session cannot inherit the
	// old process; a valid replacement must carry its settlement policy with it.
	f.harnessProcess = reducerHarnessProcessIdentity(ev)
	f.pipeMode = ev.PipeMode

	action, icon, leaseLive := r.resolve(ev)

	// Identity-less subagent lifecycle events adjust the source's balanced
	// agent multiset instead of contributing per-agent facts. The count is
	// clamped at zero (a stop before its start is bookkeeping noise, not
	// debt), and while any agent remains the bucket keeps contributing 🤖 —
	// a clean stop only withdraws the robot when it was the last one. A
	// failure-carrying stop keeps its own terminal semantics; the count still
	// balances underneath.
	if ev.SubagentDelta != 0 {
		agents := f.agents + ev.SubagentDelta
		if agents < 0 {
			agents = 0
		}
		f.agents = agents
		if !isTerminalAction(action) && agents > 0 {
			action, icon = "subagent", "subagent"
		}
	}

	// A cleanly completed identified subagent is a lifecycle tombstone, not a
	// user-facing outcome: it must never claim the tab's ⏹. Keep the terminal
	// fact (it suppresses stragglers for the same agent id) but contribute no
	// icon. Failure-carrying stops keep their sticky error.
	if ev.Source.Kind == SourceSubagent && action == "stop" {
		icon = ""
	}

	// A passive idle/clear only withdraws active-class work (bolt, subagent,
	// compact). Attention and terminal icons persist until the user acts or the
	// source produces a real new state, mirroring the legacy cleanup semantics
	// where idle preserved stop/permission/failure icons. ASL sources are
	// exempt: their between-turns phase is a genuine idle contribution.
	if (action == "idle" || action == "clear") && ev.Source.Kind != SourceASL &&
		f.set && !f.acked && idlePreservesIcon(f.icon) {
		f.seq = ev.Seq
		r.stampCausalOrder(f)
		r.traces = append(r.traces, ReducerTrace{
			Source:   ev.Source,
			Seq:      ev.Seq,
			Action:   action,
			Icon:     f.icon,
			Decision: DecisionSuppressed,
			Reason:   "idle preserves attention/terminal icon",
		})
		return DecisionSuppressed
	}

	if f.terminated && !isTerminalAction(action) && icon != "" && r.suppressed(f, ev) {
		// Advance the sequence so this event is not reprocessed, but keep the
		// terminal fact: trailing activity must not reassert the icon.
		f.seq = ev.Seq
		r.stampCausalOrder(f)
		r.traces = append(r.traces, ReducerTrace{
			Source:   ev.Source,
			Seq:      ev.Seq,
			Action:   action,
			Icon:     f.icon,
			Decision: DecisionSuppressed,
			Reason:   r.suppressionReason(f, ev),
		})
		return DecisionSuppressed
	}

	f.set = true
	f.seq = ev.Seq
	f.at = ev.At
	f.turnID = ev.TurnID
	f.originWindowID = ev.OriginWindowID
	f.action = action
	f.icon = icon
	f.reactivate()
	r.stampCausalOrder(f)
	if ev.ToolUse != "" {
		f.toolUse = ev.ToolUse
	}
	if ev.SessionID != "" {
		f.sessionID = ev.SessionID
	}
	if ev.Source.Kind == SourceASL {
		f.runID = ev.RunID
		f.phase = ev.Phase
		f.leaseLive = leaseLive
	}
	if isTerminalAction(action) {
		f.terminated = true
		f.terminalTurn = ev.TurnID
		f.terminalToolUse = f.toolUse
		f.terminalAt = ev.At
	} else {
		f.terminated = false
		f.terminalTurn = ""
		f.terminalToolUse = ""
	}

	r.supersede(f, ev, action, icon)
	r.supersedeInteractiveActive(f, ev, action)

	reason := ""
	if ev.EndsSession {
		if cleared := r.clearSessionAgents(ev.SessionID, ev.At); cleared > 0 {
			reason = "session end cleared its remaining subagent facts"
		}
	}

	r.traces = append(r.traces, ReducerTrace{
		Source:   ev.Source,
		Seq:      ev.Seq,
		Action:   action,
		Icon:     icon,
		Decision: DecisionApplied,
		Reason:   reason,
	})
	return DecisionApplied
}

// clearSessionAgents ends the remaining subagent contributions of one harness
// session — or of every session in the window when sessionID is empty, since
// identity-less clients carry no session boundary. Identified agents become
// icon-less tombstones so stragglers for the same agent id stay suppressed
// until Compact retires them; legacy multiset buckets rebalance to zero.
// Terminal facts (a subagent's sticky ❗, an already-stopped agent) are left
// untouched: session completion clears activity, not outcomes awaiting
// acknowledgement. Reports how many facts changed.
func (r *Reducer) clearSessionAgents(sessionID string, at int64) int {
	cleared := 0
	for id, f := range r.facts {
		if !f.set || f.terminated {
			continue
		}
		switch id.Kind {
		case SourceSubagent:
			if sessionID != "" && f.sessionID != sessionID {
				continue
			}
			f.action = "stop"
			f.icon = ""
			f.terminated = true
			f.terminalTurn = ""
			f.terminalToolUse = ""
			f.terminalAt = at
			cleared++
		case SourceLegacy:
			if !isLegacySubagentBucket(id.Key) {
				continue
			}
			if sessionID != "" && id.Key != legacySubagentBucketKey(sessionID) {
				continue
			}
			if f.agents == 0 && f.icon == "" {
				continue
			}
			f.agents = 0
			f.action = "idle"
			f.icon = ""
			cleared++
		}
	}
	return cleared
}

// suppressed reports whether a terminated source should drop this trailing
// non-terminal event. A new prompt is always authoritative recovery. When both
// the terminal fact and the event carry the same identity class (tool-use ID
// first, then turn ID), only an identifiable trailing completion of the
// terminated turn is suppressed — a new tool call or turn recovers
// immediately. Without usable identity on both sides, suppression degrades to
// the guard_duration grace, after which activity self-heals.
func (r *Reducer) suppressed(f *sourceFact, ev ReducerEvent) bool {
	if ev.Prompt {
		return false
	}
	// An agent id never restarts: once a subagent source has terminated, any
	// trailing non-terminal event for the same id is a duplicate delivery or
	// a reordered straggler, regardless of turn identity or age.
	if f.source.Kind == SourceSubagent {
		return true
	}
	if ev.ToolUse != "" && f.terminalToolUse != "" {
		return ev.ToolUse == f.terminalToolUse
	}
	if ev.TurnID != "" && f.terminalTurn != "" {
		return ev.TurnID == f.terminalTurn
	}
	return ev.At-f.terminalAt < r.cfg.GuardDuration
}

func (r *Reducer) suppressionReason(f *sourceFact, ev ReducerEvent) string {
	switch {
	case f.source.Kind == SourceSubagent:
		return "stopped subagent never restarts"
	case ev.ToolUse != "" && f.terminalToolUse != "":
		return "same-source same-tool terminal suppression"
	case ev.TurnID != "" && f.terminalTurn != "":
		return "same-source same-turn terminal suppression"
	default:
		return "terminal trailing-event grace"
	}
}

// idlePreservesIcon lists the contributions a passive idle/clear must not
// withdraw: attention prompts and terminal outcomes stay visible until the
// user acknowledges them or the source emits a real new state.
func idlePreservesIcon(icon string) bool {
	switch icon {
	case "stop", "key", "question", "warning", "error":
		return true
	default:
		return false
	}
}
