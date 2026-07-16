package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// Decision reason constants are the stable vocabulary shared by daemon traces
// and the explanation service. Keep these values machine-oriented: callers
// must branch on them rather than parsing human log messages.
const (
	DecisionReasonAccepted                  = "accepted"
	DecisionReasonMissingEvent              = "missing_event"
	DecisionReasonInvalidPayload            = "invalid_payload"
	DecisionReasonUnknownEvent              = "unknown_event"
	DecisionReasonIgnoredEvent              = "ignored_event"
	DecisionReasonGuardBlocked              = "guard_blocked"
	DecisionReasonSubagentStillRunning      = "subagent_still_running"
	DecisionReasonCopilotMidSubagent        = "copilot_mid_subagent"
	DecisionReasonCopilotPendingPrompt      = "copilot_pending_prompt"
	DecisionReasonActiveAfterTerminal       = "active_after_terminal"
	DecisionReasonElicitationPending        = "elicitation_pending"
	DecisionReasonAutoApprovedPermission    = "auto_approved_permission"
	DecisionReasonOwnerMismatch             = "owner_mismatch"
	DecisionReasonPreservesIcon             = "preserves_icon"
	DecisionReasonReducerBypassed           = "reducer_bypassed"
	DecisionReasonResetFlowRewrite          = "reset_flow_rewrite"
	DecisionReasonUntrustedIdentityFallback = "untrusted_identity_fallback"
	DecisionReasonRequestReplayed           = "request_replayed"
	DecisionReasonDuplicateSequence         = "duplicate_sequence"
	DecisionReasonReorderedSequence         = "reordered_sequence"
	DecisionReasonIdlePreservesTerminal     = "idle_preserves_terminal"
	DecisionReasonTerminalTurnSuppression   = "terminal_turn_suppression"
	DecisionReasonTerminalGraceSuppression  = "terminal_grace_suppression"
	DecisionReasonUserAcknowledged          = "user_acknowledged"
	DecisionReasonSessionAgentsCleared      = "session_agents_cleared"
	DecisionReasonNotSelected               = "not_selected"
)

const (
	ParseOutcomeDecoded      = "decoded"
	ParseOutcomeInvalid      = "invalid"
	ParseOutcomeMissingEvent = "missing_event"
	ParseOutcomeSynthetic    = "synthetic"

	NormalizationOutcomeNormalized        = "normalized"
	NormalizationOutcomeIgnored           = "ignored"
	NormalizationOutcomeUnknown           = "unknown"
	NormalizationOutcomeQuarantined       = "quarantined"
	NormalizationOutcomeLegacySynthesized = "legacy_synthesized"
	NormalizationOutcomeUntrustedFallback = "quarantined_legacy_fallback"
	NormalizationOutcomeReducerRewrite    = "reducer_rewrite"
	NormalizationOutcomeNotApplicable     = "not_applicable"

	IdentityConfidenceDeclared          = "declared"
	IdentityConfidenceDaemon            = "daemon"
	IdentityConfidenceLegacySynthesized = "legacy_synthesized"
	IdentityConfidenceUntrustedFallback = "untrusted_synthesized"
	IdentityConfidenceInvalid           = "invalid"
	IdentityConfidenceUnresolved        = "unresolved"
)

const (
	routeDecisionRejected = "rejected"

	routeLookupRequestOnly         = "request_only"
	routeLookupSnapshotUnavailable = "snapshot_unavailable"
	routeLookupSourceNotFound      = "source_not_found"
	routeLookupSourceFound         = "source_found"
)

// DecisionBypassed is used when the harness-quirk layer intentionally handles
// an event without admitting a fact to the reducer (for example a
// preserve-current child completion). It is distinct from a reducer
// suppression, duplicate, or quarantine.
const DecisionBypassed ReducerDecision = "bypassed"

// EffectiveStateEvidence is the sanitized before/after reducer projection.
// It contains only state-machine identity and never carries hook payload text.
type EffectiveStateEvidence struct {
	Icon       string `json:"icon"`
	Looping    bool   `json:"looping"`
	SourceKind string `json:"source_kind,omitempty"`
	SourceKey  string `json:"source_key,omitempty"`
}

// RoutingWindowEvidence describes one Kitty pane considered by routing. Only
// stable identity and management metadata are retained; titles, directories,
// commands, prompts, and tool arguments are intentionally excluded.
type RoutingWindowEvidence struct {
	WindowID int    `json:"window_id"`
	Harness  string `json:"harness,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Managed  bool   `json:"managed"`
	Active   bool   `json:"active"`
	Source   bool   `json:"source,omitempty"`
	Owner    bool   `json:"owner,omitempty"`
}

// RejectedRouteEvidence identifies an alternative target and the stable reason
// it was not selected.
type RejectedRouteEvidence struct {
	WindowID int    `json:"window_id,omitempty"`
	Reason   string `json:"reason"`
}

// RoutingEvidence is the complete sanitized route decision attached to a
// SessionEvent. The candidate list is bounded so one event cannot make the
// in-memory ring or journal record grow with an unbounded Kitty snapshot.
type RoutingEvidence struct {
	SnapshotReady         bool                    `json:"snapshot_ready"`
	LookupOutcome         string                  `json:"lookup_outcome"`
	SourceWindowID        int                     `json:"source_window_id,omitempty"`
	TargetWindowID        int                     `json:"target_window_id,omitempty"`
	EffectiveHarness      string                  `json:"effective_harness,omitempty"`
	EffectiveProfile      string                  `json:"effective_profile,omitempty"`
	SourceMatched         bool                    `json:"source_matched"`
	OwnerMatched          bool                    `json:"owner_matched"`
	FocusCandidate        bool                    `json:"focus_candidate"`
	PromptCandidate       bool                    `json:"prompt_candidate"`
	StickyMatches         int                     `json:"sticky_matches,omitempty"`
	ASLMatches            int                     `json:"asl_matches,omitempty"`
	SelectionReason       string                  `json:"selection_reason,omitempty"`
	ChosenRoute           string                  `json:"chosen_route,omitempty"`
	ConsideredWindowCount int                     `json:"considered_window_count"`
	ConsideredWindows     []RoutingWindowEvidence `json:"considered_windows,omitempty"`
	RejectedAlternatives  []RejectedRouteEvidence `json:"rejected_alternatives,omitempty"`
}

const maxRoutingEvidenceWindows = 16

type sessionEventReference struct {
	daemonSeq uint64
}

func ensureRequestTraceID(req Request) Request {
	if (req.Type == TypeHookEvent || req.Type == TypeStateUpdate) && req.TraceID == "" {
		req.TraceID = diagnostics.NewTraceID()
	}
	return req
}

func effectiveStateEvidence(state EffectiveState) EffectiveStateEvidence {
	return EffectiveStateEvidence{
		Icon: state.Icon, Looping: state.Looping,
		SourceKind: string(state.Source.Kind), SourceKey: state.Source.Key,
	}
}

func cloneRoutingEvidence(in *RoutingEvidence) *RoutingEvidence {
	if in == nil {
		return nil
	}
	out := *in
	out.ConsideredWindows = append([]RoutingWindowEvidence(nil), in.ConsideredWindows...)
	out.RejectedAlternatives = append([]RejectedRouteEvidence(nil), in.RejectedAlternatives...)
	return &out
}

func cloneSessionEvent(in SessionEvent) SessionEvent {
	out := in
	out.RouteEvidence = cloneRoutingEvidence(in.RouteEvidence)
	out.ProbeTraceIDs = append([]string(nil), in.ProbeTraceIDs...)
	out.ProcessSources = append([]ProcessLifecycleSourceEvidence(nil), in.ProcessSources...)
	if in.WithdrawnFacts != nil {
		value := *in.WithdrawnFacts
		out.WithdrawnFacts = &value
	}
	if in.ClearedSubagents != nil {
		value := *in.ClearedSubagents
		out.ClearedSubagents = &value
	}
	if in.SynthesizedStop != nil {
		value := *in.SynthesizedStop
		out.SynthesizedStop = &value
	}
	if in.ReducerInput != nil {
		input := *in.ReducerInput
		out.ReducerInput = &input
	}
	if in.EffectiveBefore != nil {
		before := *in.EffectiveBefore
		out.EffectiveBefore = &before
	}
	if in.EffectiveAfter != nil {
		after := *in.EffectiveAfter
		out.EffectiveAfter = &after
	}
	return out
}

func newRoutingEvidence(sourceWindowID int) *RoutingEvidence {
	return &RoutingEvidence{
		LookupOutcome:  routeLookupRequestOnly,
		SourceWindowID: sourceWindowID,
	}
}

func populateRoutingCandidates(evidence *RoutingEvidence, tab *kitty.Tab, source, owner *kitty.Window) {
	if evidence == nil || tab == nil {
		return
	}
	evidence.ConsideredWindowCount = len(tab.Windows)
	for i := range tab.Windows {
		if len(evidence.ConsideredWindows) >= maxRoutingEvidenceWindows {
			break
		}
		win := &tab.Windows[i]
		profile := win.UserVars.EffectiveProfile()
		harnessName := harness.NormalizeKnown(win.UserVars.EffectiveHarness())
		evidence.ConsideredWindows = append(evidence.ConsideredWindows, RoutingWindowEvidence{
			WindowID: win.ID,
			Harness:  harnessName,
			Profile:  profile,
			Managed:  profile != "" || harnessName != "",
			Active:   win.IsActive,
			Source:   source != nil && win.ID == source.ID,
			Owner:    owner != nil && win.ID == owner.ID,
		})
	}
}

func chooseRoute(evidence *RoutingEvidence, decision, selectionReason string, target *kitty.Window) {
	if evidence == nil {
		return
	}
	evidence.ChosenRoute = decision
	evidence.SelectionReason = selectionReason
	if target == nil {
		if decision == routeDecisionSource || decision == routeDecisionSourceFallback {
			evidence.TargetWindowID = evidence.SourceWindowID
		}
		return
	}
	evidence.TargetWindowID = target.ID
	evidence.EffectiveHarness = harness.NormalizeKnown(target.UserVars.EffectiveHarness())
	evidence.EffectiveProfile = target.UserVars.EffectiveProfile()
	evidence.OwnerMatched = evidence.TargetWindowID > 0 && decision == routeDecisionOwner
	for _, candidate := range evidence.ConsideredWindows {
		if candidate.WindowID == target.ID {
			continue
		}
		evidence.RejectedAlternatives = append(evidence.RejectedAlternatives, RejectedRouteEvidence{
			WindowID: candidate.WindowID,
			Reason:   DecisionReasonNotSelected,
		})
	}
}

func routingWindowByID(tab *kitty.Tab, windowID int) *kitty.Window {
	if tab == nil || windowID <= 0 {
		return nil
	}
	for i := range tab.Windows {
		if tab.Windows[i].ID == windowID {
			return &tab.Windows[i]
		}
	}
	return nil
}

func normalizationOutcomeFor(n NormalizedEvent) string {
	switch {
	case n.Invalid:
		return NormalizationOutcomeQuarantined
	case n.Reason == reasonIgnoredEvent:
		return NormalizationOutcomeIgnored
	case n.Reason == reasonUnknownEvent:
		return NormalizationOutcomeUnknown
	default:
		return NormalizationOutcomeNormalized
	}
}

func identityConfidenceFor(n NormalizedEvent) string {
	switch n.Source.Kind {
	case "":
		return IdentityConfidenceUnresolved
	case SourceInvalid:
		return IdentityConfidenceInvalid
	case SourceLegacy:
		return IdentityConfidenceLegacySynthesized
	case SourceProbe:
		return IdentityConfidenceDaemon
	default:
		return IdentityConfidenceDeclared
	}
}

func propagateIngressTrace(dst *Request, src Request) {
	dst.TraceID = src.TraceID
	dst.RequestID = src.RequestID
	dst.SourceEventID = src.SourceEventID
	dst.Attempt = src.Attempt
	dst.PayloadSize = src.PayloadSize
	dst.PayloadHMAC = src.PayloadHMAC
	dst.PayloadEncoding = src.PayloadEncoding
	dst.PayloadCaptured = src.PayloadCaptured
	dst.ProducerHarness = src.ProducerHarness
	if src.HarnessProcess != nil {
		identity := *src.HarnessProcess
		dst.HarnessProcess = &identity
	}
	dst.parseOutcome = src.parseOutcome
	dst.normalizationOutcome = src.normalizationOutcome
	dst.routeEvidence = cloneRoutingEvidence(src.routeEvidence)
	dst.saturationReason = src.saturationReason
}

func (h *Handler) setDecisionJournal(journal *diagnostics.Journal) {
	h.decisionJournal = journal
}

func (h *Handler) recordDecisionEvent(event SessionEvent) {
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

func appendReplayJournalEvidence(out map[string]string, event SessionEvent) {
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

func appendReplayStateEvidence(out map[string]string, prefix string, evidence *EffectiveStateEvidence) {
	if evidence == nil {
		return
	}
	out["replay."+prefix+"_icon"] = evidence.Icon
	out["replay."+prefix+"_looping"] = fmt.Sprint(evidence.Looping)
	out["replay."+prefix+"_source_kind"] = evidence.SourceKind
	out["replay."+prefix+"_source_key"] = evidence.SourceKey
}

func eventSaturationReason(event SessionEvent) string {
	return event.SaturationReason
}

func formatEffectiveEvidence(evidence *EffectiveStateEvidence) string {
	if evidence == nil {
		return ""
	}
	data, _ := json.Marshal(evidence)
	return string(data)
}

func appendRoutingJournalEvidence(out map[string]string, evidence *RoutingEvidence) {
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
		return DecisionReasonDuplicateSequence
	case "older sequence arrived after a newer one":
		return DecisionReasonReorderedSequence
	case "idle preserves attention/terminal icon":
		return DecisionReasonIdlePreservesTerminal
	case "same identified turn after terminal":
		return DecisionReasonTerminalTurnSuppression
	case "stopped subagent never restarts", "same-source same-tool terminal suppression", "same-source same-turn terminal suppression":
		return DecisionReasonTerminalTurnSuppression
	case "legacy terminal grace window":
		return DecisionReasonTerminalGraceSuppression
	case "terminal trailing-event grace":
		return DecisionReasonTerminalGraceSuppression
	case "user acknowledged visible facts; ASL leases preserved":
		return DecisionReasonUserAcknowledged
	case "session end cleared its remaining subagent facts":
		return DecisionReasonSessionAgentsCleared
	default:
		return strings.TrimSpace(trace.Reason)
	}
}

func (h *Handler) effectiveStateForTrace(windowID int) EffectiveStateEvidence {
	if h == nil || windowID <= 0 {
		return EffectiveStateEvidence{}
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if st, ok := h.windowStateLocked(windowID); ok {
		return effectiveStateEvidence(st.Effective)
	}
	return EffectiveStateEvidence{}
}

func eventByReferenceLocked(st *WindowState, ref *sessionEventReference) *SessionEvent {
	if st == nil || ref == nil || ref.daemonSeq == 0 {
		return nil
	}
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if st.EventLog[i].DaemonSeq == ref.daemonSeq {
			return &st.EventLog[i]
		}
	}
	return nil
}

func ensureDirectRouteEvidence(req Request) *RoutingEvidence {
	if req.routeEvidence != nil {
		evidence := cloneRoutingEvidence(req.routeEvidence)
		if evidence.TargetWindowID == 0 && evidence.ChosenRoute != routeDecisionRejected {
			evidence.TargetWindowID = req.WindowID
		}
		if evidence.EffectiveHarness == "" {
			evidence.EffectiveHarness = harness.NormalizeKnown(req.targetHarness)
			if evidence.EffectiveHarness == "" {
				evidence.EffectiveHarness = harness.NormalizeKnown(req.Harness)
			}
		}
		if evidence.EffectiveProfile == "" {
			evidence.EffectiveProfile = req.Account
		}
		return evidence
	}
	evidence := newRoutingEvidence(req.WindowID)
	evidence.TargetWindowID = req.WindowID
	evidence.EffectiveHarness = harness.NormalizeKnown(req.targetHarness)
	if evidence.EffectiveHarness == "" {
		evidence.EffectiveHarness = harness.NormalizeKnown(req.Harness)
	}
	evidence.EffectiveProfile = req.Account
	evidence.ChosenRoute = routeDecisionSource
	evidence.SelectionReason = "direct"
	return evidence
}

func (h *Handler) finalizeBypassedStateUpdate(req Request, outcome applyOutcome, before EffectiveStateEvidence) {
	if req.WindowID <= 0 {
		return
	}
	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(req.WindowID)
	event := eventByReferenceLocked(st, req.eventRef)
	if event == nil {
		fallback := sessionEventFromRequest(req, outcome.reduceAction, IconStatePreserve, desiredIconForState(st))
		fallback.Suppressed = true
		fallback.SuppressionReason = outcome.skipReason
		appended := h.appendEventLocked(req.WindowID, fallback)
		event = eventByReferenceLocked(st, req.eventRef)
		if event == nil {
			event = &appended
		}
	}
	after := effectiveStateEvidence(st.Effective)
	event.TraceID = req.TraceID
	event.RequestID = req.RequestID
	event.SourceEventID = firstNonEmpty(req.SourceEventID, event.SourceEventID)
	event.Attempt = req.Attempt
	event.ReducerDecision = DecisionBypassed
	if outcome.skipReason == "" {
		outcome.skipReason = DecisionReasonReducerBypassed
	}
	event.DecisionReason = outcome.skipReason
	event.ReducerReason = outcome.skipReason
	event.ReducerInputAction = outcome.reduceAction
	event.EffectiveBefore = &before
	event.EffectiveAfter = &after
	event.RouteEvidence = ensureDirectRouteEvidence(req)
	event.TargetWindowID = req.WindowID
	if event.IdentityConfidence == "" && req.normalized != nil {
		event.IdentityConfidence = identityConfidenceFor(*req.normalized)
	}
	completed := cloneSessionEvent(*event)
	h.stateMu.Unlock()
	h.recordDecisionEvent(completed)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func suppressionReasonForReducer(trace ReducerTrace, outcome applyOutcome) string {
	switch trace.Decision {
	case DecisionDuplicate:
		return DecisionReasonDuplicateSequence
	case DecisionReordered:
		return DecisionReasonReorderedSequence
	case DecisionQuarantined:
		reason := stableReducerReason(trace)
		if strings.HasPrefix(reason, DecisionReasonInvalidPayload) {
			return DecisionReasonInvalidPayload
		}
		return reason
	case DecisionSuppressed:
		stable := stableReducerReason(trace)
		switch stable {
		case DecisionReasonTerminalTurnSuppression, DecisionReasonTerminalGraceSuppression:
			return DecisionReasonActiveAfterTerminal
		default:
			return stable
		}
	default:
		if outcome.guardBlocked {
			return DecisionReasonGuardBlocked
		}
		return ""
	}
}

// finalizeReducerDecisionLocked enriches the exact event row appended by the
// legacy compatibility layer. When that layer appended nothing (for example a
// guard refusal), it creates the one authoritative row here. Caller holds
// stateMu.
func (h *Handler) finalizeReducerDecisionLocked(
	req Request,
	n NormalizedEvent,
	trace ReducerTrace,
	before, after EffectiveState,
	identityConfidence string,
	usedFallbackIdentity bool,
	outcome applyOutcome,
) SessionEvent {
	st := h.ensureWindowStateLocked(req.WindowID)
	event := eventByReferenceLocked(st, req.eventRef)
	if event == nil {
		fallback := sessionEventFromRequest(req, n.Action, IconStatePreserve, desiredIconForState(st))
		appended := h.appendEventLocked(req.WindowID, fallback)
		event = eventByReferenceLocked(st, req.eventRef)
		if event == nil {
			event = &appended
		}
	}

	reason := suppressionReasonForReducer(trace, outcome)
	wasSuppressed := event.Suppressed
	if reason != "" {
		event.Suppressed = true
		event.SuppressionReason = reason
	}
	if event.Suppressed && !wasSuppressed && event.SuppressionReason != "" {
		meta := h.windowMetadata[req.WindowID]
		if meta.Stats.SuppressionReasons == nil {
			meta.Stats.SuppressionReasons = make(map[string]int)
		}
		meta.Stats.SuppressionReasons[event.SuppressionReason]++
		h.windowMetadata[req.WindowID] = meta
	}

	event.TraceID = req.TraceID
	event.RequestID = req.RequestID
	event.SourceEventID = firstNonEmpty(req.SourceEventID, n.SourceEventID, event.SourceEventID)
	event.Attempt = req.Attempt
	event.ParseOutcome = firstNonEmpty(req.parseOutcome, event.ParseOutcome, ParseOutcomeSynthetic)
	switch {
	case usedFallbackIdentity:
		event.NormalizationOutcome = NormalizationOutcomeUntrustedFallback
	case req.normalized == nil:
		event.NormalizationOutcome = NormalizationOutcomeLegacySynthesized
	case outcome.reduceAction != "" && req.normalized.Action != "" && outcome.reduceAction != req.normalized.Action:
		event.NormalizationOutcome = NormalizationOutcomeReducerRewrite
	default:
		event.NormalizationOutcome = firstNonEmpty(req.normalizationOutcome, normalizationOutcomeFor(n))
	}
	event.IdentityConfidence = identityConfidence
	event.UsedFallbackIdentity = usedFallbackIdentity
	event.ProducerHarness = req.Harness
	event.TargetHarness = req.targetHarness
	if !req.targetHarnessSet {
		event.TargetHarness = req.Harness
	}
	event.Profile = req.Account
	event.SessionID = n.SessionID
	event.RunID = n.RunID
	event.TargetWindowID = req.WindowID
	event.SourceKind = string(n.Source.Kind)
	event.SourceKey = n.Source.Key
	event.SourceSeq = n.Seq
	if n.OriginWindowID > 0 {
		event.OriginWindowID = n.OriginWindowID
	}
	event.ReducerInputAction = n.Action
	event.ReducerInput = &ReducerInputEvidence{
		EmittedAt: n.At, OriginWindowID: n.OriginWindowID, Prompt: n.Prompt,
		PipeMode: n.PipeMode, ASLPhase: n.Phase, EndsSession: n.EndsSession,
		SubagentDelta: n.SubagentDelta, GuardDuration: h.reducerCfg.GuardDuration,
		StaleActive: h.reducerCfg.StaleActive, StaleSubagent: h.reducerCfg.StaleSubagent,
		StaleCompact: h.reducerCfg.StaleCompact,
	}
	event.ReducerDecision = trace.Decision
	event.ReducerReason = stableReducerReason(trace)
	event.DecisionReason = event.ReducerReason
	if usedFallbackIdentity {
		event.DecisionReason = DecisionReasonUntrustedIdentityFallback
	}
	event.Quarantined = trace.Decision == DecisionQuarantined || usedFallbackIdentity ||
		(req.normalized != nil && req.normalized.Invalid)
	beforeEvidence := effectiveStateEvidence(before)
	afterEvidence := effectiveStateEvidence(after)
	event.EffectiveBefore = &beforeEvidence
	event.EffectiveAfter = &afterEvidence
	event.RouteEvidence = ensureDirectRouteEvidence(req)
	if event.RouteDecision == "" {
		event.RouteDecision = event.RouteEvidence.ChosenRoute
	}
	// Some reducer-only mutations do not pass through the legacy dirty-marking
	// path. Link those applied/acknowledged rows to the generation the reducer
	// just dirtied so their asynchronous render remains followable.
	if event.RenderGeneration == 0 && st.Dirty &&
		(trace.Decision == DecisionApplied || trace.Decision == DecisionAcknowledged) {
		h.linkRequestRenderGenerationLocked(req, st.Seq)
	}
	return cloneSessionEvent(*event)
}

// recordDuplicateDelivery appends a non-mutating causal row for an IPC retry
// that the dedupe cache replayed before reducer admission. It clones the
// original sanitized decision evidence when available, then stamps the current
// trace/attempt identity and an unchanged before/after state.
func (h *Handler) recordDuplicateDelivery(req Request, ack *DeliveryAcknowledgement) {
	if h == nil || ack == nil || ack.Status != DeliveryAcknowledgementDuplicate {
		return
	}
	windowID := ack.TargetWindowID
	if windowID <= 0 {
		windowID = req.WindowID
	}
	if windowID <= 0 {
		return
	}

	h.stateMu.Lock()
	st := h.ensureWindowStateLocked(windowID)
	var event SessionEvent
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if st.EventLog[i].RequestID == req.RequestID && req.RequestID != "" {
			event = cloneSessionEvent(st.EventLog[i])
			break
		}
	}
	if event.Event == "" {
		event = SessionEvent{
			Event: req.Event, Action: req.Action, ResultIconState: IconStatePreserve,
			ParseOutcome:         ParseOutcomeDecoded,
			NormalizationOutcome: NormalizationOutcomeNotApplicable,
			OriginWindowID:       req.WindowID,
		}
	}
	now := time.Now()
	event.At = now.Unix()
	event.ReceivedAtNs = now.UnixNano()
	event.DaemonSeq = 0
	event.TraceID = req.TraceID
	event.RequestID = req.RequestID
	event.SourceEventID = firstNonEmpty(req.SourceEventID, event.SourceEventID)
	event.Attempt = req.Attempt
	event.Suppressed = true
	event.SuppressionReason = DecisionReasonRequestReplayed
	event.ResultIconState = IconStatePreserve
	event.ReducerDecision = DecisionDuplicate
	event.ReducerReason = DecisionReasonRequestReplayed
	event.DecisionReason = DecisionReasonRequestReplayed
	event.RenderGeneration = 0
	event.RenderOutcome = ""
	event.RenderReason = ""
	event.NotificationTrace = ""
	event.NotificationOutcome = ""
	event.NotificationReason = ""
	event.ProbeTraceIDs = nil
	event.TargetWindowID = windowID
	event.RouteDecision = ack.RouteDecision
	effective := effectiveStateEvidence(st.Effective)
	event.EffectiveBefore = &effective
	effectiveAfter := effective
	event.EffectiveAfter = &effectiveAfter
	if event.RouteEvidence == nil {
		event.RouteEvidence = newRoutingEvidence(req.WindowID)
	}
	event.RouteEvidence.TargetWindowID = windowID
	event.RouteEvidence.ChosenRoute = ack.RouteDecision
	event.RouteEvidence.SelectionReason = DecisionReasonRequestReplayed
	event.RouteEvidence.EffectiveHarness = ack.TargetHarness
	event.RouteEvidence.EffectiveProfile = ack.Profile
	event = h.appendEventLocked(windowID, event)
	h.stateMu.Unlock()
	h.recordDecisionEvent(event)
}
