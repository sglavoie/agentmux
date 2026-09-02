package state

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// This file implements the side-effect-free state reducer that owns each
// window's visible state in production: reduceStateUpdate folds every
// normalized hook event into it and publishes the reduced result (via
// publishEffectiveLocked) as the authoritative visible icon and loop state,
// and its facts persist as the schema-v2 user-var payload. The reducer itself
// stays deliberately decoupled from the live Handler, StateGuard, WindowState,
// renderer, and Kitty I/O: it reads no clock (callers pass `now`), touches no
// globals, and performs no rendering.
//
// The model replaces the tab's last-write-wins scalar action with source-scoped
// facts. Each fact-producing source (the main harness session, an individual
// subagent, an ASL run, a daemon probe, or a legacy unidentified client) keeps
// its own last-known state. Reduce() collapses every live fact into exactly one
// visible icon by the target precedence and one loop marker. Per-source replay
// remains order-independent through Seq; equal-timestamp facts from different
// sources follow the reducer's source-independent causal application order.

// ASL run phases. A run holds a live loop lease (🔁) from its first non-finished
// event until it finishes; the phase also selects the run's contributed icon.
const (
	ASLPhaseTurn         = "turn"         // ⚡🔁 — a turn is executing
	ASLPhaseVerification = "verification" // ⚡🔁 — verification is running
	ASLPhaseBetween      = "between"      // 🔁 only — idle between turns
	ASLPhaseWaiting      = "waiting"      // 🔁 only — worker awaits a mailbox reply
	ASLPhasePaused       = "paused"       // attention icon + 🔁 — awaiting a human
	ASLPhaseFinished     = "finished"     // 🛑, lease released
)

// ReducerConfig holds the deterministic thresholds. All durations are in
// seconds. A zero threshold disables the corresponding self-heal.
type ReducerConfig struct {
	// GuardDuration is the legacy trailing-event grace: after an unidentified
	// terminal event, later non-terminal activity from the same legacy source is
	// suppressed for this many seconds, after which activity self-heals.
	GuardDuration int64
	// StaleActive/StaleSubagent/StaleCompact are the ages at which a stuck
	// active-class icon self-heals to idle so a wedged session stops claiming the
	// tab. They mirror [defaults.watchdog] active/subagent/compact.
	StaleActive   int64
	StaleSubagent int64
	StaleCompact  int64
}

// EffectiveState is the reduced, renderable outcome: exactly one icon state and
// the loop marker. Source identifies which fact won the icon (zero value when
// idle). It carries no side effects — callers map Icon to a palette glyph.
type EffectiveState struct {
	Icon    string // "", "bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"
	Looping bool
	Source  protocol.SourceID
}

// ReducerTrace records one Apply outcome. Reliability tests assert the full
// before/after trace, not just the final icon, so a regression that reaches the
// right icon by the wrong path still fails.
type ReducerTrace struct {
	Source   protocol.SourceID
	Seq      uint64
	Action   string
	Icon     string
	Decision protocol.ReducerDecision
	Reason   string
}

// Reducer accumulates source facts and reduces them to one EffectiveState. It
// is not safe for concurrent use; callers serialize Apply/Reduce (the live
// Handler already holds stateMu for its equivalent). It holds no clock: Reduce
// takes `now`.
type Reducer struct {
	cfg             ReducerConfig
	facts           map[protocol.SourceID]*sourceFact
	traces          []ReducerTrace
	nextCausalOrder uint64

	// mergeCausalFloor is the highest causal order imported by MergeDisjoint.
	// Facts above it are the in-process generation and are shifted when a later
	// merge introduces a wider persisted order range. It is in-process only: a
	// full Restore already receives one coherent persisted order domain.
	mergeCausalFloor uint64

	// Window-level supersession watermarks (see supersessionMark). In-process
	// only, never serialized. MergeDisjoint replays them against later-imported
	// disjoint facts so a degraded-startup hook accepted before the first restore
	// is not undone by merging older persisted facts.
	clearCutoff       supersessionMark // user_clear/reset acknowledged everything visible
	promptCutoff      supersessionMark // a prompt acked stuck terminal facts on other sources
	interactiveCutoff supersessionMark // a prompt/terminal/run finish retired older interactive active facts
}

// NewReducer returns an empty reducer with the given thresholds.
func NewReducer(cfg ReducerConfig) *Reducer {
	return &Reducer{cfg: cfg, facts: make(map[protocol.SourceID]*sourceFact)}
}

// stampCausalOrder records one accepted reducer mutation on f. Per-source Seq
// continues to own duplicate/reordered rejection; this independent generation
// exists only for cross-source causal comparisons and durable replay of them.
func (r *Reducer) stampCausalOrder(f *sourceFact) {
	r.nextCausalOrder++
	f.causalOrder = r.nextCausalOrder
}

// resolve turns an event into its contributed action, icon, and loop lease.
// ASL phases are mapped here so callers only ever supply the phase (and, for a
// pause, the attention Action).
func (r *Reducer) resolve(ev protocol.ReducerEvent) (action, icon string, leaseLive bool) {
	if ev.Source.Kind == protocol.SourceASL {
		switch ev.Phase {
		case ASLPhaseTurn, ASLPhaseVerification:
			return "active", "bolt", true
		case ASLPhaseBetween:
			return "idle", "", true
		case ASLPhaseWaiting:
			// A worker mailbox wait is internal coordination until the
			// interactive supervisor explicitly hands the question to the
			// user. Keep only the live loop lease here; the correlated
			// supervisor probe owns the human-actionable question icon.
			return "idle", "", true
		case ASLPhasePaused:
			return ev.Action, ActionIcon(ev.Action), true
		case ASLPhaseFinished:
			return "stop", "stop", false
		}
	}
	return ev.Action, ActionIcon(ev.Action), false
}
