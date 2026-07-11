package daemon

// This file introduces a side-effect-free state reducer. It is deliberately
// decoupled from the live Handler, StateGuard, WindowState, renderer, and Kitty
// I/O: it reads no clock (callers pass `now`), touches no globals, and performs
// no rendering. Production behavior is unchanged — nothing in this file is wired
// into handleStateUpdate yet. Later sessions consume it (session 2 for hook
// normalization, session 5 for schema-v2 persistence).
//
// The model replaces the tab's last-write-wins scalar action with source-scoped
// facts. Each fact-producing source (the main harness session, an individual
// subagent, an ASL run, a daemon probe, or a legacy unidentified client) keeps
// its own last-known state. Reduce() collapses every live fact into exactly one
// visible icon by the target precedence and one loop marker, deterministically
// and independent of arrival order.

// SourceKind categorizes the origin of a fact. Identity matters for terminal
// suppression: identified sources suppress only their own same-turn trailing
// events, while a legacy source falls back to the guard_duration grace window.
type SourceKind string

const (
	// SourceHarness is the managed harness session (Claude/Codex/Copilot).
	SourceHarness SourceKind = "harness"
	// SourceSubagent is one spawned subagent, keyed by its agent id.
	SourceSubagent SourceKind = "subagent"
	// SourceASL is one agent-session-loop run, keyed by its run id.
	SourceASL SourceKind = "asl"
	// SourceProbe is a daemon-side screen probe (e.g. context-limit banner).
	SourceProbe SourceKind = "probe"
	// SourceLegacy is an unidentified pre-identity client. It carries no turn or
	// source key, so terminal suppression degrades to a guard_duration grace.
	SourceLegacy SourceKind = "legacy"
)

// SourceID uniquely identifies a fact producer within one window. Kind plus Key
// is the map key, so two subagents (distinct Keys) are distinct sources while a
// duplicate start for the same agent id collapses onto one fact.
type SourceID struct {
	Kind SourceKind
	Key  string
}

// ASL run phases. A run holds a live loop lease (🔁) from its first non-finished
// event until it finishes; the phase also selects the run's contributed icon.
const (
	ASLPhaseTurn         = "turn"         // ⚡🔁 — a turn is executing
	ASLPhaseVerification = "verification" // ⚡🔁 — verification is running
	ASLPhaseBetween      = "between"      // 🔁 only — idle between turns
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

	// Prompt marks a direct user prompt submission. A prompt is authoritative
	// recovery: it bypasses terminal trailing suppression for its own source and
	// acknowledges stuck terminal facts on every other source.
	Prompt bool
	// ToolUse is the tool-use identity carried by the event, used alongside
	// TurnID to distinguish an identifiable trailing completion of the
	// terminated turn from a genuinely new tool call.
	ToolUse string

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

// reducerIconPrecedence encodes the target icon precedence from the plan. It is
// intentionally distinct from actionPriority (the legacy guard order): here a
// recoverable warning outranks a subagent, permission and question share a tier,
// and stop sits just above idle. Higher wins; ties break on recency (Seq).
//
//	error > (key | question) > warning > subagent > compact > bolt > stop > idle
var reducerIconPrecedence = map[string]int{
	"":         0,
	"stop":     1,
	"bolt":     2,
	"compact":  3,
	"subagent": 4,
	"warning":  5,
	"key":      6,
	"question": 6,
	"error":    7,
}

// reducerActionIcon maps an action to its icon state under the reducer. It
// mirrors actionIconState for real states but omits the loop_marker preserve
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
	source SourceID
	set    bool
	action string
	icon   string
	seq    uint64
	turnID string
	at     int64
	acked  bool

	// toolUse is the most recent tool-use identity seen from this source, kept
	// so a terminal event can pin which tool call was in flight when it landed.
	toolUse string

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

// Reducer accumulates source facts and reduces them to one EffectiveState. It
// is not safe for concurrent use; callers serialize Apply/Reduce (the live
// Handler already holds stateMu for its equivalent). It holds no clock: Reduce
// takes `now`.
type Reducer struct {
	cfg    ReducerConfig
	facts  map[SourceID]*sourceFact
	traces []ReducerTrace
}

// NewReducer returns an empty reducer with the given thresholds.
func NewReducer(cfg ReducerConfig) *Reducer {
	return &Reducer{cfg: cfg, facts: make(map[SourceID]*sourceFact)}
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
		case ASLPhasePaused:
			return ev.Action, reducerActionIcon(ev.Action), true
		case ASLPhaseFinished:
			return "stop", "stop", false
		}
	}
	return ev.Action, reducerActionIcon(ev.Action), false
}

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
		r.acknowledgeAll()
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
	f.action = action
	f.icon = icon
	f.acked = false
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

// supersede lets a new user prompt acknowledge stuck terminal facts on every
// other source. A prompt is the user explicitly starting a new turn — the
// authoritative recovery for ⏹/❗ icons whose source (e.g. a daemon probe)
// will never emit a follow-up of its own. Non-terminal facts are untouched:
// concurrent attention icons keep their plan precedence over new activity.
func (r *Reducer) supersede(applied *sourceFact, ev ReducerEvent, action, icon string) {
	if !ev.Prompt || isTerminalAction(action) {
		return
	}
	for _, f := range r.facts {
		if f == applied || !f.set || f.acked {
			continue
		}
		if f.terminated {
			f.acked = true
		}
	}
}

// LoopStateKnown reports whether any ASL run has ever contributed a fact, so
// callers know whether the reduced Looping value carries an opinion. Windows
// whose 🔁 marker comes from discovery-side writers (restored titles, sibling
// routing) keep those writers until an ASL event actually reaches the reducer.
func (r *Reducer) LoopStateKnown() bool {
	for id := range r.facts {
		if id.Kind == SourceASL {
			return true
		}
	}
	return false
}

// Retract withdraws a source's non-terminal contribution, bypassing the
// idle-preserve semantics. Screen probes use it to take back their own
// key/question observation once the prompt is no longer visible, so an
// answered prompt cannot strand an attention icon over the session's real
// activity. Terminal facts are not retractable: they stay until
// acknowledgement or same-source recovery. Reports whether a live
// contribution was actually withdrawn.
func (r *Reducer) Retract(source SourceID, seq uint64, at int64) bool {
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
	r.traces = append(r.traces, ReducerTrace{
		Source:   source,
		Seq:      f.seq,
		Action:   "idle",
		Decision: DecisionApplied,
		Reason:   "probe retracted its own observation",
	})
	return withdrawn
}

// AckActiveClass acknowledges every fact currently contributing an
// active-class icon (bolt/subagent/compact). The stale sweep uses it so a
// wedged session's self-heal is reflected in the authoritative facts; leases
// and attention/terminal facts are untouched, and the next real event from
// the source clears the acknowledgement.
func (r *Reducer) AckActiveClass() {
	for _, f := range r.facts {
		if !f.set || f.acked {
			continue
		}
		switch f.icon {
		case "bolt", "subagent", "compact":
			f.acked = true
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
func (r *Reducer) ExpireFact(source SourceID, seq uint64) bool {
	f := r.facts[source]
	if f == nil || !f.set || f.acked || f.seq != seq {
		return false
	}
	switch f.icon {
	case "bolt", "subagent", "compact":
	default:
		return false
	}
	f.acked = true
	// An expired 🤖 also rebalances the identity-less agent multiset so the
	// stale count cannot inflate the next real start.
	f.agents = 0
	return true
}

// acknowledgeAll marks every fact acknowledged so no icon renders, while
// leaving ASL loop leases untouched. A subsequent higher-Seq event clears the
// ack for its source. It also rebalances every identity-less agent multiset:
// the 🤖 the user just dismissed must not reappear from a stale count.
func (r *Reducer) acknowledgeAll() {
	for _, f := range r.facts {
		f.acked = true
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

// moreRecent breaks a same-precedence tie between two facts. Emitted timestamp
// is comparable across sources, so it decides first; per-source Seq is the
// deterministic fallback when two facts share a timestamp.
func moreRecent(f, best *sourceFact) bool {
	if f.at != best.at {
		return f.at > best.at
	}
	return f.seq > best.seq
}

// Reduce collapses all facts into one EffectiveState at time `now`. The loop
// marker is set when any ASL run holds a live lease, independent of which icon
// wins. The icon is the highest-precedence visible fact, ties broken by Seq.
//
// While any ASL lease is live, ⏹ contributions are masked: between turns the
// loop is still running, so the harness stop that ended the previous turn (and
// a finished sibling run's 🛑) must not claim the tab — the window shows only
// 🔁 (or the live run's own bolt/attention icon, which outranks stop anyway).
// Masking at reduce time keeps the outcome order-independent: a stop arriving
// before or after the between-turns event converges to the same icon, and the
// stop fact itself is untouched — once the last lease releases, an unmasked
// terminal ⏹/🛑 becomes visible again. Only "stop" is masked; terminal errors
// and attention icons always surface.
func (r *Reducer) Reduce(now int64) EffectiveState {
	var best *sourceFact
	bestPrec := -1
	looping := false

	for _, f := range r.facts {
		if f.source.Kind == SourceASL && f.leaseLive {
			looping = true
		}
	}

	for _, f := range r.facts {
		icon := r.visibleIcon(f, now)
		if icon == "" {
			continue
		}
		if looping && icon == "stop" {
			continue
		}
		prec := reducerIconPrecedence[icon]
		if prec > bestPrec || (prec == bestPrec && best != nil && moreRecent(f, best)) {
			bestPrec = prec
			best = f
		}
	}

	out := EffectiveState{Looping: looping}
	if best != nil {
		out.Icon = r.visibleIcon(best, now)
		out.Source = best.source
	}
	return out
}

// Traces returns a copy of the audit trace accumulated so far.
func (r *Reducer) Traces() []ReducerTrace {
	out := make([]ReducerTrace, len(r.traces))
	copy(out, r.traces)
	return out
}

// Replay applies a sequence of events to a fresh reducer and returns the final
// effective state. It is a convenience for deterministic replay/order tests.
func Replay(cfg ReducerConfig, now int64, events ...ReducerEvent) EffectiveState {
	r := NewReducer(cfg)
	for _, ev := range events {
		r.Apply(ev)
	}
	return r.Reduce(now)
}
