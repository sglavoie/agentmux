package harness

import (
	"strings"
)

const (
	Claude  = "claude"
	Copilot = "copilot"
)

var known = map[string]bool{
	Claude:  true,
	Copilot: true,
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
	return []string{Claude, Copilot}
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

