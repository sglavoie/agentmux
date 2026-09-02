package events

import "github.com/sglavoie/agentmux/internal/daemon/protocol"

// NormalizationOutcomeFor classifies what normalization did with one event.
// It is the single place that maps the reason a normalized event carries onto
// the outcome vocabulary the diagnostic surfaces report, so a new refusal
// reason has exactly one classification to update.
func NormalizationOutcomeFor(n protocol.NormalizedEvent) string {
	switch {
	case n.Invalid:
		return protocol.NormalizationOutcomeQuarantined
	case n.Reason == ReasonIgnoredEvent:
		return protocol.NormalizationOutcomeIgnored
	case n.Reason == ReasonUnknownEvent:
		return protocol.NormalizationOutcomeUnknown
	default:
		return protocol.NormalizationOutcomeNormalized
	}
}

// IdentityConfidenceFor reports how much the daemon trusts the source identity
// a normalized event resolved to: declared by the producer, synthesized from a
// legacy payload, recovered by a daemon probe, or not resolved at all.
func IdentityConfidenceFor(n protocol.NormalizedEvent) string {
	switch n.Source.Kind {
	case "":
		return protocol.IdentityConfidenceUnresolved
	case protocol.SourceInvalid:
		return protocol.IdentityConfidenceInvalid
	case protocol.SourceLegacy:
		return protocol.IdentityConfidenceLegacySynthesized
	case protocol.SourceProbe:
		return protocol.IdentityConfidenceDaemon
	default:
		return protocol.IdentityConfidenceDeclared
	}
}
