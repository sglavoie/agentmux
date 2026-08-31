package daemon

import (
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// This file publishes the outcome of the startup screen-pattern load. The
// classifier's UI copy is data with a user override (see
// internal/screenstate/patterns.go), and a bad or stale override is a silent
// failure: the daemon keeps running with the embedded default and the only
// visible symptom is a tab that never leaves ⚡. So the daemon carries the load
// result in its health response and `amx doctor` turns it into a finding.

// ScreenPatternWarning is one screenstate.LoadWarning in health-response form.
// Kind is the stable machine-readable vocabulary from
// screenstate.LoadWarningKind; consumers key on it rather than on Detail.
type ScreenPatternWarning struct {
	Kind    string `json:"kind"`
	Harness string `json:"harness,omitempty"`
	Path    string `json:"path,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ScreenPatternsSnapshot is the health-response view of the startup pattern
// load. Sources maps each pattern set's name to where its installed copy came
// from — "embedded" or the absolute override path — so a stale local file is
// visible without reading the operator's config directory. Warnings is
// everything Load wanted a human to know, in Load's deterministic order.
//
// Note that a pattern set's name is not always a harness: internal/contextlimit
// owns one too, and it flows through the same loader under the name
// "contextlimit".
type ScreenPatternsSnapshot struct {
	Sources  map[string]string      `json:"sources"`
	Warnings []ScreenPatternWarning `json:"warnings,omitempty"`
}

// SetScreenPatterns records the result of the startup screenstate.Load call.
// Called once on the startup goroutine before any probe loop starts; see the
// screenPatterns field for the lock-free convention this relies on.
func (h *Handler) SetScreenPatterns(warnings []screenstate.LoadWarning) {
	snapshot := &ScreenPatternsSnapshot{Sources: screenstate.LoadedPatternSources()}
	for _, warning := range warnings {
		snapshot.Warnings = append(snapshot.Warnings, ScreenPatternWarning{
			Kind:    string(warning.Kind),
			Harness: warning.Harness,
			Path:    warning.Path,
			Detail:  warning.Detail,
		})
	}
	h.screenPatterns = snapshot
}

// ScreenPatterns returns the recorded startup load, or nil when Load never ran
// for this Handler.
func (h *Handler) ScreenPatterns() *ScreenPatternsSnapshot {
	return h.screenPatterns
}
