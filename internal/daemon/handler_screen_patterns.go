package daemon

import (
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// This file publishes the outcome of the startup screen-pattern load. The
// classifier's UI copy is data with a user override (see
// internal/screenstate/patterns.go), and a bad or stale override is a silent
// failure: the daemon keeps running with the embedded default and the only
// visible symptom is a tab that never leaves ⚡. So the daemon carries the load
// result in its health response and `amx doctor` turns it into a finding.

// SetScreenPatterns records the result of the startup screenstate.Load call.
// Called once on the startup goroutine before any probe loop starts; see the
// screenPatterns field for the lock-free convention this relies on.
func (h *Handler) SetScreenPatterns(warnings []screenstate.LoadWarning) {
	snapshot := &protocol.ScreenPatternsSnapshot{Sources: screenstate.LoadedPatternSources()}
	for _, warning := range warnings {
		snapshot.Warnings = append(snapshot.Warnings, protocol.ScreenPatternWarning{
			Kind:    string(warning.Kind),
			Harness: warning.Harness,
			Path:    warning.Path,
			Detail:  warning.Detail,
		})
	}
	h.screenPatterns = snapshot
}
