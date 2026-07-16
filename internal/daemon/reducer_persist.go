package daemon

// This file is the reducer's persistence seam. The fact map itself stays
// unexported; schema-v4 user-var serialization and startup restoration go
// through the Facts/Restore snapshot pair, and the Compact retention policy
// keeps both the live map and the persisted payload bounded to live/recent
// facts.

import "sort"

// tombstoneRetentionSeconds bounds how long an unacknowledged terminal fact is
// retained after termination. Terminal suppression only matters for trailing
// events shortly after a stop, so an hour comfortably covers every real burst
// while keeping the persisted amx_state payload from accreting dead sources.
const tombstoneRetentionSeconds int64 = 3600

// acknowledgedFactRetentionSeconds and maxAcknowledgedFacts bound withdrawn
// nonterminal source cutoffs. Keeping a recent tail still absorbs ordinary
// duplicate/reordered deliveries, while old sessions cannot grow amx_state
// without limit.
const (
	acknowledgedFactRetentionSeconds int64 = 3600
	maxAcknowledgedFacts             int   = 64
)

// subagentStopRetentionSeconds bounds how long a completed subagent's
// icon-less tombstone is retained. Within the window, duplicate stops and a
// start arriving after its own stop (reordered delivery) collapse onto the
// terminated fact instead of resurrecting 🤖; afterwards the identity is
// forgotten — a finished agent produces no further real events.
const subagentStopRetentionSeconds int64 = 300

// SourceFactSnapshot is the exported, durable view of one source fact. Field
// names mirror sourceFact; the compact JSON tags are the schema-v4 user-var
// wire form (amx_state must stay small), so this one struct is both the
// snapshot seam and the persisted shape.
type SourceFactSnapshot struct {
	Kind   SourceKind `json:"k"`
	Key    string     `json:"y,omitempty"`
	Action string     `json:"a,omitempty"`
	Icon   string     `json:"i,omitempty"`
	Seq    uint64     `json:"q,omitempty"`
	// CausalOrder is the reducer-wide application generation. Seq remains
	// source-local deduplication metadata; this independent order is what makes
	// equal-timestamp comparisons valid across sources. Older payloads omit it
	// and restore with the deterministic legacy fallback.
	CausalOrder uint64 `json:"o,omitempty"`
	TurnID      string `json:"tn,omitempty"`
	At          int64  `json:"t,omitempty"`
	Acked       bool   `json:"ak,omitempty"`
	AckedAt     int64  `json:"aa,omitempty"`
	// OriginWindowID is the pane that produced a routed fact. It differs from
	// the reducer/window carrying this snapshot when an unmanaged sibling borrows
	// a managed title owner.
	OriginWindowID int    `json:"ow,omitempty"`
	ToolUse        string `json:"tu,omitempty"`

	// Harness process ownership is safe lifecycle metadata, never command-line
	// content. The three fields are deliberately flat and compact because this
	// snapshot is persisted in Kitty's size-sensitive amx_state user var.
	// Partial or source-incompatible triples are discarded during restoration
	// without invalidating the containing fact.
	HarnessProcessHarness    string `json:"ph,omitempty"`
	HarnessProcessPID        int    `json:"pp,omitempty"`
	HarnessProcessStartToken string `json:"pt,omitempty"`

	// PipeMode records whether this harness source is a non-interactive
	// `claude -p` child. Absent (false) restores as interactive, so a payload
	// written before this field decodes with the backward-compatible default;
	// ASL and subagent facts remain independently typed by their source kind.
	PipeMode bool `json:"pm,omitempty"`

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

// HasMalformedHarnessProcessIdentity reports a partial, source-incompatible,
// or non-canonical persisted process fingerprint. It performs validation over
// the exported snapshot only and never consults the live process table.
func (s SourceFactSnapshot) HasMalformedHarnessProcessIdentity() bool {
	present := s.HarnessProcessHarness != "" || s.HarnessProcessPID != 0 || s.HarnessProcessStartToken != ""
	if !present {
		return false
	}
	identity := restoredHarnessProcessIdentity(s)
	return identity == (HarnessProcessIdentity{}) ||
		identity.Harness != s.HarnessProcessHarness ||
		identity.PID != s.HarnessProcessPID ||
		identity.StartToken != s.HarnessProcessStartToken
}

// Facts returns a deterministic snapshot of every set fact, ordered by source
// kind then key so serialized payloads are stable across runs.
func (r *Reducer) Facts() []SourceFactSnapshot {
	out := make([]SourceFactSnapshot, 0, len(r.facts))
	for _, f := range r.facts {
		if !f.set {
			continue
		}
		out = append(out, SourceFactSnapshot{
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
	r.nextCausalOrder = 0
	r.mergeCausalFloor = 0
	for _, s := range facts {
		if s.Kind == "" || s.Kind == SourceInvalid {
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
func (r *Reducer) MergeDisjoint(facts []SourceFactSnapshot) int {
	if r.facts == nil {
		r.facts = make(map[SourceID]*sourceFact, len(facts))
	}

	// First identify real additions. Repeated startup restores that contain no
	// new sources must not keep shifting the causal domain.
	candidates := make([]SourceFactSnapshot, 0, len(facts))
	importedMax := r.mergeCausalFloor
	for _, s := range facts {
		if s.Kind == "" || s.Kind == SourceInvalid {
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
	if f.source.Kind == SourceHarness && !f.pipeMode &&
		(f.icon == "bolt" || f.icon == "compact") &&
		r.interactiveCutoff.notNewerThan(f) {
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
func sourceFactFromSnapshot(s SourceFactSnapshot) *sourceFact {
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
		leaseLive:       s.LeaseLive,
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
func restoredHarnessProcessIdentity(s SourceFactSnapshot) HarnessProcessIdentity {
	return reducerHarnessProcessIdentity(ReducerEvent{
		Source: s.Source(),
		HarnessProcess: HarnessProcessIdentity{
			Harness:    s.HarnessProcessHarness,
			PID:        s.HarnessProcessPID,
			StartToken: s.HarnessProcessStartToken,
		},
	})
}

func (s *SourceFactSnapshot) clearHarnessProcessIdentity() {
	s.HarnessProcessHarness = ""
	s.HarnessProcessPID = 0
	s.HarnessProcessStartToken = ""
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
// finish event. If a live fact already occupies the destination (a v2/v3/v4 payload
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
// real new state. Unacknowledged nonterminal facts are never compacted here —
// active-class staleness is a render-time concern (visibleIcon), not a
// retention one. Acknowledged nonterminal facts remain recent replay cutoffs
// for one hour and are capped at the newest 64. A live ASL lease is exempt even
// when acknowledged because it still contributes the loop marker.
func (r *Reducer) Compact(now int64) int {
	removed := 0
	type acknowledgedCandidate struct {
		id SourceID
		f  *sourceFact
	}
	candidates := make([]acknowledgedCandidate, 0)
	for id, f := range r.facts {
		if !f.set {
			continue
		}
		if id.Kind == SourceASL && f.leaseLive {
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

	retained := candidates[:0]
	for _, candidate := range candidates {
		if acknowledgedFactRetentionSeconds > 0 &&
			now-candidate.f.ackedAt >= acknowledgedFactRetentionSeconds {
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
	for len(retained) > maxAcknowledgedFacts {
		delete(r.facts, retained[0].id)
		retained = retained[1:]
		removed++
	}
	return removed
}
