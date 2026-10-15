package eventlog

import (
	"encoding/json"
	"fmt"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// decisionRecord is the journal record name carried by every persisted row.
// It is a consumer contract: `amx debug` selectors and bundles name it, so it
// must not be renamed without migrating those readers. It stays unexported
// because those readers match the persisted stage as a literal on immutable
// artifacts rather than importing the writer.
const decisionRecord = "daemon.decision"

// DecisionEnvelope projects one finalized row into the durable journal record
// that outlives the in-memory ring. The evidence map is the persisted format:
// its keys are read back by the offline explain, bundle and replay surfaces in
// internal/debugger, so key names and value encodings are a compatibility
// contract rather than a formatting choice.
//
// It is a pure projection. Appending the record — with the fail-open timeout
// that keeps a slow journal off the hook path — stays with the daemon root,
// which owns the journal handle.
func DecisionEnvelope(event protocol.SessionEvent) diagnostics.TraceEnvelope {
	envelope := diagnostics.NewEnvelope(decisionRecord)
	if event.TraceID != "" {
		envelope.TraceID = event.TraceID
	}
	envelope.RequestID = event.RequestID
	envelope.PayloadSize = event.PayloadSize
	envelope.PayloadHMAC = event.PayloadHMAC
	envelope.Harness = event.ProducerHarness
	envelope.Profile = event.Profile
	envelope.SessionID = event.SessionID
	envelope.WindowID = event.TargetWindowID
	envelope.RunID = event.RunID
	envelope.Outcome = string(event.ReducerDecision)
	envelope.Reason = event.DecisionReason
	envelope.Evidence = map[string]string{
		"event":                  event.Event,
		"action":                 event.Action,
		"reducer_action":         event.ReducerInputAction,
		"source_event_id":        event.SourceEventID,
		"parse_outcome":          event.ParseOutcome,
		"normalization_outcome":  event.NormalizationOutcome,
		"identity_confidence":    event.IdentityConfidence,
		"source_kind":            event.SourceKind,
		"source_key":             event.SourceKey,
		"source_sequence":        fmt.Sprint(event.SourceSeq),
		"daemon_sequence":        fmt.Sprint(event.DaemonSeq),
		"render_generation":      fmt.Sprint(event.RenderGeneration),
		"turn_id":                event.TurnID,
		"tool_use_id":            event.ToolUseID,
		"reducer_reason":         event.ReducerReason,
		"producer_config_dir":    event.ProducerConfigDir,
		"effective_before":       formatEffectiveEvidence(event.EffectiveBefore),
		"effective_after":        formatEffectiveEvidence(event.EffectiveAfter),
		"used_fallback_identity": fmt.Sprint(event.UsedFallbackIdentity),
		"wire_encoding":          event.PayloadEncoding,
		"saturation":             eventSaturationReason(event),
	}
	appendReplayJournalEvidence(envelope.Evidence, event)
	if event.Origin != nil && event.Origin.RuntimeSessionID != "" {
		envelope.Evidence["runtime_session_id"] = event.Origin.RuntimeSessionID
	}
	appendRoutingJournalEvidence(envelope.Evidence, event.RouteEvidence)
	if event.Attempt > 0 {
		envelope.Evidence["attempt"] = fmt.Sprint(event.Attempt)
	}
	return envelope
}

// appendReplayJournalEvidence writes the replay inputs an offline reducer needs
// to re-derive this row's decision from the record alone. The replay.version
// key pins the shape internal/debugger decodes.
func appendReplayJournalEvidence(out map[string]string, event protocol.SessionEvent) {
	if out == nil {
		return
	}
	out["replay.version"] = "1"
	appendReplayStateEvidence(out, "before", event.EffectiveBefore)
	appendReplayStateEvidence(out, "after", event.EffectiveAfter)
	if input := event.ReducerInput; input != nil {
		out["replay.emitted_at"] = fmt.Sprint(input.EmittedAt)
		out["replay.origin_window_id"] = fmt.Sprint(input.OriginWindowID)
		out["replay.is_user_submission"] = fmt.Sprint(input.Prompt)
		out["replay.pipe_mode"] = fmt.Sprint(input.PipeMode)
		out["replay.asl_phase"] = input.ASLPhase
		out["replay.ends_session"] = fmt.Sprint(input.EndsSession)
		out["replay.subagent_delta"] = fmt.Sprint(input.SubagentDelta)
		out["replay.guard_duration"] = fmt.Sprint(input.GuardDuration)
		out["replay.stale_active"] = fmt.Sprint(input.StaleActive)
		out["replay.stale_subagent"] = fmt.Sprint(input.StaleSubagent)
		out["replay.stale_compact"] = fmt.Sprint(input.StaleCompact)
	}
}

func appendReplayStateEvidence(out map[string]string, prefix string, evidence *protocol.EffectiveStateEvidence) {
	if evidence == nil {
		return
	}
	out["replay."+prefix+"_icon"] = evidence.Icon
	out["replay."+prefix+"_looping"] = fmt.Sprint(evidence.Looping)
	out["replay."+prefix+"_source_kind"] = evidence.SourceKind
	out["replay."+prefix+"_source_key"] = evidence.SourceKey
}

func eventSaturationReason(event protocol.SessionEvent) string {
	return event.SaturationReason
}

func formatEffectiveEvidence(evidence *protocol.EffectiveStateEvidence) string {
	if evidence == nil {
		return ""
	}
	data, _ := json.Marshal(evidence)
	return string(data)
}

func appendRoutingJournalEvidence(out map[string]string, evidence *protocol.RoutingEvidence) {
	if out == nil || evidence == nil {
		return
	}
	out["route.snapshot_ready"] = fmt.Sprint(evidence.SnapshotReady)
	out["route.lookup_outcome"] = evidence.LookupOutcome
	out["route.source_window_id"] = fmt.Sprint(evidence.SourceWindowID)
	out["route.target_window_id"] = fmt.Sprint(evidence.TargetWindowID)
	out["route.effective_harness"] = evidence.EffectiveHarness
	out["route.effective_profile"] = evidence.EffectiveProfile
	out["route.source_matched"] = fmt.Sprint(evidence.SourceMatched)
	out["route.owner_matched"] = fmt.Sprint(evidence.OwnerMatched)
	out["route.focus_candidate"] = fmt.Sprint(evidence.FocusCandidate)
	out["route.prompt_candidate"] = fmt.Sprint(evidence.PromptCandidate)
	out["route.sticky_matches"] = fmt.Sprint(evidence.StickyMatches)
	out["route.asl_matches"] = fmt.Sprint(evidence.ASLMatches)
	out["route.selection_reason"] = evidence.SelectionReason
	out["route.chosen"] = evidence.ChosenRoute
	out["route.considered_count"] = fmt.Sprint(evidence.ConsideredWindowCount)
	for i, candidate := range evidence.ConsideredWindows {
		data, _ := json.Marshal(candidate)
		out[fmt.Sprintf("route.candidate.%02d", i)] = string(data)
	}
	for i, rejected := range evidence.RejectedAlternatives {
		data, _ := json.Marshal(rejected)
		out[fmt.Sprintf("route.rejected.%02d", i)] = string(data)
	}
}
