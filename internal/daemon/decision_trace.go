package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const (
	routeDecisionRejected = "rejected"

	routeLookupRequestOnly         = "request_only"
	routeLookupSnapshotUnavailable = "snapshot_unavailable"
	routeLookupSourceNotFound      = "source_not_found"
	routeLookupSourceFound         = "source_found"
)

const maxRoutingEvidenceWindows = 16

func ensureRequestTraceID(req protocol.Request) protocol.Request {
	if (req.Type == protocol.TypeHookEvent || req.Type == protocol.TypeStateUpdate) && req.TraceID == "" {
		req.TraceID = diagnostics.NewTraceID()
	}
	return req
}

func effectiveStateEvidence(state EffectiveState) protocol.EffectiveStateEvidence {
	return protocol.EffectiveStateEvidence{
		Icon: state.Icon, Looping: state.Looping,
		SourceKind: string(state.Source.Kind), SourceKey: state.Source.Key,
	}
}

func cloneRoutingEvidence(in *protocol.RoutingEvidence) *protocol.RoutingEvidence {
	if in == nil {
		return nil
	}
	out := *in
	out.ConsideredWindows = append([]protocol.RoutingWindowEvidence(nil), in.ConsideredWindows...)
	out.RejectedAlternatives = append([]protocol.RejectedRouteEvidence(nil), in.RejectedAlternatives...)
	return &out
}

func cloneSessionEvent(in protocol.SessionEvent) protocol.SessionEvent {
	out := in
	out.RouteEvidence = cloneRoutingEvidence(in.RouteEvidence)
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

func newRoutingEvidence(sourceWindowID int) *protocol.RoutingEvidence {
	return &protocol.RoutingEvidence{
		LookupOutcome:  routeLookupRequestOnly,
		SourceWindowID: sourceWindowID,
	}
}

func populateRoutingCandidates(evidence *protocol.RoutingEvidence, tab *kitty.Tab, source, owner *kitty.Window) {
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
		evidence.ConsideredWindows = append(evidence.ConsideredWindows, protocol.RoutingWindowEvidence{
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

func chooseRoute(evidence *protocol.RoutingEvidence, decision, selectionReason string, target *kitty.Window) {
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
		evidence.RejectedAlternatives = append(evidence.RejectedAlternatives, protocol.RejectedRouteEvidence{
			WindowID: candidate.WindowID,
			Reason:   protocol.DecisionReasonNotSelected,
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

func normalizationOutcomeFor(n protocol.NormalizedEvent) string {
	switch {
	case n.Invalid:
		return protocol.NormalizationOutcomeQuarantined
	case n.Reason == reasonIgnoredEvent:
		return protocol.NormalizationOutcomeIgnored
	case n.Reason == reasonUnknownEvent:
		return protocol.NormalizationOutcomeUnknown
	default:
		return protocol.NormalizationOutcomeNormalized
	}
}

func identityConfidenceFor(n protocol.NormalizedEvent) string {
	switch n.Source.Kind {
	case "":
		return protocol.IdentityConfidenceUnresolved
	case SourceInvalid:
		return protocol.IdentityConfidenceInvalid
	case protocol.SourceLegacy:
		return protocol.IdentityConfidenceLegacySynthesized
	case protocol.SourceProbe:
		return protocol.IdentityConfidenceDaemon
	default:
		return protocol.IdentityConfidenceDeclared
	}
}

func propagateIngressTrace(dst *protocol.Request, src protocol.Request) {
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
	dst.RouteEvidence = cloneRoutingEvidence(src.RouteEvidence)
	dst.SaturationReason = src.SaturationReason
}
