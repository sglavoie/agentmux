package harness

import (
	"strings"
)

const (
	Claude  = "claude"
	Copilot = "copilot"
	Codex   = "codex"
)

var known = map[string]bool{
	Claude:  true,
	Copilot: true,
	Codex:   true,
}

// Normalize returns a lowercase harness name. Empty values default to Claude
// so legacy configs keep working.
func Normalize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return Claude
	}
	return name
}

// Valid reports whether name identifies a supported harness.
func Valid(name string) bool {
	return known[Normalize(name)]
}

// Names returns the supported harness names in stable order.
func Names() []string {
	return []string{Claude, Copilot, Codex}
}

// Executable returns the CLI executable used to launch the harness.
func Executable(name string) string { return Lookup(name).Executable() }

// EnvVar returns the harness-specific env var used to override its home/config
// directory.
func EnvVar(name string) string { return Lookup(name).ConfigEnvVar() }

// DefaultHomeDir returns the default per-user home/config directory for the
// harness.
func DefaultHomeDir(name string) string { return Lookup(name).DefaultHomeDir() }

// HookFileName is the generated Copilot hook config filename.
func HookFileName() string {
	return "agent-mux.json"
}

// KnownEvents returns the set of hook event names that the named harness
// installs. Used by config.Validate to surface typos in
// hooks.overrides.<profile>.disabled_events before they become silent no-ops.
// Returns nil for unknown harness names.
func KnownEvents(name string) []string {
	switch Normalize(name) {
	case Claude:
		out := make([]string, len(claudeHookEvents))
		copy(out, claudeHookEvents)
		return out
	case Copilot:
		out := make([]string, len(copilotHookEvents))
		copy(out, copilotHookEvents)
		return out
	case Codex:
		out := make([]string, len(codexHookEvents))
		copy(out, codexHookEvents)
		return out
	}
	return nil
}
