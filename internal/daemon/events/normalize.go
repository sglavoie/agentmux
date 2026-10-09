// Package events is what one hook delivery is, before anything decides what to
// do about it: the agent-session-loop wire vocabulary and the compatibility
// gate that admits it, the normalization of a raw payload into an
// identity-stamped protocol.NormalizedEvent, the state-update request built
// from that payload, the per-event routing evidence and the outcome and
// identity-confidence classifications read back off it, and the durable session
// event a window retains.
//
// Everything here is pure. The package reads no clock beyond stamping a record,
// performs no Kitty, IPC, or journal I/O, and never imports the daemon root.
// The division with that root is deliberate: this package supplies the
// vocabulary and the evidence, and the root makes the routing decision, owns
// transport dispatch, and acts on it.
package events

// This file introduces the normalized hook-event schema. A raw HookPayload is
// noisy, dialect-specific (snake vs camel), and carries sensitive prompt bodies.
// The reducer must never see any of that: it consumes only NormalizedEvent,
// which is the single, schema-stable front door to Reduce/Apply. Normalization
// parses the identity fields, assigns a stable source key, resolves the action
// (or ASL phase), and quarantines anything malformed so a bad sequence can never
// shadow a later real event or paint a terminal icon.
//
// Normalization is pure: NormalizedFromHook is a deterministic function of its
// inputs and reads no clock, so the same payload always normalizes to the same
// event.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// InvalidPayloadReason labels a quarantined event whose payload could not be
// trusted (unparseable JSON, or a present-but-malformed numeric identity field).
// Quarantined events never reach the fact map, so they cannot mutate the visible
// icon.
const InvalidPayloadReason = "invalid_payload"

const (
	// ReasonUnknownEvent marks an event name the resolver does not recognize.
	// It is inert (empty action), not quarantined: an unknown event simply has
	// no icon effect, matching the live handler's suppression path.
	ReasonUnknownEvent = "unknown_event"
	// ReasonIgnoredEvent marks an event that is recognized but intentionally has
	// no tab-icon effect.
	ReasonIgnoredEvent = "ignored_event"
)

// NormalizedFromHookRequest centralizes request validation and process- and
// runtime-identity stamping for every hook-routing path. NormalizedFromHook stays pure and
// transport-agnostic for reducer tests and replay; this seam adds a complete
// validated identity only to ordinary harness, legacy, and subagent facts.
func NormalizedFromHookRequest(event string, req protocol.Request, windowID int) protocol.NormalizedEvent {
	normalized := NormalizedFromHook(event, req.Harness, req.PayloadJSON, windowID, req.PipeMode)
	if req.Origin != nil {
		normalized.RuntimeSessionID = req.Origin.RuntimeSessionID
	}
	switch normalized.Source.Kind {
	case protocol.SourceHarness, protocol.SourceLegacy, protocol.SourceSubagent:
		if identity := protocol.ValidatedHarnessProcessIdentity(req.Harness, req.HarnessProcess); identity != nil {
			normalized.HarnessProcess = *identity
		}
	}
	return normalized
}

// Quarantine builds an inert, invalid event carrying the given reason. Its
// Action is empty and its source kind is the synthetic protocol.SourceInvalid
// so it can never be mistaken for a live fact.
func Quarantine(event, reason string) protocol.NormalizedEvent {
	return protocol.NormalizedEvent{
		Source:  protocol.SourceID{Kind: protocol.SourceInvalid, Key: InvalidPayloadReason},
		Event:   event,
		Invalid: true,
		Reason:  reason,
	}
}

// Inert builds a recognized-but-no-effect event (unknown or ignored). It is not
// quarantined (the payload was well-formed); it simply contributes no icon.
func Inert(event, reason string) protocol.NormalizedEvent {
	return protocol.NormalizedEvent{Event: event, Reason: reason}
}

// NormalizedFromHook turns a raw hook delivery into a NormalizedEvent. It is
// the production boundary in front of the reducer: every hook delivery path
// (direct hook events, ASL routing, loop lifecycle) normalizes through it
// before reduceStateUpdate. The result is a deterministic function of the
// inputs:
//
//   - Unparseable JSON, or a present-but-malformed numeric identity field, is
//     quarantined as invalid_payload and can never mutate visible state.
//   - An unrecognized or intentionally ignored event is inert (empty action).
//   - Everything else carries a stable source key, the resolved action or ASL
//     phase, and the parsed identity/timing fields.
func NormalizedFromHook(event, harnessName, payloadJSON string, windowID int, pipeMode bool) protocol.NormalizedEvent {
	event = strings.TrimSpace(event)
	harnessName = harness.NormalizeKnown(harnessName)

	payload, err := hookevent.DecodeHookPayload(strings.NewReader(payloadJSON))
	if err != nil {
		return Quarantine(event, fmt.Sprintf("%s: %v", InvalidPayloadReason, err))
	}

	if IsAgentSessionLoopEvent(event) {
		return normalizeASLEvent(event, payload, windowID, pipeMode)
	}

	if hookevent.IsIgnoredEventForHarness(event, payload, harnessName) {
		return Inert(event, ReasonIgnoredEvent)
	}

	action, ok := hookevent.ResolveActionForHarness(event, payload, harnessName)
	if !ok {
		return Inert(event, ReasonUnknownEvent)
	}

	seq, at, err := ParseSourceSeqAt(payload)
	if err != nil {
		return Quarantine(event, fmt.Sprintf("%s: %v", InvalidPayloadReason, err))
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
	if IsSubagentStopEvent(event) && action == "active" {
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
		if IsSubagentStopEvent(event) {
			subagentDelta = -1
		} else {
			subagentDelta = 1
		}
	}

	return protocol.NormalizedEvent{
		Source:         normalizeSource(event, harnessName, action, payload, windowID),
		Action:         action,
		Seq:            seq,
		TurnID:         payload.EffectiveTurnID(),
		At:             at,
		OriginWindowID: windowID,
		Prompt:         hookevent.IsPromptEvent(event),
		ToolUse:        payload.EffectiveToolUseID(),
		PipeMode:       pipeMode,
		Event:          event,
		AgentType:      payload.EffectiveAgentType(),
		SourceEventID:  payload.EffectiveSourceEventID(),
		SessionID:      payload.EffectiveSessionID(),
		EndsSession:    isSessionEndEvent(event),
		SubagentDelta:  subagentDelta,
	}
}

// normalizeASLEvent keys an agent-session-loop event by its run id and maps the
// lifecycle event onto a reducer phase, so each run holds exactly one fact
// ordered by the loop's persisted source sequence. When run_id is absent (older
// ASL events predate the identity schema) it falls back to the origin window so
// the run is still a stable SourceASL bucket rather than being quarantined.
// A paused run additionally carries the attention action derived from its pause
// reason: the reducer renders it as the matching attention icon plus 🔁.
func normalizeASLEvent(event string, payload hookevent.HookPayload, windowID int, pipeMode bool) protocol.NormalizedEvent {
	seq, at, err := ParseSourceSeqAt(payload)
	if err != nil {
		return Quarantine(event, fmt.Sprintf("%s: %v", InvalidPayloadReason, err))
	}
	runID := payload.EffectiveRunID()
	key := runID
	if key == "" {
		key = protocol.WindowKey(windowID)
	}
	action := ""
	if event == AgentSessionLoopPaused {
		action = ASLPausedAttentionAction(payload.EffectivePauseReason())
	}
	return protocol.NormalizedEvent{
		Source:         protocol.SourceID{Kind: protocol.SourceASL, Key: key},
		Action:         action,
		Seq:            seq,
		At:             at,
		OriginWindowID: windowID,
		PipeMode:       pipeMode,
		RunID:          runID,
		Phase:          ASLPhaseForEvent(event),
		TurnID:         payload.EffectiveTurnID(),
		Event:          event,
		SourceEventID:  payload.EffectiveSourceEventID(),
	}
}

// ASLPhaseForEvent maps ASL lifecycle events onto reducer phases. Started opens
// the run's loop lease without a turn (between); turn start/heartbeat are an
// executing turn, and so are a suspected worker hang and its retraction (the
// turn is still live — notify-only suspicion must not drop the lease or repaint
// the icon); verification start/heartbeat are a running verification; a
// finished turn or verification returns the run to between-turns (the lease
// stays live, no icon); a started wait awaits a mailbox reply and an ended wait
// returns to between-turns; paused awaits a human; finished releases the lease
// and stops.
func ASLPhaseForEvent(event string) string {
	switch event {
	case AgentSessionLoopStarted:
		return state.ASLPhaseBetween
	case AgentSessionLoopTurnStarted, AgentSessionLoopTurnHeartbeat,
		AgentSessionLoopTurnHangSuspected, AgentSessionLoopTurnHangCleared:
		return state.ASLPhaseTurn
	case AgentSessionLoopTurnFinished, AgentSessionLoopVerificationFinished:
		return state.ASLPhaseBetween
	case AgentSessionLoopVerificationStarted, AgentSessionLoopVerificationHeartbeat:
		return state.ASLPhaseVerification
	case AgentSessionLoopWaitStarted:
		return state.ASLPhaseWaiting
	case AgentSessionLoopWaitEnded:
		return state.ASLPhaseBetween
	case AgentSessionLoopPaused:
		return state.ASLPhasePaused
	case AgentSessionLoopFinished:
		return state.ASLPhaseFinished
	default:
		return ""
	}
}

// ASLPausedAttentionAction maps an ASL pause reason onto the attention action a
// paused run contributes alongside its 🔁 lease. Question-style pauses (the loop
// is waiting for an answer) surface the question icon; every other pause reason
// (verify-failed, blocked, unsafe, malformed-plan, unknown) is a recoverable
// warning — the run is not dead, it is waiting for a human to intervene.
func ASLPausedAttentionAction(reason string) string {
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
//   - user_active is the operator's forced ⚡; it keys its own synthetic source
//     so the next real event can retire exactly that fact.
//   - Everything else is the managed harness session, keyed by harness plus
//     session id, falling back to the origin window.
func normalizeSource(event, harnessName, action string, payload hookevent.HookPayload, windowID int) protocol.SourceID {
	switch {
	case action == "user_clear":
		return protocol.UserActionSource()
	case action == "user_active":
		return protocol.UserActiveSource()
	case harness.NormalizeKnown(harnessName) == "":
		// Missing and unsupported producers must not acquire a harness identity
		// from payload session data or from configuration's default-to-Claude
		// normalization. Keep them in the legacy per-window bucket.
		return protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.WindowKey(windowID)}
	case isSubagentEvent(event):
		if agentID := payload.EffectiveAgentID(); agentID != "" {
			return protocol.SourceID{Kind: protocol.SourceSubagent, Key: agentID}
		}
		return protocol.SourceID{Kind: protocol.SourceLegacy, Key: protocol.LegacySubagentBucketKey(sessionOrWindowKey(payload, windowID))}
	default:
		return protocol.SourceID{Kind: protocol.SourceHarness, Key: harnessSourceKey(harnessName, payload, windowID)}
	}
}

// harnessSourceKey builds "harness:session" when a session id is present, and
// falls back to the origin window otherwise.
func harnessSourceKey(harnessName string, payload hookevent.HookPayload, windowID int) string {
	if known := harness.NormalizeKnown(harnessName); known != "" {
		if sid := payload.EffectiveSessionID(); sid != "" {
			return known + ":" + sid
		}
	}
	return protocol.WindowKey(windowID)
}

// sessionOrWindowKey returns the session id when present, else the window key.
func sessionOrWindowKey(payload hookevent.HookPayload, windowID int) string {
	if sid := payload.EffectiveSessionID(); sid != "" {
		return sid
	}
	return protocol.WindowKey(windowID)
}

// isSubagentEvent reports whether the event describes a subagent lifecycle
// transition (start or stop) in either harness dialect.
func isSubagentEvent(event string) bool {
	switch event {
	case "SubagentStart", "subagentStart":
		return true
	default:
		return IsSubagentStopEvent(event)
	}
}

// IsSubagentStopEvent reports whether the event is a subagent completion in
// either harness dialect.
func IsSubagentStopEvent(event string) bool {
	return event == "SubagentStop" || event == "subagentStop"
}

// isSessionEndEvent reports whether the event terminates a whole harness
// session (as opposed to a per-turn Stop) in either dialect.
func isSessionEndEvent(event string) bool {
	return event == "SessionEnd" || event == "sessionEnd"
}

// ParseSourceSeqAt parses the per-source sequence and emitted timestamp from
// their string payload fields. Absent fields yield zero (no sequence / no
// timestamp). A present-but-unparseable field returns an error so the caller can
// quarantine: an untrustworthy sequence could otherwise shadow a later real
// event, which must never reach Apply.
func ParseSourceSeqAt(payload hookevent.HookPayload) (seq uint64, at int64, err error) {
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

// NormalizeProducerFields makes the additive ProducerHarness field
// authoritative when present while keeping the legacy Harness field usable by
// every existing handler path.
func NormalizeProducerFields(req protocol.Request) protocol.Request {
	if producer := strings.TrimSpace(req.ProducerHarness); producer != "" {
		req.Harness = producer
	} else {
		req.ProducerHarness = req.Harness
	}
	req.HarnessProcess = protocol.ValidatedHarnessProcessIdentity(req.Harness, req.HarnessProcess)
	return req
}
