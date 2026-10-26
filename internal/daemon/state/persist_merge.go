package state

// This file is the reducer's persistence seam. The fact map itself stays
// unexported; schema-v4 user-var serialization and startup restoration go
// through the Facts/Restore snapshot pair, and the Compact retention policy
// keeps both the live map and the persisted payload bounded to live/recent
// facts.

import (
	"sort"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// reconcileMergedFact re-applies the reducer's recorded window-level
// supersessions (see supersessionMark) to one fact just imported by
// MergeDisjoint. A transition accepted before the merge acked/retired the facts
// present when it ran but left the still-unseen persisted facts untouched;
// without this a degraded-startup merge would resurrect the state the transition
// superseded. It mirrors the in-place mutations acknowledgeAll, supersede, and
// retireOlderInteractiveActive perform, scoped to facts not more recent than each
// transition so a genuinely concurrent higher-precedence contribution still
// survives:
//   - user_clear/reset acknowledged everything visible at clear time. The fact
//     is acked (agents rebalanced) but its LeaseLive is left set, exactly as
//     acknowledgeAll leaves a live ASL loop looping through a clear.
//   - a prompt acknowledged stuck terminal facts on every other source, so an
//     older imported terminal fact cannot mask the prompt's bolt.
//   - a prompt, interactive terminal, or finished ASL run retired older
//     interactive active-class (bolt/compact) facts, so a stale ⚡ cannot mask
//     the current result.
//
// Pipe-mode `claude -p` children, subagents, ASL leases, attention (key/
// question), warnings, and terminal errors are concurrent background/protected
// state the interactive-active retirement never touches — matching the live
// path — and any of them is left intact unless a user_clear (which dismisses
// everything visible) covers it.
func (r *Reducer) reconcileMergedFact(f *sourceFact) {
	acknowledge := false
	ackedAt := int64(0)
	if r.clearCutoff.notNewerThan(f) {
		acknowledge = true
		ackedAt = r.clearCutoff.at
		f.agents = 0
	}
	if f.terminated && r.promptCutoff.notNewerThan(f) {
		acknowledge = true
		if r.promptCutoff.at > ackedAt {
			ackedAt = r.promptCutoff.at
		}
	}
	if interactiveActive(f) && r.interactiveCutoff.notNewerThan(f) {
		acknowledge = true
		if r.interactiveCutoff.at > ackedAt {
			ackedAt = r.interactiveCutoff.at
		}
	}
	if acknowledge {
		f.acknowledge(ackedAt)
	}
}

// sourceFactFromSnapshot rebuilds a live fact from its durable snapshot. Both
// Restore (full rebuild) and MergeDisjoint (additive merge) share it so the two
// persistence entry points can never drift on which fields survive a restart.
func sourceFactFromSnapshot(s protocol.SourceFactSnapshot) *sourceFact {
	ackedAt := s.AckedAt
	if s.Acked && ackedAt <= 0 {
		ackedAt = s.At
		if s.TerminalAt > ackedAt {
			ackedAt = s.TerminalAt
		}
		if ackedAt <= 0 {
			ackedAt = 1
		}
	}
	if !s.Acked {
		ackedAt = 0
	}
	// Older builds persisted paused runs as live leases. A paused run has no
	// loop process, so neither restore nor a late merge may revive 🔁.
	leaseLive := s.LeaseLive && s.Phase != ASLPhasePaused
	return &sourceFact{
		source:          s.Source(),
		set:             true,
		action:          s.Action,
		icon:            s.Icon,
		seq:             s.Seq,
		causalOrder:     s.CausalOrder,
		turnID:          s.TurnID,
		at:              s.At,
		acked:           s.Acked,
		ackedAt:         ackedAt,
		originWindowID:  s.OriginWindowID,
		toolUse:         s.ToolUse,
		harnessProcess:  restoredHarnessProcessIdentity(s),
		pipeMode:        s.PipeMode,
		sessionID:       s.SessionID,
		agents:          s.Agents,
		runID:           s.RunID,
		phase:           s.Phase,
		leaseLive:       leaseLive,
		terminated:      s.Terminated,
		terminalTurn:    s.TerminalTurn,
		terminalToolUse: s.TerminalToolUse,
		terminalAt:      s.TerminalAt,
	}
}

// restoredHarnessProcessIdentity validates lifecycle metadata independently
// from the containing fact. A malformed or partial fingerprint becomes the
// zero identity while the fact itself remains restorable. Reusing the reducer
// admission boundary also enforces ordinary-source ownership and the harness
// source-key match for SourceHarness facts.
func restoredHarnessProcessIdentity(s protocol.SourceFactSnapshot) protocol.HarnessProcessIdentity {
	return reducerHarnessProcessIdentity(protocol.ReducerEvent{
		Source: s.Source(),
		HarnessProcess: protocol.HarnessProcessIdentity{
			Harness:    s.HarnessProcessHarness,
			PID:        s.HarnessProcessPID,
			StartToken: s.HarnessProcessStartToken,
		},
	})
}

// SetConfig replaces the reducer's thresholds. Restoration deserializes facts
// before the owning Handler is known, so the restore path re-binds the
// daemon's configured thresholds afterward.
func (r *Reducer) SetConfig(cfg ReducerConfig) { r.cfg = cfg }

// Config returns the thresholds currently bound to the reducer, so a caller
// that re-binds them (the restore path) can prove the binding took.
func (r *Reducer) Config() ReducerConfig { return r.cfg }

// RekeyMigratedFacts moves facts persisted under the v1 migration key onto the
// window's live per-window key. Deserialization does not know the window ID,
// but live unstamped events key by window — without the rekey a migrated
// terminal fact and the window's next legacy event would be distinct sources,
// so a restored ⏹ would lose to any trailing bolt with no same-source
// suppression, and a migrated ASL lease could never be released by a live
// finish event. If a live fact already occupies the destination (a v2/v3/v4 payload
// can carry both), the higher-sequence fact wins. Reports whether any fact
// moved.
func (r *Reducer) RekeyMigratedFacts(windowID int) bool {
	moved := false
	for _, kind := range []protocol.SourceKind{protocol.SourceLegacy, protocol.SourceASL} {
		from := protocol.SourceID{Kind: kind, Key: legacyV1MigrationKey}
		f := r.facts[from]
		if f == nil {
			continue
		}
		to := protocol.SourceID{Kind: kind, Key: protocol.WindowKey(windowID)}
		if existing := r.facts[to]; existing != nil && existing.seq >= f.seq {
			delete(r.facts, from)
			continue
		}
		delete(r.facts, from)
		f.source = to
		r.facts[to] = f
		moved = true
	}
	return moved
}

// LeaseState reports whether an ASL source fact exists for key and whether its
// loop lease is live. Reconciliation uses it to decide between seeding a
// missing run and leaving a live or deliberately released lease alone.
func (r *Reducer) LeaseState(key string) (exists, live bool) {
	f := r.facts[protocol.SourceID{Kind: protocol.SourceASL, Key: key}]
	if f == nil || !f.set {
		return false, false
	}
	return true, f.leaseLive
}

// MaxSeq returns the highest per-source sequence currently held, so a restored
// window can continue its daemon-assigned receipt sequence above every
// persisted fact instead of colliding into duplicate/reordered suppression.
func (r *Reducer) MaxSeq() uint64 {
	var highest uint64
	for _, f := range r.facts {
		if f.set && f.seq > highest {
			highest = f.seq
		}
	}
	return highest
}

// Compact enforces the retention policy on the live fact map and returns how
// many facts it removed. Only live/recent facts survive:
//   - a cleanly completed subagent contributes no icon (its ⏹ must not claim
//     the tab) and is retained only through the straggler window, so a
//     duplicate stop or a start reordered after its own stop collapses onto
//     the terminated fact instead of resurrecting 🤖 — then it is dropped,
//     since a finished identity produces no further real events;
//   - an acknowledged terminal fact is removed (the user has seen it, and it
//     can never render again);
//   - an unacknowledged terminal stop expires after the tombstone retention.
//
// An unacknowledged terminal FAILURE (error icon) never expires: it stays
// sticky until the user acknowledges it or the same source recovers with a
// real new state. Unacknowledged nonterminal facts are never compacted here —
// active-class staleness is a render-time concern (visibleIcon), not a
// retention one. Acknowledged nonterminal facts remain recent replay cutoffs
// for one hour and are capped at the newest 64. A live ASL lease is exempt even
// when acknowledged because it still contributes the loop marker.
func (r *Reducer) Compact(now int64) int {
	removed := 0
	type acknowledgedCandidate struct {
		id protocol.SourceID
		f  *sourceFact
	}
	candidates := make([]acknowledgedCandidate, 0)
	for id, f := range r.facts {
		if !f.set {
			continue
		}
		if id.Kind == protocol.SourceASL && f.leaseLive {
			continue
		}
		if !f.terminated {
			if f.acked {
				candidates = append(candidates, acknowledgedCandidate{id: id, f: f})
			}
			continue
		}
		switch {
		case f.acked:
			// Acknowledged tombstone.
		case id.Kind == protocol.SourceSubagent && f.icon != "error":
			// Completed subagent: hold through the straggler window, then drop.
			if now-f.terminalAt < SubagentStopRetentionSeconds {
				continue
			}
		case f.icon != "error" && TombstoneRetentionSeconds > 0 && now-f.terminalAt >= TombstoneRetentionSeconds:
			// Expired tombstone (sticky failures are exempt).
		default:
			continue
		}
		delete(r.facts, id)
		removed++
	}

	retained := candidates[:0]
	for _, candidate := range candidates {
		if AcknowledgedFactRetentionSeconds > 0 &&
			now-candidate.f.ackedAt >= AcknowledgedFactRetentionSeconds {
			delete(r.facts, candidate.id)
			removed++
			continue
		}
		retained = append(retained, candidate)
	}
	sort.Slice(retained, func(i, j int) bool {
		left, right := retained[i], retained[j]
		if left.f.ackedAt != right.f.ackedAt {
			return left.f.ackedAt < right.f.ackedAt
		}
		if left.f.causalOrder != right.f.causalOrder {
			return left.f.causalOrder < right.f.causalOrder
		}
		if left.id.Kind != right.id.Kind {
			return left.id.Kind < right.id.Kind
		}
		return left.id.Key < right.id.Key
	})
	for len(retained) > MaxAcknowledgedFacts {
		delete(r.facts, retained[0].id)
		retained = retained[1:]
		removed++
	}
	return removed
}
