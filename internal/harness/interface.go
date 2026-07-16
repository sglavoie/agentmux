package harness

import (
	"errors"
	"fmt"
)

// InvocationKind identifies the session lifecycle operation a harness CLI
// should perform.
type InvocationKind string

const (
	// InvocationFresh starts a new interactive conversation.
	InvocationFresh InvocationKind = "fresh"
	// InvocationResume reopens an existing conversation in place.
	InvocationResume InvocationKind = "resume"
	// InvocationFork branches an existing conversation without changing it.
	InvocationFork InvocationKind = "fork"
)

// InvocationInput is the harness-independent context supplied when building a
// CLI invocation. Implementations decide where profile arguments, a session
// ID, and an initial prompt appear in the final argv.
type InvocationInput struct {
	ProfileArgs   []string
	Environment   map[string]string
	CWD           string
	SessionID     string
	InitialPrompt string
	NativeLogging NativeLoggingRequest

	nativeLogging NativeLogging
}

// NativeLoggingRequest describes the private Agent Mux directory available to
// a harness invocation. Harness adapters remain responsible for detecting
// equivalent user controls and for placing any injected flags in the correct
// CLI-global segment.
type NativeLoggingRequest struct {
	Enabled   bool
	LaunchID  string
	Directory string
}

// NativeLogCoverage is the effective ownership/health of native harness logs.
type NativeLogCoverage string

const (
	NativeLogManaged  NativeLogCoverage = "managed"
	NativeLogExternal NativeLogCoverage = "external"
	NativeLogDegraded NativeLogCoverage = "degraded"
	NativeLogDisabled NativeLogCoverage = "disabled"
)

// NativeLogging records the controls that will actually reach the harness.
// Path is a file for Claude and a directory for Codex/Copilot.
type NativeLogging struct {
	Coverage         NativeLogCoverage
	Path             string
	Level            string
	Override         string
	ManagedDirectory bool
}

// Invocation is a structured harness launch specification. Args is already
// ordered for the target CLI but intentionally remains unquoted; callers must
// render it through their shell-quoting boundary.
type Invocation struct {
	Harness       string
	Executable    string
	Args          []string
	Environment   map[string]string
	CWD           string
	InitialPrompt string
	Kind          InvocationKind
	SessionID     string
	LaunchID      string
	NativeLogging NativeLogging
}

// OperationCapability describes whether a native harness operation is
// available. Reason is populated only for unsupported operations so callers
// can show an actionable explanation instead of guessing from a harness name.
type OperationCapability struct {
	Supported bool
	Reason    string
}

// InvocationCapabilities declares a harness's native continuity support.
type InvocationCapabilities struct {
	Fresh  OperationCapability
	Resume OperationCapability
	Fork   OperationCapability
}

// For returns the capability for kind. Unknown operations are unsupported.
func (c InvocationCapabilities) For(kind InvocationKind) OperationCapability {
	switch kind {
	case InvocationFresh:
		return c.Fresh
	case InvocationResume:
		return c.Resume
	case InvocationFork:
		return c.Fork
	default:
		return OperationCapability{Reason: fmt.Sprintf("unknown invocation operation %q", kind)}
	}
}

// ErrUnsupportedInvocation marks an attempt to build an operation that the
// selected harness cannot perform natively.
var ErrUnsupportedInvocation = errors.New("harness invocation unsupported")

// UnsupportedInvocationError carries enough detail for CLI and TUI clients to
// explain an unavailable native operation without duplicating capability rules.
type UnsupportedInvocationError struct {
	Harness string
	Kind    InvocationKind
	Reason  string
}

func (e *UnsupportedInvocationError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s %s is unavailable: %s", e.Harness, e.Kind, e.Reason)
	}
	return fmt.Sprintf("%s %s is unavailable", e.Harness, e.Kind)
}

func (*UnsupportedInvocationError) Unwrap() error { return ErrUnsupportedInvocation }

// HookEventBehavior describes whether an event is allowed to mutate Agent
// Mux's reducer state. Observer-only events are still delivered and recorded,
// but they must never change icons, routing ownership, or notifications.
type HookEventBehavior string

const (
	HookStateChanging HookEventBehavior = "state_changing"
	HookObserverOnly  HookEventBehavior = "observer_only"
)

// HookEventInstallation records whether Agent Mux installs an event. Events
// with special output or matcher contracts remain in the matrix as explicitly
// unsupported so adding a vendor event can never make it install by accident.
type HookEventInstallation string

const (
	HookInstalled   HookEventInstallation = "installed"
	HookUnsupported HookEventInstallation = "unsupported"
)

// HookEventContract is one row in a harness's complete hook contract matrix.
// DocumentationSource and VerifiedCLIVersion make contract staleness visible
// when a newer CLI is installed than the version this row was checked against.
type HookEventContract struct {
	CanonicalName       string
	Behavior            HookEventBehavior
	Installation        HookEventInstallation
	DisableSupported    bool
	SpecialSemantics    string
	DocumentationSource string
	VerifiedCLIVersion  string
}

// HookContract encapsulates harness-specific hook installation logic.
type HookContract interface {
	// EventMatrix returns one explicit classification for every documented
	// event known to this Agent Mux release.
	EventMatrix() []HookEventContract
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
	// TranscriptsDir returns the directory under homeDir where this harness
	// writes per-session transcript files (typically JSONL). Callers pass the
	// effective profile home directory; the harness joins the appropriate
	// subdirectory.
	TranscriptsDir(homeDir string) string
	// InvocationCapabilities reports which fresh/resume/fork operations the
	// harness supports natively.
	InvocationCapabilities() InvocationCapabilities
	// BuildInvocation constructs an ordered, unquoted CLI launch specification
	// for kind. Implementations own all CLI-specific argument placement.
	BuildInvocation(kind InvocationKind, input InvocationInput) (Invocation, error)
}
