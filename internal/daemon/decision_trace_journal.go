package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func (h *Handler) setDecisionJournal(journal *diagnostics.Journal) {
	h.decisionJournal = journal
}

func (h *Handler) recordDecisionEvent(event protocol.SessionEvent) {
	if h == nil || h.decisionJournal == nil {
		return
	}
	envelope := diagnostics.NewEnvelope("daemon.decision")
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
	appendRoutingJournalEvidence(envelope.Evidence, event.RouteEvidence)
	if event.Attempt > 0 {
		envelope.Evidence["attempt"] = fmt.Sprint(event.Attempt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}

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
	if evidence == nil {
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

func stableReducerReason(trace ReducerTrace) string {
	if trace.Reason == "" {
		return string(trace.Decision)
	}
	switch trace.Reason {
	case "duplicate sequence":
		return protocol.DecisionReasonDuplicateSequence
	case "older sequence arrived after a newer one":
		return protocol.DecisionReasonReorderedSequence
	case "idle preserves attention/terminal icon":
		return protocol.DecisionReasonIdlePreservesTerminal
	case "same identified turn after terminal":
		return protocol.DecisionReasonTerminalTurnSuppression
	case "stopped subagent never restarts", "same-source same-tool terminal suppression", "same-source same-turn terminal suppression":
		return protocol.DecisionReasonTerminalTurnSuppression
	case "legacy terminal grace window":
		return protocol.DecisionReasonTerminalGraceSuppression
	case "terminal trailing-event grace":
		return protocol.DecisionReasonTerminalGraceSuppression
	case "user acknowledged visible facts; ASL leases preserved":
		return protocol.DecisionReasonUserAcknowledged
	case "session end cleared its remaining subagent facts":
		return protocol.DecisionReasonSessionAgentsCleared
	default:
		return strings.TrimSpace(trace.Reason)
	}
}
