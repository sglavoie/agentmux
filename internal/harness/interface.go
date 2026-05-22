package harness

// HookContract encapsulates harness-specific hook installation logic.
type HookContract interface {
	// InstallPath returns the absolute path to the hooks file for the given homeDir.
	InstallPath(homeDir string) string
	// MergeData produces the complete new file bytes to write, given existing file
	// bytes (nil if the file doesn't exist), events to disable, and the amx binary path.
	MergeData(existingData []byte, disabledEvents []string, amxBin string) ([]byte, error)
	// ParseInstalledEvents reads the hooks file at path and returns the amx-managed
	// event names currently installed. Returns nil if the file does not exist.
	ParseInstalledEvents(path string) ([]string, error)
}

// Impl is the interface all harness implementations must satisfy.
// Use Lookup(name) to obtain one.
type Impl interface {
	// Name returns the canonical lowercase harness name ("claude", "copilot").
	Name() string
	// Executable is the CLI binary launched for sessions (e.g. "claude").
	Executable() string
	// ConfigEnvVar is the env var that overrides the home directory
	// (e.g. "CLAUDE_CONFIG_DIR"). Returns "" if the harness has none.
	ConfigEnvVar() string
	// DefaultHomeDir is the default per-user config/home directory.
	DefaultHomeDir() string
	// PipeModeTokens returns the CLI tokens that indicate non-interactive
	// (pipe/batch) invocation for this harness when they appear after the
	// executable in argv. Depending on the harness, these may be flags or
	// subcommands.
	PipeModeTokens() []string
	// HookContract returns the harness-specific hook installation contract.
	HookContract() HookContract
}
