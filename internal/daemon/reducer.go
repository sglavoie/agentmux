package daemon

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

// ReducerEvent is one normalized, source-scoped fact delivered to the reducer.
// Seq is the per-source monotonic sequence used for dedup and reordering: only
// the highest Seq seen for a source takes effect, so replay is order-independent
// and duplicates are no-ops. At is the emitted unix timestamp used for staleness
// and the legacy grace. TurnID scopes identified terminal suppression.
type ReducerEvent struct {
	Source SourceID
	Action string // active|compact|subagent|stop|elicitation|permission|failure|stop_failure|idle|clear|user_clear
	Seq    uint64
	TurnID string
	At     int64
	// OriginWindowID identifies the pane that produced this fact. It can differ
	// from the reducer's managed window when an unmanaged sibling borrows it.
	OriginWindowID int

	// Prompt marks a direct user prompt submission. A prompt is authoritative
	// recovery: it bypasses terminal trailing suppression for its own source and
	// acknowledges stuck terminal facts on every other source.
	Prompt bool
	// ToolUse is the tool-use identity carried by the event, used alongside
	// TurnID to distinguish an identifiable trailing completion of the
	// terminated turn from a genuinely new tool call.
	ToolUse string
	// PipeMode marks a non-interactive `claude -p` hook. It stamps interactive
	// ownership on the fact: an interactive (non-pipe) harness session may
	// retire an older interactive session's stale active fact, while pipe-mode
	// facts are protected from that supersession. Absent (false) is interactive.
	PipeMode bool
	// HarnessProcess is the validated, non-sensitive process fingerprint that
	// produced this event. Ordinary harness, legacy, and subagent sources retain
	// it; the zero value deliberately clears any older ownership on an accepted
	// event. ASL and probe sources never retain process ownership.
	HarnessProcess HarnessProcessIdentity

	// ASL-only. Phase selects the contributed icon and lease; for ASLPhasePaused
	// the attention icon comes from Action.
	RunID string
	Phase string

	// SessionID is the parent harness session the event belongs to. Subagent
	// facts remember it so a session's completion clears only its own agents.
	SessionID string
	// EndsSession marks a session-terminating event: after it applies, the
	// session's remaining subagent contributions (identified facts and its
	// legacy multiset bucket) are ended. With an empty SessionID the clear is
	// window-wide — identity-less clients have no session boundary.
	EndsSession bool
	// SubagentDelta adjusts the source's balanced agent multiset (+1 start,
	// -1 stop) for identity-less subagent lifecycle events. The count never
	// goes negative, and while any agent remains the source contributes 🤖.
	SubagentDelta int
}

// EffectiveState is the reduced, renderable outcome: exactly one icon state and
// the loop marker. Source identifies which fact won the icon (zero value when
// idle). It carries no side effects — callers map Icon to a palette glyph.
type EffectiveState struct {
	Icon    string // "", "bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"
	Looping bool
	Source  SourceID
}

// ReducerDecision labels how Apply handled one event, for audit traces.
type ReducerDecision string

const (
	DecisionApplied      ReducerDecision = "applied"      // fact advanced
	DecisionDuplicate    ReducerDecision = "duplicate"    // same Seq already seen
	DecisionReordered    ReducerDecision = "reordered"    // older Seq arrived late
	DecisionSuppressed   ReducerDecision = "suppressed"   // terminal suppression blocked activity
	DecisionAcknowledged ReducerDecision = "acknowledged" // user_clear acked visible facts
	DecisionQuarantined  ReducerDecision = "quarantined"  // malformed/inert event never reached the facts
)

// ReducerTrace records one Apply outcome. Reliability tests assert the full
// before/after trace, not just the final icon, so a regression that reaches the
// right icon by the wrong path still fails.
type ReducerTrace struct {
	Source   SourceID
	Seq      uint64
	Action   string
	Icon     string
	Decision ReducerDecision
	Reason   string
}

// Reducer accumulates source facts and reduces them to one EffectiveState. It
// is not safe for concurrent use; callers serialize Apply/Reduce (the live
// Handler already holds stateMu for its equivalent). It holds no clock: Reduce
// takes `now`.
type Reducer struct {
	cfg             ReducerConfig
	facts           map[SourceID]*sourceFact
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
	return &Reducer{cfg: cfg, facts: make(map[SourceID]*sourceFact)}
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
func (r *Reducer) resolve(ev ReducerEvent) (action, icon string, leaseLive bool) {
	if ev.Source.Kind == SourceASL {
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
			return ev.Action, reducerActionIcon(ev.Action), true
		case ASLPhaseFinished:
			return "stop", "stop", false
		}
	}
	return ev.Action, reducerActionIcon(ev.Action), false
}
