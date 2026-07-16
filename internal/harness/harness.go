package harness

import (
	"strings"
)

const (
	Claude  = "claude"
	Copilot = "copilot"
	Codex   = "codex"
)

// Normalize returns a lowercase harness name. Empty values default to Claude
// so legacy configs keep working.
func Normalize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return Claude
	}
	return name
}

// NormalizeKnown returns the canonical name for a supported harness. Missing
// and unknown values return empty so event-producer identity is never guessed.
// Configuration paths that intentionally default missing values to Claude
// should continue to use Normalize.
func NormalizeKnown(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case Claude:
		return Claude
	case Copilot:
		return Copilot
	case Codex:
		return Codex
	default:
		return ""
	}
}

// Valid reports whether name identifies a supported harness.
func Valid(name string) bool {
	_, ok := registry[Normalize(name)]
	return ok
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
	if !Valid(name) {
		return nil
	}
	return installedHookEventNames(Lookup(name).HookContract().EventMatrix())
}

// HookEventMatrix returns the complete event classification for name,
// including explicitly unsupported events. Unknown harnesses return nil.
func HookEventMatrix(name string) []HookEventContract {
	if !Valid(name) {
		return nil
	}
	return Lookup(name).HookContract().EventMatrix()
}

func installedHookEventNames(matrix []HookEventContract) []string {
	events := make([]string, 0, len(matrix))
	for _, event := range matrix {
		if event.Installation == HookInstalled && event.DisableSupported {
			events = append(events, event.CanonicalName)
		}
	}
	return events
}

func cloneHookEventMatrix(matrix []HookEventContract) []HookEventContract {
	out := make([]HookEventContract, len(matrix))
	copy(out, matrix)
	return out
}
