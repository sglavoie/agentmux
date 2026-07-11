package daemon

// This file is the reducer's persistence seam. The fact map itself stays
// unexported; schema-v2 user-var serialization and startup restoration go
// through the Facts/Restore snapshot pair, and the Compact retention policy
// keeps both the live map and the persisted payload bounded to live/recent
// facts.

import "sort"

// tombstoneRetentionSeconds bounds how long an unacknowledged terminal fact is
// retained after termination. Terminal suppression only matters for trailing
// events shortly after a stop, so an hour comfortably covers every real burst
// while keeping the persisted amx_state payload from accreting dead sources.
const tombstoneRetentionSeconds int64 = 3600

// subagentStopRetentionSeconds bounds how long a completed subagent's
// icon-less tombstone is retained. Within the window, duplicate stops and a
// start arriving after its own stop (reordered delivery) collapse onto the
// terminated fact instead of resurrecting 🤖; afterwards the identity is
// forgotten — a finished agent produces no further real events.
const subagentStopRetentionSeconds int64 = 300

// SourceFactSnapshot is the exported, durable view of one source fact. Field
// names mirror sourceFact; the compact JSON tags are the schema-v2 user-var
// wire form (amx_state must stay small), so this one struct is both the
// snapshot seam and the persisted shape.
type SourceFactSnapshot struct {
	Kind    SourceKind `json:"k"`
	Key     string     `json:"y,omitempty"`
	Action  string     `json:"a,omitempty"`
	Icon    string     `json:"i,omitempty"`
	Seq     uint64     `json:"q,omitempty"`
	TurnID  string     `json:"tn,omitempty"`
	At      int64      `json:"t,omitempty"`
	Acked   bool       `json:"ak,omitempty"`
	ToolUse string     `json:"tu,omitempty"`

	// Subagent aggregation: the parent harness session an identified agent
	// belongs to, and the balanced identity-less agent multiset carried by a
	// legacy subagent bucket.
	SessionID string `json:"sid,omitempty"`
	Agents    int    `json:"ag,omitempty"`

	// ASL lease bookkeeping.
	RunID     string `json:"r,omitempty"`
	Phase     string `json:"p,omitempty"`
	LeaseLive bool   `json:"ll,omitempty"`

	// Terminal suppression bookkeeping.
	Terminated      bool   `json:"tm,omitempty"`
	TerminalTurn    string `json:"tt,omitempty"`
	TerminalToolUse string `json:"ttu,omitempty"`
	TerminalAt      int64  `json:"ta,omitempty"`
}

// Source reconstructs the fact's map key.
func (s SourceFactSnapshot) Source() SourceID { return SourceID{Kind: s.Kind, Key: s.Key} }

// Facts returns a deterministic snapshot of every set fact, ordered by source
// kind then key so serialized payloads are stable across runs.
func (r *Reducer) Facts() []SourceFactSnapshot {
	out := make([]SourceFactSnapshot, 0, len(r.facts))
	for _, f := range r.facts {
		if !f.set {
			continue
		}
		out = append(out, SourceFactSnapshot{
			Kind:            f.source.Kind,
			Key:             f.source.Key,
			Action:          f.action,
			Icon:            f.icon,
			Seq:             f.seq,
			TurnID:          f.turnID,
			At:              f.at,
			Acked:           f.acked,
			ToolUse:         f.toolUse,
			SessionID:       f.sessionID,
			Agents:          f.agents,
			RunID:           f.runID,
			Phase:           f.phase,
			LeaseLive:       f.leaseLive,
			Terminated:      f.terminated,
			TerminalTurn:    f.terminalTurn,
			TerminalToolUse: f.terminalToolUse,
			TerminalAt:      f.terminalAt,
		})
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
func (r *Reducer) Restore(facts []SourceFactSnapshot) {
	r.facts = make(map[SourceID]*sourceFact, len(facts))
	for _, s := range facts {
		if s.Kind == "" || s.Kind == SourceInvalid {
			continue
		}
		r.facts[s.Source()] = &sourceFact{
			source:          s.Source(),
			set:             true,
			action:          s.Action,
			icon:            s.Icon,
			seq:             s.Seq,
			turnID:          s.TurnID,
			at:              s.At,
			acked:           s.Acked,
			toolUse:         s.ToolUse,
			sessionID:       s.SessionID,
			agents:          s.Agents,
			runID:           s.RunID,
			phase:           s.Phase,
			leaseLive:       s.LeaseLive,
			terminated:      s.Terminated,
			terminalTurn:    s.TerminalTurn,
			terminalToolUse: s.TerminalToolUse,
			terminalAt:      s.TerminalAt,
		}
	}
}

// SetConfig replaces the reducer's thresholds. Restoration deserializes facts
// before the owning Handler is known, so the restore path re-binds the
// daemon's configured thresholds afterward.
func (r *Reducer) SetConfig(cfg ReducerConfig) { r.cfg = cfg }

// RekeyMigratedFacts moves facts persisted under the v1 migration key onto the
// window's live per-window key. Deserialization does not know the window ID,
// but live unstamped events key by window — without the rekey a migrated
// terminal fact and the window's next legacy event would be distinct sources,
// so a restored ⏹ would lose to any trailing bolt with no same-source
// suppression, and a migrated ASL lease could never be released by a live
// finish event. If a live fact already occupies the destination (a v2 payload
// can carry both), the higher-sequence fact wins. Reports whether any fact
// moved.
func (r *Reducer) RekeyMigratedFacts(windowID int) bool {
	moved := false
	for _, kind := range []SourceKind{SourceLegacy, SourceASL} {
		from := SourceID{Kind: kind, Key: legacyV1MigrationKey}
		f := r.facts[from]
		if f == nil {
			continue
		}
		to := SourceID{Kind: kind, Key: windowKey(windowID)}
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
	f := r.facts[SourceID{Kind: SourceASL, Key: key}]
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
// real new state. Non-terminal facts are never compacted here — active-class
// staleness is a render-time concern (visibleIcon), not a retention one, and
// the fact's Seq still guards against duplicate/reordered replays.
func (r *Reducer) Compact(now int64) int {
	removed := 0
	for id, f := range r.facts {
		if !f.set || !f.terminated {
			continue
		}
		switch {
		case f.acked:
			// Acknowledged tombstone.
		case id.Kind == SourceSubagent && f.icon != "error":
			// Completed subagent: hold through the straggler window, then drop.
			if now-f.terminalAt < subagentStopRetentionSeconds {
				continue
			}
		case f.icon != "error" && tombstoneRetentionSeconds > 0 && now-f.terminalAt >= tombstoneRetentionSeconds:
			// Expired tombstone (sticky failures are exempt).
		default:
			continue
		}
		delete(r.facts, id)
		removed++
	}
	return removed
}
