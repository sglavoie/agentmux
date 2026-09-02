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

// TombstoneRetentionSeconds bounds how long an unacknowledged terminal fact is
// retained after termination. Terminal suppression only matters for trailing
// events shortly after a stop, so an hour comfortably covers every real burst
// while keeping the persisted amx_state payload from accreting dead sources.
// The retention policy is exported because it is a persistence contract: daemon
// behaviour that outlives a fact (restore, staleness, straggler absorption) is
// only meaningful relative to these bounds.
const TombstoneRetentionSeconds int64 = 3600

// AcknowledgedFactRetentionSeconds and MaxAcknowledgedFacts bound withdrawn
// nonterminal source cutoffs. Keeping a recent tail still absorbs ordinary
// duplicate/reordered deliveries, while old sessions cannot grow amx_state
// without limit.
const (
	AcknowledgedFactRetentionSeconds int64 = 3600
	MaxAcknowledgedFacts             int   = 64
)

// SubagentStopRetentionSeconds bounds how long a completed subagent's
// icon-less tombstone is retained. Within the window, duplicate stops and a
// start arriving after its own stop (reordered delivery) collapse onto the
// terminated fact instead of resurrecting 🤖; afterwards the identity is
// forgotten — a finished agent produces no further real events.
const SubagentStopRetentionSeconds int64 = 300

// snapshot projects one live fact onto its exported, durable view.
func (f *sourceFact) snapshot() protocol.SourceFactSnapshot {
	return protocol.SourceFactSnapshot{
		Kind:                     f.source.Kind,
		Key:                      f.source.Key,
		Action:                   f.action,
		Icon:                     f.icon,
		Seq:                      f.seq,
		CausalOrder:              f.causalOrder,
		TurnID:                   f.turnID,
		At:                       f.at,
		Acked:                    f.acked,
		AckedAt:                  f.ackedAt,
		OriginWindowID:           f.originWindowID,
		ToolUse:                  f.toolUse,
		HarnessProcessHarness:    f.harnessProcess.Harness,
		HarnessProcessPID:        f.harnessProcess.PID,
		HarnessProcessStartToken: f.harnessProcess.StartToken,
		PipeMode:                 f.pipeMode,
		SessionID:                f.sessionID,
		Agents:                   f.agents,
		RunID:                    f.runID,
		Phase:                    f.phase,
		LeaseLive:                f.leaseLive,
		Terminated:               f.terminated,
		TerminalTurn:             f.terminalTurn,
		TerminalToolUse:          f.terminalToolUse,
		TerminalAt:               f.terminalAt,
	}
}

// Fact returns one source's snapshot and whether the reducer knows the source
// at all. A known-but-withdrawn source still reports ok: callers that reason
// about acknowledgement or sequence continuity need the withdrawn entry, which
// Facts deliberately omits.
func (r *Reducer) Fact(source protocol.SourceID) (protocol.SourceFactSnapshot, bool) {
	if r == nil {
		return protocol.SourceFactSnapshot{}, false
	}
	f := r.facts[source]
	if f == nil {
		return protocol.SourceFactSnapshot{}, false
	}
	return f.snapshot(), true
}

// Facts returns a deterministic snapshot of every set fact, ordered by source
// kind then key so serialized payloads are stable across runs.
func (r *Reducer) Facts() []protocol.SourceFactSnapshot {
	out := make([]protocol.SourceFactSnapshot, 0, len(r.facts))
	for _, f := range r.facts {
		if !f.set {
			continue
		}
		out = append(out, f.snapshot())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Restore replaces the fact map with the given snapshots. Entries without a
// source kind (or carrying the synthetic invalid kind) are dropped: they can
// only come from a corrupted payload and must never become live facts.
func (r *Reducer) Restore(facts []protocol.SourceFactSnapshot) {
	r.facts = make(map[protocol.SourceID]*sourceFact, len(facts))
	r.nextCausalOrder = 0
	r.mergeCausalFloor = 0
	for _, s := range facts {
		if s.Kind == "" || s.Kind == protocol.SourceInvalid {
			continue
		}
		f := sourceFactFromSnapshot(s)
		r.facts[s.Source()] = f
		if f.causalOrder > r.nextCausalOrder {
			r.nextCausalOrder = f.causalOrder
		}
	}
}

// MergeDisjoint adds persisted facts for sources this reducer does not already
// hold, leaving every existing in-process fact's semantic state untouched. A
// degraded-startup hook can create a window's in-process state (for its own
// source only) before the first restore runs; merging lets that restore recover
// the window's other still-live durable facts — a live ASL lease, a pipe-mode
// child, a subagent,
// or an attention/error contribution — without ever overwriting the accepted
// hook fact. Before importing, the merge rebases the in-process causal-order
// range above the persisted range. That metadata-only shift preserves the fact
// that every imported snapshot predates the hook which created this fresh
// reducer, even when both share one Unix timestamp and the persisted source has
// a much higher unrelated Seq. A source already present in-process always wins
// its own slot (its persisted snapshot is discarded), so the merge cannot roll
// a newer hook fact, sequence, or terminal state backward, and repeating it is
// idempotent: a second merge finds those sources already present and skips them.
//
// Acknowledged snapshots are imported too, exactly as full Restore keeps them:
// acknowledgement does not make a fact semantically dead. user_clear leaves a
// live ASL lease's LeaseLive set, so Reduce still derives Looping from an acked
// between-turns lease; and an acked non-terminal fact stays a source-sequence
// cutoff that keeps duplicate or reordered pre-clear events no-ops. Dropping
// those disjoint facts on a hook-before-restore degraded startup would lose the
// live loop marker or let pre-clear activity reappear. The caller Compacts after
// merging, which removes the only acked facts that are genuinely dead —
// acknowledged terminal tombstones — matching the full-restore path precisely.
// Structurally invalid snapshots are still skipped so the merge never
// resurrects corrupted work.
//
// A window-level supersession accepted in-process before this merge (a
// user_clear, a prompt, or an interactive terminal event) acked/retired only the
// facts present when it ran; each imported fact is therefore re-subjected to the
// recorded cutoffs (reconcileMergedFact) so an older persisted fact cannot
// resurrect exactly the state the accepted transition superseded. Reports how
// many facts were added.
func (r *Reducer) MergeDisjoint(facts []protocol.SourceFactSnapshot) int {
	if r.facts == nil {
		r.facts = make(map[protocol.SourceID]*sourceFact, len(facts))
	}

	// First identify real additions. Repeated startup restores that contain no
	// new sources must not keep shifting the causal domain.
	candidates := make([]protocol.SourceFactSnapshot, 0, len(facts))
	importedMax := r.mergeCausalFloor
	for _, s := range facts {
		if s.Kind == "" || s.Kind == protocol.SourceInvalid {
			continue
		}
		if s.CausalOrder > importedMax {
			importedMax = s.CausalOrder
		}
		id := s.Source()
		if existing := r.facts[id]; existing != nil && existing.set {
			continue
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 0 {
		return 0
	}

	// The fresh reducer's accepted facts are causally newer than every fact in
	// the pre-restore payload. Shift only the range above the prior import floor;
	// facts imported by an earlier partial/repeated merge keep their persisted
	// relative order while the in-process generation remains above all imports.
	if importedMax > r.mergeCausalFloor {
		delta := importedMax - r.mergeCausalFloor
		for _, f := range r.facts {
			if f.set && f.causalOrder > r.mergeCausalFloor {
				f.causalOrder += delta
			}
		}
		r.nextCausalOrder += delta
		r.mergeCausalFloor = importedMax
	}

	merged := 0
	for _, s := range candidates {
		id := s.Source()
		f := sourceFactFromSnapshot(s)
		r.reconcileMergedFact(f)
		r.facts[id] = f
		merged++
	}
	return merged
}
