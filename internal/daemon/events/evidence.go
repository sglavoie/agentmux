package events

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// Route decisions recorded onto SessionEvent.RouteDecision: the firing pane
// was the target itself, the event was rerouted to the tab's managed title
// owner, ownership could not be resolved and the daemon fell back to the
// firing pane, or no route was taken at all.
const (
	RouteDecisionSource         = "source"
	RouteDecisionOwner          = "owner"
	RouteDecisionSourceFallback = "source_fallback"
	RouteDecisionRejected       = "rejected"

	RouteLookupRequestOnly         = "request_only"
	RouteLookupSnapshotUnavailable = "snapshot_unavailable"
	RouteLookupSourceNotFound      = "source_not_found"
	RouteLookupSourceFound         = "source_found"
)

func EnsureRequestTraceID(req protocol.Request) protocol.Request {
	if (req.Type == protocol.TypeHookEvent || req.Type == protocol.TypeStateUpdate) && req.TraceID == "" {
		req.TraceID = diagnostics.NewTraceID()
	}
	return req
}

func EffectiveStateEvidence(effective state.EffectiveState) protocol.EffectiveStateEvidence {
	return protocol.EffectiveStateEvidence{
		Icon: effective.Icon, Looping: effective.Looping,
		SourceKind: string(effective.Source.Kind), SourceKey: effective.Source.Key,
	}
}

func CloneRoutingEvidence(in *protocol.RoutingEvidence) *protocol.RoutingEvidence {
	if in == nil {
		return nil
	}
	out := *in
	out.ConsideredWindows = append([]protocol.RoutingWindowEvidence(nil), in.ConsideredWindows...)
	out.RejectedAlternatives = append([]protocol.RejectedRouteEvidence(nil), in.RejectedAlternatives...)
	return &out
}

func CloneSessionEvent(in protocol.SessionEvent) protocol.SessionEvent {
	out := in
	out.RouteEvidence = CloneRoutingEvidence(in.RouteEvidence)
	out.ProbeTraceIDs = append([]string(nil), in.ProbeTraceIDs...)
	out.ProcessSources = append([]protocol.ProcessLifecycleSourceEvidence(nil), in.ProcessSources...)
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

func NewRoutingEvidence(sourceWindowID int) *protocol.RoutingEvidence {
	return &protocol.RoutingEvidence{
		LookupOutcome:  RouteLookupRequestOnly,
		SourceWindowID: sourceWindowID,
	}
}

// RouteTarget is the identity and presentation context needed for a routing
// decision, independent of how the terminal supplied it.
type RouteTarget struct {
	WindowID int
	Harness  string
	Profile  string
}

func ChooseRoute(evidence *protocol.RoutingEvidence, decision, selectionReason string, target *RouteTarget) {
	if evidence == nil {
		return
	}
	evidence.ChosenRoute = decision
	evidence.SelectionReason = selectionReason
	if target == nil {
		if decision == RouteDecisionSource || decision == RouteDecisionSourceFallback {
			evidence.TargetWindowID = evidence.SourceWindowID
		}
		return
	}
	evidence.TargetWindowID = target.WindowID
	evidence.EffectiveHarness = target.Harness
	evidence.EffectiveProfile = target.Profile
	evidence.OwnerMatched = evidence.TargetWindowID > 0 && decision == RouteDecisionOwner
	for _, candidate := range evidence.ConsideredWindows {
		if candidate.WindowID == target.WindowID {
			continue
		}
		evidence.RejectedAlternatives = append(evidence.RejectedAlternatives, protocol.RejectedRouteEvidence{
			WindowID: candidate.WindowID,
			Reason:   protocol.DecisionReasonNotSelected,
		})
	}
}

func PropagateIngressTrace(dst *protocol.Request, src protocol.Request) {
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
	dst.ParseOutcome = src.ParseOutcome
	dst.NormalizationOutcome = src.NormalizationOutcome
	dst.RouteEvidence = CloneRoutingEvidence(src.RouteEvidence)
	dst.SaturationReason = src.SaturationReason
	dst.RuntimeSessionID = src.RuntimeSessionID
	if src.Origin != nil {
		origin := *src.Origin
		dst.Origin = &origin
	}
}
