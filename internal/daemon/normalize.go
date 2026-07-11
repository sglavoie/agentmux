package daemon

// This file introduces the normalized hook-event schema. A raw HookPayload is
// noisy, dialect-specific (snake vs camel), and carries sensitive prompt bodies.
// The reducer must never see any of that: it consumes only NormalizedEvent,
// which is the single, schema-stable front door to Reduce/Apply. Normalization
// parses the identity fields, assigns a stable source key, resolves the action
// (or ASL phase), and quarantines anything malformed so a bad sequence can never
// shadow a later real event or paint a terminal icon.
//
// Like reducer.go, nothing here is wired into production rendering yet. It is
// pure: NormalizedFromHook is a deterministic function of its inputs and reads
// no clock, so the same payload always normalizes to the same event.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// InvalidPayloadReason labels a quarantined event whose payload could not be
// trusted (unparseable JSON, or a present-but-malformed numeric identity field).
// Quarantined events never reach the fact map, so they cannot mutate the visible
// icon.
const InvalidPayloadReason = "invalid_payload"

const (
	// reasonUnknownEvent marks an event name the resolver does not recognize.
	// It is inert (empty action), not quarantined: an unknown event simply has
	// no icon effect, matching the live handler's suppression path.
	reasonUnknownEvent = "unknown_event"
	// reasonIgnoredEvent marks an event that is recognized but intentionally has
	// no tab-icon effect.
	reasonIgnoredEvent = "ignored_event"
)

// SourceInvalid is the synthetic source kind stamped onto a quarantined event's
// trace. It is never stored as a live fact; it exists only so audit traces can
// attribute the drop. It is intentionally outside the five real SourceKinds.
const SourceInvalid SourceKind = "invalid_payload"

// NormalizedEvent is the schema-stable, source-scoped view of one hook event and
// the ONLY input the reducer accepts (via ApplyNormalized). It deliberately
// omits prompt bodies and other free text — only identity, timing, the resolved
// action, and the ASL phase survive. Deliverable events fold into the reducer;
// Invalid events are quarantined and inert events (unknown/ignored) are no-ops.
type NormalizedEvent struct {
	Source SourceID
	Action string // resolved canonical action, "" for inert (unknown/ignored) events
	Seq    uint64 // per-source monotonic sequence parsed from source_sequence
	TurnID string // scopes identified terminal suppression
	At     int64  // emitted unix timestamp for staleness/recency ordering

	// Prompt marks a direct user prompt submission — authoritative recovery
	// from terminal states. ToolUse carries the event's tool-use identity so a
	// trailing completion of a terminated turn can be told apart from a new
	// tool call.
	Prompt  bool
	ToolUse string

	// ASL-only. Phase selects the reducer icon + loop lease; for a paused run the
	// attention icon comes from Action.
	RunID string
	Phase string

	// SessionID is the parent harness session carried by the payload. Subagent
	// facts group by it, so completing one session clears only its own agents.
	SessionID string
	// EndsSession marks a session-terminating event (SessionEnd): when it
	// applies, the reducer ends the session's remaining subagent contributions.
	EndsSession bool
	// SubagentDelta is the balanced-multiset adjustment for identity-less
	// subagent lifecycle events (+1 start, -1 stop) aggregated on a legacy
	// per-session bucket. Identified agents carry 0: their per-agent facts
	// aggregate by source key instead.
	SubagentDelta int

	// Event is the raw hook event name, retained for audit/logging only — never
	// an input to icon logic.
	Event string
	// AgentType is retained for later source-scoped aggregation (session 7). It
	// never affects the icon.
	AgentType string
	// SourceEventID is the upstream event identity, retained so retries can be
	// recognized by later sessions. It does not affect reduction here.
	SourceEventID string

	// Invalid marks a quarantined payload. Reason carries the diagnostic. An
	// invalid event's Action is forced empty so even a mishandled conversion can
	// never produce a terminal state.
	Invalid bool
	Reason  string
}

// Deliverable reports whether the event should fold into the reducer. Invalid
// (quarantined) and inert (unknown/ignored → empty action, no ASL phase) events
// are not deliverable: they must not mutate any source fact.
func (n NormalizedEvent) Deliverable() bool {
	if n.Invalid {
		return false
	}
	return n.Action != "" || n.Phase != ""
}

// ReducerEvent projects the normalized event onto the reducer's fact contract.
// It is only meaningful for deliverable events; callers gate on Deliverable().
func (n NormalizedEvent) ReducerEvent() ReducerEvent {
	return ReducerEvent{
		Source:        n.Source,
		Action:        n.Action,
		Seq:           n.Seq,
		TurnID:        n.TurnID,
		At:            n.At,
		Prompt:        n.Prompt,
		ToolUse:       n.ToolUse,
		RunID:         n.RunID,
		Phase:         n.Phase,
		SessionID:     n.SessionID,
		EndsSession:   n.EndsSession,
		SubagentDelta: n.SubagentDelta,
	}
}

// quarantine builds an inert, invalid event carrying the given reason. Its
// Action is empty and its source kind is the synthetic SourceInvalid so it can
// never be mistaken for a live fact.
func quarantine(event, reason string) NormalizedEvent {
	return NormalizedEvent{
		Source:  SourceID{Kind: SourceInvalid, Key: InvalidPayloadReason},
		Event:   event,
		Invalid: true,
		Reason:  reason,
	}
}

// inert builds a recognized-but-no-effect event (unknown or ignored). It is not
// quarantined (the payload was well-formed); it simply contributes no icon.
func inert(event, reason string) NormalizedEvent {
	return NormalizedEvent{Event: event, Reason: reason}
}

// NormalizedFromHook turns a raw hook delivery into a NormalizedEvent. It is the
// production boundary a later session wires in front of the reducer; today it is
// exercised by tests. The result is a deterministic function of the inputs:
//
//   - Unparseable JSON, or a present-but-malformed numeric identity field, is
//     quarantined as invalid_payload and can never mutate visible state.
//   - An unrecognized or intentionally ignored event is inert (empty action).
//   - Everything else carries a stable source key, the resolved action or ASL
//     phase, and the parsed identity/timing fields.
func NormalizedFromHook(event, harnessName, payloadJSON string, windowID int) NormalizedEvent {
	event = strings.TrimSpace(event)

	payload, err := hookevent.DecodeHookPayload(strings.NewReader(payloadJSON))
	if err != nil {
		return quarantine(event, fmt.Sprintf("%s: %v", InvalidPayloadReason, err))
	}

	if isAgentSessionLoopEvent(event) {
		return normalizeASLEvent(event, payload, windowID)
	}

	if hookevent.IsIgnoredEvent(event, payload) {
		return inert(event, reasonIgnoredEvent)
	}

	action, ok := hookevent.ResolveAction(event, payload)
	if !ok {
		return inert(event, reasonUnknownEvent)
	}

	seq, at, err := parseSourceSeqAt(payload)
	if err != nil {
		return quarantine(event, fmt.Sprintf("%s: %v", InvalidPayloadReason, err))
	}

	// A clean SubagentStop resolves to "active" for the legacy scalar (repaint
	// the bolt once the last agent finishes), but as a source-scoped fact that
	// bolt would strand on the finished subagent's identity forever. Rewrite
	// the clean completion so the subagent's own contribution ends: an
	// identified subagent terminates (retained briefly as an icon-less
	// tombstone so stragglers for the same agent id stay suppressed); the
	// identity-less session bucket falls back to a plain idle. The visible icon
	// then falls back to the highest remaining fact — normally the parent
	// session's own activity. Failure-carrying stops keep their stop_failure.
	if isSubagentStopEvent(event) && action == "active" {
		if payload.EffectiveAgentID() != "" {
			action = "stop"
		} else {
			action = "idle"
		}
	}

	// Identity-less subagent lifecycle events cannot key per-agent facts, so
	// they carry a balanced multiset adjustment for their legacy session
	// bucket instead: starts add an agent, stops (clean or failing) remove
	// one, and the bucket keeps contributing 🤖 while any remain.
	subagentDelta := 0
	if isSubagentEvent(event) && payload.EffectiveAgentID() == "" {
		if isSubagentStopEvent(event) {
			subagentDelta = -1
		} else {
			subagentDelta = 1
		}
	}

	return NormalizedEvent{
		Source:        normalizeSource(event, harnessName, action, payload, windowID),
		Action:        action,
		Seq:           seq,
		TurnID:        payload.EffectiveTurnID(),
		At:            at,
		Prompt:        hookevent.IsPromptEvent(event),
		ToolUse:       payload.EffectiveToolUseID(),
		Event:         event,
		AgentType:     payload.EffectiveAgentType(),
		SourceEventID: payload.EffectiveSourceEventID(),
		SessionID:     payload.EffectiveSessionID(),
		EndsSession:   isSessionEndEvent(event),
		SubagentDelta: subagentDelta,
	}
}

// normalizeASLEvent keys an agent-session-loop event by its run id and maps the
// lifecycle event onto a reducer phase, so each run holds exactly one fact
// ordered by the loop's persisted source sequence. When run_id is absent (older
// ASL events predate the identity schema) it falls back to the origin window so
// the run is still a stable SourceASL bucket rather than being quarantined.
// A paused run additionally carries the attention action derived from its pause
// reason: the reducer renders it as the matching attention icon plus 🔁.
func normalizeASLEvent(event string, payload hookevent.HookPayload, windowID int) NormalizedEvent {
	seq, at, err := parseSourceSeqAt(payload)
	if err != nil {
		return quarantine(event, fmt.Sprintf("%s: %v", InvalidPayloadReason, err))
	}
	runID := payload.EffectiveRunID()
	key := runID
	if key == "" {
		key = windowKey(windowID)
	}
	action := ""
	if event == agentSessionLoopPaused {
		action = aslPausedAttentionAction(payload.EffectivePauseReason())
	}
	return NormalizedEvent{
		Source:        SourceID{Kind: SourceASL, Key: key},
		Action:        action,
		Seq:           seq,
		At:            at,
		RunID:         runID,
		Phase:         aslPhaseForEvent(event),
		TurnID:        payload.EffectiveTurnID(),
		Event:         event,
		SourceEventID: payload.EffectiveSourceEventID(),
	}
}

// aslPhaseForEvent maps ASL lifecycle events onto reducer phases. Started opens
// the run's loop lease without a turn (between); turn start/heartbeat are an
// executing turn; verification start/heartbeat are a running verification; a
// finished turn or verification returns the run to between-turns (the lease
// stays live, no icon); paused awaits a human; finished releases the lease and
// stops.
func aslPhaseForEvent(event string) string {
	switch event {
	case agentSessionLoopStarted:
		return ASLPhaseBetween
	case agentSessionLoopTurnStarted, agentSessionLoopTurnHeartbeat:
		return ASLPhaseTurn
	case agentSessionLoopTurnFinished, agentSessionLoopVerificationFinished:
		return ASLPhaseBetween
	case agentSessionLoopVerificationStarted, agentSessionLoopVerificationHeartbeat:
		return ASLPhaseVerification
	case agentSessionLoopPaused:
		return ASLPhasePaused
	case agentSessionLoopFinished:
		return ASLPhaseFinished
	default:
		return ""
	}
}

// aslPausedAttentionAction maps an ASL pause reason onto the attention action a
// paused run contributes alongside its 🔁 lease. Question-style pauses (the loop
// is waiting for an answer) surface the question icon; every other pause reason
// (verify-failed, blocked, unsafe, malformed-plan, unknown) is a recoverable
// warning — the run is not dead, it is waiting for a human to intervene.
func aslPausedAttentionAction(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "question", "questions", "qna", "elicitation", "clarification", "answer-required":
		return "elicitation"
	default:
		return "failure"
	}
}

// normalizeSource assigns the stable source key for a non-ASL event:
//
//   - Subagent events key by agent id, falling back to a legacy per-session
//     bucket (SourceLegacy) when the agent id is absent so terminal self-heal
//     uses the guard_duration grace rather than same-turn matching.
//   - user_clear is a window-level action; it carries an explicit synthetic
//     source key (its source is ignored by the reducer, but the key keeps traces
//     attributable).
//   - Everything else is the managed harness session, keyed by harness plus
//     session id, falling back to the origin window.
func normalizeSource(event, harnessName, action string, payload hookevent.HookPayload, windowID int) SourceID {
	switch {
	case action == "user_clear":
		return userActionSource()
	case isSubagentEvent(event):
		if agentID := payload.EffectiveAgentID(); agentID != "" {
			return SourceID{Kind: SourceSubagent, Key: agentID}
		}
		return SourceID{Kind: SourceLegacy, Key: legacySubagentBucketKey(sessionOrWindowKey(payload, windowID))}
	default:
		return SourceID{Kind: SourceHarness, Key: harnessSourceKey(harnessName, payload, windowID)}
	}
}

// userActionSource is the explicit synthetic source for window-level user
// actions (F8 user_clear). The reducer ignores the source on a user_clear, so
// this only labels the audit trace; it deliberately reuses SourceProbe (the
// daemon-originated, non-harness bucket) with a distinct key.
func userActionSource() SourceID { return SourceID{Kind: SourceProbe, Key: "user"} }

// ProbeSource is the explicit synthetic source for daemon-side screen probes
// (e.g. the context-limit banner detector). It is exported so the probe path a
// later session wires in can attribute its synthesized events consistently.
func ProbeSource() SourceID { return SourceID{Kind: SourceProbe, Key: "context_limit"} }

// harnessSourceKey builds "harness:session" when a session id is present, and
// falls back to the origin window otherwise.
func harnessSourceKey(harnessName string, payload hookevent.HookPayload, windowID int) string {
	if sid := payload.EffectiveSessionID(); sid != "" {
		return harness.Normalize(harnessName) + ":" + sid
	}
	return windowKey(windowID)
}

// sessionOrWindowKey returns the session id when present, else the window key.
func sessionOrWindowKey(payload hookevent.HookPayload, windowID int) string {
	if sid := payload.EffectiveSessionID(); sid != "" {
		return sid
	}
	return windowKey(windowID)
}

func windowKey(windowID int) string { return "win:" + strconv.Itoa(windowID) }

// legacySubagentBucketPrefix marks a legacy-kind source as an identity-less
// subagent multiset bucket (as opposed to a plain per-window legacy fact),
// so session-end clearing can find the buckets without a separate registry.
const legacySubagentBucketPrefix = "session:"

// legacySubagentBucketKey builds the legacy multiset bucket key for a session
// (or window fallback) identity.
func legacySubagentBucketKey(sessionOrWindow string) string {
	return legacySubagentBucketPrefix + sessionOrWindow
}

// isLegacySubagentBucket reports whether a legacy-kind source key names an
// identity-less subagent multiset bucket.
func isLegacySubagentBucket(key string) bool {
	return strings.HasPrefix(key, legacySubagentBucketPrefix)
}

// isSubagentEvent reports whether the event describes a subagent lifecycle
// transition (start or stop) in either harness dialect.
func isSubagentEvent(event string) bool {
	switch event {
	case "SubagentStart", "subagentStart":
		return true
	default:
		return isSubagentStopEvent(event)
	}
}

// isSubagentStopEvent reports whether the event is a subagent completion in
// either harness dialect.
func isSubagentStopEvent(event string) bool {
	return event == "SubagentStop" || event == "subagentStop"
}

// isSessionEndEvent reports whether the event terminates a whole harness
// session (as opposed to a per-turn Stop) in either dialect.
func isSessionEndEvent(event string) bool {
	return event == "SessionEnd" || event == "sessionEnd"
}

// parseSourceSeqAt parses the per-source sequence and emitted timestamp from
// their string payload fields. Absent fields yield zero (no sequence / no
// timestamp). A present-but-unparseable field returns an error so the caller can
// quarantine: an untrustworthy sequence could otherwise shadow a later real
// event, and this is exactly the case Session 1 requires never reach Apply.
func parseSourceSeqAt(payload hookevent.HookPayload) (seq uint64, at int64, err error) {
	if raw := strings.TrimSpace(payload.EffectiveSourceSequence()); raw != "" {
		v, perr := strconv.ParseUint(raw, 10, 64)
		if perr != nil {
			return 0, 0, fmt.Errorf("source_sequence %q: %w", raw, perr)
		}
		seq = v
	}
	if raw := strings.TrimSpace(payload.EffectiveEmittedTime()); raw != "" {
		v, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil {
			return 0, 0, fmt.Errorf("emitted_time %q: %w", raw, perr)
		}
		at = v
	}
	return seq, at, nil
}
