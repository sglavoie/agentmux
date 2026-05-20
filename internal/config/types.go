package config

import "github.com/sglavoie/agentmux/internal/harness"

// Config is the top-level structure for config.toml.
type Config struct {
	Defaults Defaults             `toml:"defaults"`
	Profiles []Account            `toml:"profiles"`
	Accounts []Account            `toml:"accounts"` // deprecated alias; mirrored from Profiles on load
	Projects []Session            `toml:"projects"`
	Sessions []Session            `toml:"sessions"` // deprecated alias; mirrored from Projects on load
	Hooks    HooksConfig          `toml:"hooks"`

	// Deprecations records any legacy TOML keys encountered during Load.
	// Populated from the raw decoded struct before normalizeAliases erases
	// the distinction between deprecated and canonical fields. Surfaced by
	// the TUI as a one-line banner pointing the user at the migration.
	Deprecations []Deprecation `toml:"-"`
}

// Deprecation captures a single legacy TOML key that the user's config
// relies on. Location is a short pointer to where the key was used
// (e.g. "[defaults]" or "[[sessions]] api") so the user can find it.
type Deprecation struct {
	Key         string
	Replacement string
	Location    string
}

// Defaults holds global default settings.
type Defaults struct {
	Sort               string             `toml:"sort"`
	RefreshInterval    int                `toml:"refresh_interval"`
	GuardDuration      int                `toml:"guard_duration"`
	IdleThreshold      int                `toml:"idle_threshold"` // seconds before a session is considered idle; 0 → default (300)
	StaleActiveThreshold int              `toml:"stale_active_threshold"` // seconds before an active session is flagged as stale; 0 → default (600)
	Args               []string           `toml:"args"`         // preferred shared launch args
	ClaudeFlags        []string           `toml:"claude_flags"` // legacy alias for args
	ConfirmDestructive bool               `toml:"confirm_destructive"` // when true, fall back to the modal y/n confirm for close/restart instead of toast+undo
	Notifications      NotificationConfig `toml:"notifications"`
	Preview            PreviewConfig      `toml:"preview"`
}

// NotificationConfig controls which macOS notifications fire.
//
// StopFailure is *bool so the load path can distinguish "user omitted the key"
// (nil → defaults to true so pre-existing configs still get terminal-failure
// notifications) from "user set stop_failure = false" (&false → suppressed).
type NotificationConfig struct {
	Error                bool  `toml:"error"`
	Permission           bool  `toml:"permission"`
	Stop                 bool  `toml:"stop"`
	StopFailure          *bool `toml:"stop_failure"`
	StaleActive          *bool `toml:"stale_active"` // notify on stale-active detection; nil → default true
	PermissionDebounceMs int   `toml:"permission_debounce_ms"`
}

// PreviewConfig controls the dashboard split-pane live preview.
type PreviewConfig struct {
	Direction string  `toml:"direction"`  // "horizontal" | "vertical"
	Ratio     float64 `toml:"ratio"`      // fraction of width/height given to preview (0 < ratio < 1)
	RefreshMs int     `toml:"refresh_ms"` // tick interval while preview is open
	MinCols   int     `toml:"min_cols"`   // below this, horizontal split is disabled
	MinRows   int     `toml:"min_rows"`   // below this, vertical split is disabled
}

// Account defines a launch profile. Accounts remain as a deprecated internal
// type name because much of the codebase still uses it while the user-facing
// config is moving to `profiles`.
type Account struct {
	Name      string `toml:"name"`
	Harness   string `toml:"harness"`
	HomeDir   string `toml:"home_dir"`
	ConfigDir string `toml:"config_dir"`
	Color     string `toml:"color"`
	Args      []string `toml:"args"`
}

// Session defines a configured project / launch template. Sessions remain as a
// deprecated internal type name because much of the codebase still uses it
// while the user-facing config is moving to `projects`.
type Session struct {
	Title          string            `toml:"title"`
	Directory      string            `toml:"directory"`
	Profile        string            `toml:"profile"`
	Account        string            `toml:"account"`
	Args           []string          `toml:"args"`          // preferred per-project args
	ClaudeFlags    []string          `toml:"claude_flags"` // legacy alias for args
	Env            map[string]string `toml:"env"`
	Groups         []string          `toml:"groups"`
}

// HooksConfig is the top-level [hooks] table.
type HooksConfig struct {
	Overrides map[string]AccountHookOverride `toml:"overrides"`
}

// AccountHookOverride holds per-account hook customizations.
type AccountHookOverride struct {
	DisabledEvents []string `toml:"disabled_events"`
}

// validColors is the set of allowed color names.
var validColors = map[string]bool{
	"green": true, "blue": true, "red": true, "orange": true,
	"purple": true, "teal": true, "pink": true, "yellow": true,
	"gray": true, "cyan": true,
}

// validSorts is the set of allowed sort values.
var validSorts = map[string]bool{
	"alpha": true, "account": true,
}

// validPreviewDirections is the set of allowed preview directions.
var validPreviewDirections = map[string]bool{
	"horizontal": true, "vertical": true,
}

// ValidColor reports whether s is a valid palette color name.
func ValidColor(s string) bool { return validColors[s] }

// ValidSort reports whether s is a valid sort value.
func ValidSort(s string) bool { return validSorts[s] }

// ValidPreviewDirection reports whether s is a valid preview direction.
func ValidPreviewDirection(s string) bool { return validPreviewDirections[s] }

// EffectiveArgs returns the normalized default args, preserving legacy
// claude_flags behavior when args is unset.
func (d Defaults) EffectiveArgs() []string {
	if len(d.Args) > 0 {
		return d.Args
	}
	return d.ClaudeFlags
}

// EffectiveHarness returns the normalized harness for the profile.
func (a Account) EffectiveHarness() string {
	return harness.Normalize(a.Harness)
}

// EffectiveHomeDir returns the normalized profile home/config directory.
func (a Account) EffectiveHomeDir() string {
	if a.HomeDir != "" {
		return a.HomeDir
	}
	if a.ConfigDir != "" {
		return a.ConfigDir
	}
	return harness.DefaultHomeDir(a.EffectiveHarness())
}

// EffectiveProfile returns the normalized profile name for the project.
func (s Session) EffectiveProfile() string {
	if s.Profile != "" {
		return s.Profile
	}
	return s.Account
}

// EffectiveArgs returns the normalized per-project args, preferring args over
// legacy claude_flags.
func (s Session) EffectiveArgs() []string {
	if len(s.Args) > 0 {
		return s.Args
	}
	return s.ClaudeFlags
}
