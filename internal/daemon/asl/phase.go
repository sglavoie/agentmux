package asl

import (
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// PhaseForDiscovered maps a discovered candidate's phase onto the reducer's
// phase vocabulary. The string values already match; the mapping exists to
// fail closed — an unknown phase from a newer loop version degrades to a bare
// between-turns lease instead of an active or attention state.
//
// It is the discovery-side counterpart of events.ASLPhaseForEvent, which maps
// the same vocabulary from a live lifecycle hook. The two must agree, because
// a seeded lease and a received event describe the same run.
func PhaseForDiscovered(phase string) string {
	switch phase {
	case agentloop.PhaseTurn:
		return state.ASLPhaseTurn
	case agentloop.PhaseVerification:
		return state.ASLPhaseVerification
	case agentloop.PhaseWaiting:
		return state.ASLPhaseWaiting
	case agentloop.PhasePaused:
		return state.ASLPhasePaused
	default:
		return state.ASLPhaseBetween
	}
}
