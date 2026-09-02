package debug

// The two projections `amx doctor`, `amx daemon audit`, and the `amx daemon`
// status/export surfaces all read the same way. They live here because doctor
// and audit are the surfaces that must agree with the daemon's own output --
// a doctor that computed live window metadata differently from `daemon
// export-events` would report drift that does not exist.

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// ASLStatusLine renders the optional ASL capability block for text surfaces.
// It reports only availability, identity, and sanitized counters: no plan
// directory, question, or reply text ever reaches this line.
func ASLStatusLine(s *aslbridge.Status) string {
	state := "unavailable"
	switch {
	case s.Mode == string(aslbridge.ModeDisabled):
		state = "disabled"
	case s.Available && s.MutationsEnabled:
		state = "ready"
	case s.Available:
		state = "read-only"
	}
	detail := "mode " + s.Mode
	if s.Executable != "" {
		detail += " at " + s.Executable
	} else if s.Command != "" {
		detail += " via " + s.Command
	}
	if s.Version != "" {
		detail += fmt.Sprintf("; asl %s protocol %d", s.Version, s.Protocol)
	}
	if s.SnapshotAgeSeconds != nil {
		age := fmt.Sprintf("; snapshot %ds old", *s.SnapshotAgeSeconds)
		if s.SnapshotStale {
			age += " (stale)"
		}
		detail += age
	}
	if s.RestartCount > 0 {
		detail += fmt.Sprintf("; restarts %d", s.RestartCount)
	}
	if outcomes := aslActionOutcomes(s.Metrics); outcomes != "" {
		detail += "; actions " + outcomes
	}
	if s.Reason != "" {
		detail += "; " + s.Reason
	}
	return fmt.Sprintf("%s (%s)", state, detail)
}

// aslActionOutcomes summarizes how the operator's mutations settled. The status
// vocabulary is fixed by the protocol, so the rendered order is fixed too and a
// counter key can never be bridge-authored text.
func aslActionOutcomes(m *aslbridge.Metrics) string {
	if m == nil {
		return ""
	}
	var parts []string
	for _, status := range []string{
		aslbridge.OperationAccepted,
		aslbridge.OperationConsumed,
		aslbridge.OperationRejected,
		aslbridge.OperationExpired,
		aslbridge.OperationSuperseded,
	} {
		if count := m.OperationsByStatus[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, status))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "/")
}
