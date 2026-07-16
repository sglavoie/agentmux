package daemon

import "github.com/sglavoie/agentmux/internal/conversation"

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

// CurrentConversation returns the still-live parent harness identity that owns
// the managed tab. Only SourceHarness facts can establish it: subagent facts
// carry a parent session for lifecycle accounting, but do not prove that parent
// still owns the managed tab. The interactive owner wins over a newer pipe-mode
// `claude -p` child (which shares the parent-harness kind but is a background
// invocation the user is not driving); among facts of the same pipe-ness the
// newest wins. Acknowledged/expired facts are stale, and a persisted source key
// must exactly agree with its fact session ID so malformed restored state cannot
// create a live mapping.
func (r *Reducer) CurrentConversation(now int64) (conversation.Identity, bool) {
	return r.currentConversation(now, false)
}

// CurrentInteractiveConversation is the interactive-only counterpart to
// CurrentConversation: it returns the still-live interactive parent identity and
// never falls back to a sole pipe-mode `claude -p` child. The Copilot transcript
// probe uses it to resolve the session backing a stuck ⚡ so a background pipe
// invocation can never select the transcript the probe settles: a pipe-only
// window resolves to no interactive conversation and the probe bails before any
// transcript resolution or read. CurrentConversation's pipe-fallback semantics
// remain correct for descriptive callers that just want the owning conversation.
func (r *Reducer) CurrentInteractiveConversation(now int64) (conversation.Identity, bool) {
	return r.currentConversation(now, true)
}

func (r *Reducer) currentConversation(now int64, interactiveOnly bool) (conversation.Identity, bool) {
	if r == nil {
		return conversation.Identity{}, false
	}

	var current *sourceFact
	var identity conversation.Identity
	for _, fact := range r.facts {
		if fact.source.Kind != SourceHarness || !conversationFactLive(fact, now) {
			continue
		}
		if interactiveOnly && fact.pipeMode {
			continue
		}
		candidate, ok := conversation.New(harnessFromSourceKey(fact.source.Key, fact.sessionID), fact.sessionID)
		if !ok || fact.source.Key != candidate.Harness+":"+candidate.SessionID {
			continue
		}
		if current == nil || moreCurrentConversation(fact, current) {
			current = fact
			identity = candidate
		}
	}
	return identity, current != nil
}

// moreCurrentConversation reports whether f is a better current-conversation
// owner than best. An interactive fact always beats a pipe-mode one regardless
// of recency, so a `claude -p` child cannot displace the interactive session
// the user is watching; among facts of the same pipe-ness, the more recent one
// wins.
func moreCurrentConversation(f, best *sourceFact) bool {
	if f.pipeMode != best.pipeMode {
		return !f.pipeMode
	}
	return moreRecent(f, best)
}

func conversationFactLive(fact *sourceFact, now int64) bool {
	if fact == nil || !fact.set || fact.acked {
		return false
	}
	// Keep a clean terminal fact only through the same retention interval used
	// by Compact. A sticky failure remains current until it is acknowledged or
	// replaced by a newer parent fact.
	if fact.terminated && fact.icon != "error" && tombstoneRetentionSeconds > 0 && now-fact.terminalAt >= tombstoneRetentionSeconds {
		return false
	}
	return true
}

func harnessFromSourceKey(key, sessionID string) string {
	suffix := ":" + sessionID
	if sessionID == "" || len(key) <= len(suffix) || key[len(key)-len(suffix):] != suffix {
		return ""
	}
	return key[:len(key)-len(suffix)]
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

// reducerIconPrecedence encodes the target icon precedence from the plan. It is
// intentionally distinct from actionPriority (the legacy guard order): here a
// recoverable warning outranks a subagent, permission and question share a tier,
// and stop sits just above idle. Higher wins; ties break on recency (emitted
// time, then source-independent causal application order).
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
	harnessProcess HarnessProcessIdentity

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
	source SourceID
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
func (m *supersessionMark) record(source SourceID, at int64, seq uint64) {
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

// HasLiveOriginRoute reports whether this reducer still owns the identified
// harness session from originWindowID. A terminal or acknowledged fact is not
// a route lease: the terminal event itself is admitted while the preceding
// live fact exists, then its application closes ordinary continuation.
func (r *Reducer) HasLiveOriginRoute(source SourceID, sessionID string, originWindowID int) bool {
	if r == nil || source.Kind != SourceHarness || sessionID == "" || originWindowID <= 0 {
		return false
	}
	f := r.facts[source]
	return f != nil && f.set && !f.acked && !f.terminated &&
		f.sessionID == sessionID && f.originWindowID == originWindowID
}

// CurrentLiveProducerRoute returns the newest live interactive harness route
// for producerHarness. originWindowID is the pane that emitted the producer
// fact; legacy/direct facts that predate origin stamping resolve to
// targetWindowID. Unlike CurrentInteractiveConversation, terminal facts are
// deliberately excluded: a screen probe may observe and settle only a producer
// binding that still owns live work.
func (r *Reducer) CurrentLiveProducerRoute(producerHarness string, targetWindowID int) (conversation.Identity, int, bool) {
	if r == nil || producerHarness == "" || targetWindowID <= 0 {
		return conversation.Identity{}, 0, false
	}

	var current *sourceFact
	var identity conversation.Identity
	originWindowID := 0
	for _, fact := range r.facts {
		if fact.source.Kind != SourceHarness || !fact.set || fact.acked || fact.terminated || fact.pipeMode {
			continue
		}
		candidate, ok := conversation.New(harnessFromSourceKey(fact.source.Key, fact.sessionID), fact.sessionID)
		if !ok || candidate.Harness != producerHarness || fact.source.Key != candidate.Harness+":"+candidate.SessionID {
			continue
		}
		if current == nil || moreRecent(fact, current) {
			current = fact
			identity = candidate
			originWindowID = fact.originWindowID
			if originWindowID <= 0 {
				originWindowID = targetWindowID
			}
		}
	}
	return identity, originWindowID, current != nil
}

// HasLiveProducerRoute reports whether the exact producer/session/source pane
// binding still owns live interactive work on this reducer. targetWindowID is
// used only for legacy direct facts whose runtime origin was not stamped.
func (r *Reducer) HasLiveProducerRoute(identity conversation.Identity, sourceWindowID, targetWindowID int) bool {
	if r == nil || !identity.Valid() || sourceWindowID <= 0 || targetWindowID <= 0 {
		return false
	}
	f := r.facts[SourceID{Kind: SourceHarness, Key: identity.Harness + ":" + identity.SessionID}]
	if f == nil || !f.set || f.acked || f.terminated || f.pipeMode || f.sessionID != identity.SessionID {
		return false
	}
	originWindowID := f.originWindowID
	if originWindowID <= 0 {
		originWindowID = targetWindowID
	}
	return originWindowID == sourceWindowID
}

// CurrentTerminalProducerRoute returns the newest unacknowledged interactive
// terminal fact for producerHarness and its runtime origin. It is intentionally
// separate from CurrentLiveProducerRoute: terminal facts do not authorize
// ordinary continuation, but a probe may use this exact binding for bounded
// post-terminal evidence such as a routed Codex /clear observation.
func (r *Reducer) CurrentTerminalProducerRoute(producerHarness string, targetWindowID int, now int64) (conversation.Identity, int, bool) {
	if r == nil || producerHarness == "" || targetWindowID <= 0 {
		return conversation.Identity{}, 0, false
	}

	var current *sourceFact
	var identity conversation.Identity
	originWindowID := 0
	for _, fact := range r.facts {
		if fact.source.Kind != SourceHarness || !conversationFactLive(fact, now) || !fact.terminated || fact.pipeMode {
			continue
		}
		candidate, ok := conversation.New(harnessFromSourceKey(fact.source.Key, fact.sessionID), fact.sessionID)
		if !ok || candidate.Harness != producerHarness || fact.source.Key != candidate.Harness+":"+candidate.SessionID {
			continue
		}
		if current == nil || moreRecent(fact, current) {
			current = fact
			identity = candidate
			originWindowID = fact.originWindowID
			if originWindowID <= 0 {
				originWindowID = targetWindowID
			}
		}
	}
	return identity, originWindowID, current != nil
}

// HasTerminalProducerRoute revalidates an exact post-terminal producer route.
func (r *Reducer) HasTerminalProducerRoute(identity conversation.Identity, sourceWindowID, targetWindowID int, now int64) bool {
	if r == nil || !identity.Valid() || sourceWindowID <= 0 || targetWindowID <= 0 {
		return false
	}
	f := r.facts[SourceID{Kind: SourceHarness, Key: identity.Harness + ":" + identity.SessionID}]
	if !conversationFactLive(f, now) || !f.terminated || f.pipeMode || f.sessionID != identity.SessionID {
		return false
	}
	originWindowID := f.originWindowID
	if originWindowID <= 0 {
		originWindowID = targetWindowID
	}
	return originWindowID == sourceWindowID
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

// supersede lets a new user prompt acknowledge stuck terminal facts on every
// other source. A prompt is the user explicitly starting a new turn — the
// authoritative recovery for ⏹/❗ icons whose source (e.g. a daemon probe)
// will never emit a follow-up of its own. Non-terminal facts are untouched:
// concurrent attention icons keep their plan precedence over new activity.
func (r *Reducer) supersede(applied *sourceFact, ev ReducerEvent, action, icon string) {
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
func (r *Reducer) supersedeInteractiveActive(applied *sourceFact, ev ReducerEvent, action string) {
	harnessBoundary := applied.source.Kind == SourceHarness && !applied.pipeMode &&
		(ev.Prompt || isTerminalAction(action))
	aslBoundary := applied.source.Kind == SourceASL && ev.Phase == ASLPhaseFinished
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
		if f.source.Kind != SourceHarness || f.pipeMode {
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
	probe SourceID,
	seq uint64,
	at int64,
	sessionID string,
	originWindowID int,
) ReducerDecision {
	decision := r.Apply(ReducerEvent{
		Source: probe, Action: "stop", Seq: seq, At: at,
		SessionID: sessionID, OriginWindowID: originWindowID,
	})
	if decision != DecisionApplied {
		return decision
	}
	r.retireOlderInteractiveActive(r.facts[probe])
	return decision
}

// ProbeStopRecovery reports whether `probe`'s own synthetic terminal stop is the
// SOLE visible contribution to the window at time `now` — the exact state a
// post-settle transcript append can strand — and, when so, returns the
// interactive session the stop was settled against (the memo stamped by
// SettleAuthoritativeIdle). It is the guard for a same-source recovery: the
// probe stop may be taken back only when nothing else is on screen, so recovery
// can never overwrite a native terminal, a newer interactive bolt, a pipe-mode
// child, a subagent, an attention/warning/error icon, or a live ASL lease. Any
// such fact contributing a visible icon (or a live lease) makes the window's
// state more than this probe's synthetic stop, and ok is false. It also returns
// false when the probe holds no live terminated stop or carries no session memo.
func (r *Reducer) ProbeStopRecovery(probe SourceID, now int64) (sessionID string, ok bool) {
	f := r.facts[probe]
	if f == nil || !f.set || f.acked || !f.terminated || r.visibleIcon(f, now) != "stop" || f.sessionID == "" {
		return "", false
	}
	for _, other := range r.facts {
		if other == f {
			continue
		}
		if r.visibleIcon(other, now) != "" {
			return "", false
		}
		if other.source.Kind == SourceASL && other.leaseLive {
			return "", false
		}
	}
	return f.sessionID, true
}

// ProbeStopRecoveryRoute resolves the exact interactive producer fact retired
// by a probe-owned synthetic stop. The returned source pane comes from that
// fact's runtime origin, so post-settle transcript recovery keeps the same
// producer/source/target binding and cannot select a pipe child or another
// harness's conversation.
func (r *Reducer) ProbeStopRecoveryRoute(
	probe SourceID,
	producerHarness string,
	targetWindowID int,
	now int64,
) (conversation.Identity, int, bool) {
	sessionID, ok := r.ProbeStopRecovery(probe, now)
	if !ok || producerHarness == "" || targetWindowID <= 0 {
		return conversation.Identity{}, 0, false
	}
	identity, ok := conversation.New(producerHarness, sessionID)
	if !ok {
		return conversation.Identity{}, 0, false
	}
	f := r.facts[SourceID{Kind: SourceHarness, Key: identity.Harness + ":" + identity.SessionID}]
	if f == nil || !f.set || !f.acked || f.terminated || f.pipeMode || f.sessionID != sessionID {
		return conversation.Identity{}, 0, false
	}
	switch f.icon {
	case "bolt", "compact":
	default:
		return conversation.Identity{}, 0, false
	}
	originWindowID := f.originWindowID
	if originWindowID <= 0 {
		originWindowID = targetWindowID
	}
	return identity, originWindowID, true
}

// RecoverInteractiveFromProbeStop takes back this probe's own synthetic terminal
// stop after fresh transcript evidence re-classified the interactive session
// Active — the post-settle-append race the two-sample gate and the apply-time
// freshness check bound but cannot make atomic. It is the counterpart to
// SettleAuthoritativeIdle: rather than fabricating a new probe icon, it undoes
// the exact supersession the settle performed — the probe stop fact is
// acknowledged (withdrawn, not deleted, so a later re-settle re-terminates it
// cleanly) and the same-session interactive harness fact the settle retired is
// reinstated to its live ⚡, refreshing only its timestamp so it neither
// self-heals as stale nor jumps ahead of a future settle. The session then owns
// the tab again and CurrentConversation can name it, so the ordinary bolt→stop
// probe cycle resumes with no external hook or prompt.
//
// Callers hold the ProbeStopRecovery guard, so this acts only when the probe
// stop is the sole visible contribution. It reinstates only a retired (acked),
// interactive (non-pipe) harness active-class bolt/compact fact for the memo
// session — never a pipe-mode child, subagent, ASL, attention, warning, or
// terminal fact. Reports whether it recovered.
func (r *Reducer) RecoverInteractiveFromProbeStop(probe SourceID, sessionID string, at int64) bool {
	pf := r.facts[probe]
	if pf == nil || !pf.set || pf.acked || !pf.terminated || pf.icon != "stop" {
		return false
	}
	var reinstate *sourceFact
	for _, f := range r.facts {
		if f == pf || f.source.Kind != SourceHarness || f.pipeMode {
			continue
		}
		if !f.set || !f.acked || f.terminated || f.sessionID != sessionID {
			continue
		}
		switch f.icon {
		case "bolt", "compact":
		default:
			continue
		}
		if reinstate == nil || moreRecent(f, reinstate) {
			reinstate = f
		}
	}
	if reinstate == nil {
		return false
	}
	reinstate.reactivate()
	reinstate.at = at
	r.stampCausalOrder(reinstate)
	pf.acknowledge(at)
	r.traces = append(r.traces, ReducerTrace{
		Source:   probe,
		Seq:      pf.seq,
		Action:   "idle",
		Icon:     "",
		Decision: DecisionApplied,
		Reason:   "probe recovered its synthetic stop; interactive session reinstated after newer active evidence",
	})
	return true
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

// canRetract reports whether Retract would withdraw a live contribution.
func (r *Reducer) canRetract(source SourceID) bool {
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
	r.stampCausalOrder(f)
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
func (r *Reducer) ExpireFact(source SourceID, seq uint64, at int64) bool {
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

// moreRecent breaks a recency tie between two facts. Emitted timestamp is
// comparable across sources, so it decides first. Facts in the same Unix second
// use the reducer-wide causal application order; a per-source Seq is consulted
// only for the same source (the fact map normally makes that branch redundant).
// Legacy payloads written before causalOrder use a stable source-ID fallback,
// never an unrelated source's sequence.
func moreRecent(f, best *sourceFact) bool {
	if f.at != best.at {
		return f.at > best.at
	}
	if f.causalOrder != best.causalOrder {
		return f.causalOrder > best.causalOrder
	}
	if f.source == best.source {
		return f.seq > best.seq
	}
	if f.source.Kind != best.source.Kind {
		return f.source.Kind > best.source.Kind
	}
	return f.source.Key > best.source.Key
}

// Reduce collapses all facts into one EffectiveState at time `now`. The loop
// marker is set when any ASL run holds a live lease, independent of which icon
// wins. The icon is the highest-precedence visible fact, ties broken by emitted
// time and then source-independent causal application order.
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

// LatestTrace returns the most recent Apply/ApplyNormalized audit outcome.
// The handler uses it to persist the exact reducer reason alongside the
// SessionEvent without copying the reducer's entire trace history per event.
func (r *Reducer) LatestTrace() (ReducerTrace, bool) {
	if r == nil || len(r.traces) == 0 {
		return ReducerTrace{}, false
	}
	return r.traces[len(r.traces)-1], true
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
