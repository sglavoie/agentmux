package daemon

import (
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
	DecisionReasonUnmanagedPipeSource       = "unmanaged_pipe_source"
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
	DecisionReasonTerminalStopHeld          = "terminal_stop_held"
	DecisionReasonTerminalStopSuperseded    = "terminal_stop_superseded"
	DecisionReasonTerminalStopBypassed      = "terminal_stop_bypassed"
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
	dst.ProducerConfigDir = src.ProducerConfigDir
	if src.HarnessProcess != nil {
		identity := *src.HarnessProcess
		dst.HarnessProcess = &identity
	}
	dst.parseOutcome = src.parseOutcome
	dst.normalizationOutcome = src.normalizationOutcome
	dst.routeEvidence = cloneRoutingEvidence(src.routeEvidence)
	dst.saturationReason = src.saturationReason
}
