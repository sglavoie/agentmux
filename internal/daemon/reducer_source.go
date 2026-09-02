package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// reducerActionIcon maps an action to its icon state under the reducer. It
// mirrors protocol.IconState for real states but omits the loop_marker preserve
// hack — the reducer models the loop as a lease, not an icon action.
func reducerActionIcon(action string) string {
	switch action {
	case "active":
		return "bolt"
	case "compact":
		return "compact"
	case "subagent":
		return "subagent"
	case "stop":
		return "stop"
	case "elicitation", "question":
		return "question"
	case "permission":
		return "key"
	case "failure":
		return "warning"
	case "stop_failure":
		return "error"
	default: // idle, clear, user_clear, unknown
		return ""
	}
}

// isTerminalAction reports whether an action ends the current turn's activity.
// stop and stop_failure both terminate; stop_failure is additionally sticky
// (only a user_clear or a new turn dislodges it).
func isTerminalAction(action string) bool {
	return action == "stop" || action == "stop_failure"
}

// sourceFact is one source's last-known state inside a Reducer.
type sourceFact struct {
	source protocol.SourceID
	set    bool
	action string
	icon   string
	seq    uint64
	// causalOrder is the reducer-wide application generation assigned whenever
	// this fact accepts a mutation. Unlike seq, it is comparable across sources
	// and therefore safely orders facts emitted in the same Unix second.
	causalOrder uint64
	turnID      string
	at          int64
	acked       bool
	ackedAt     int64
	// originWindowID is runtime route metadata. Persistence is introduced with
	// the schema-v3 migration; restored older facts safely carry 0.
	originWindowID int

	// toolUse is the most recent tool-use identity seen from this source, kept
	// so a terminal event can pin which tool call was in flight when it landed.
	toolUse string

	// pipeMode marks a non-interactive `claude -p` harness source. Interactive
	// ownership consults it: an interactive harness terminal event or prompt
	// retires older interactive active facts, but never a pipe-mode one, and
	// CurrentConversation prefers the interactive owner over a pipe-mode child.
	pipeMode bool
	// harnessProcess ties this fact to the exact OS process that emitted it.
	// The zero value means the source has no safely attributable process.
	harnessProcess protocol.HarnessProcessIdentity

	// sessionID is the parent harness session this fact belongs to (subagent
	// facts group by it); agents is the balanced identity-less agent multiset
	// carried by a legacy subagent bucket. Both are zero for ordinary facts.
	sessionID string
	agents    int

	// ASL lease bookkeeping.
	runID     string
	phase     string
	leaseLive bool

	// Terminal suppression bookkeeping. When a terminal action lands, the fact
	// remembers the turn, tool call, and time so later trailing activity can be
	// dropped only when it is identifiably part of the terminated turn.
	terminated      bool
	terminalTurn    string
	terminalToolUse string
	terminalAt      int64
}

// supersessionMark records a window-level supersession transition by the applied
// event's recency: emitted time first, then per-source sequence — but only
// against facts of the transition's own source, because a per-source sequence
// carries no causal order across sources. user_clear, a prompt's terminal
// acknowledgement, and interactive active-class retirement all act by mutating
// the facts present when they run (acking/retiring them in place); they leave no
// durable fact a later import could collide with. The mark captures the
// transition so MergeDisjoint can replay it against facts imported afterward —
// the degraded-startup ordering where a hook is accepted before the first
// persisted-fact restore and older persisted facts are merged in later.
//
// Marks are in-process only and never serialized: the merge always runs on the
// same reducer shortly after the transition, and a full Restore already carries
// the transition baked into each persisted fact's acked flag.
type supersessionMark struct {
	set    bool
	at     int64
	seq    uint64
	source protocol.SourceID
}

// acknowledge marks a fact as withdrawn at a deterministic event time. Live
// callers pass the event/observation timestamp that caused the withdrawal. The
// fallback keeps synthetic or legacy zero-time events deterministic while
// preserving 0 as the wire-level "timestamp absent" sentinel.
func (f *sourceFact) acknowledge(at int64) {
	if at <= 0 {
		at = f.at
		if f.terminalAt > at {
			at = f.terminalAt
		}
		if at <= 0 {
			at = 1
		}
	}
	f.acked = true
	f.ackedAt = at
}

// reactivate makes a fact live again and clears the acknowledgement timestamp
// as one invariant-preserving operation.
func (f *sourceFact) reactivate() {
	f.acked = false
	f.ackedAt = 0
}

// record advances the mark to the more recent of its current value and the given
// transition, so the newest transition of its class is the cutoff. Recordings
// happen in application order on one serialized reducer, so a same-timestamp
// transition from a different source is later work and advances the mark; the
// per-source sequence breaks a tie only within the same source.
func (m *supersessionMark) record(source protocol.SourceID, at int64, seq uint64) {
	if !m.set || at > m.at || (at == m.at && (source != m.source || seq > m.seq)) {
		m.set, m.at, m.seq, m.source = true, at, seq, source
	}
}

// notNewerThan reports whether f is not strictly more recent than the mark, so f
// existed at or before the transition the mark records and is therefore subject
// to its supersession. An unset mark supersedes nothing. Recency is the emitted
// time first; on a timestamp tie the per-source sequence decides only when f
// belongs to the mark's own source. A same-timestamp fact from any other source
// is not newer: its sequence is an unrelated per-source counter (during degraded
// startup the fresh in-process reducer starts at low sequences while the older
// persisted generation may carry arbitrarily high ones), and every fact this
// runs against was imported from the pre-restore persisted generation, which
// cannot causally follow a transition this process accepted.
func (m supersessionMark) notNewerThan(f *sourceFact) bool {
	if !m.set {
		return false
	}
	if f.at != m.at {
		return f.at < m.at
	}
	if f.source == m.source {
		return f.seq <= m.seq
	}
	return true
}
