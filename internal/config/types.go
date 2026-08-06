package config

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// Config is the top-level structure for config.toml.
type Config struct {
	Defaults Defaults    `toml:"defaults"`
	Profiles []Account   `toml:"profiles"`
	Accounts []Account   `toml:"accounts"` // deprecated alias; mirrored from Profiles on load
	Projects []Session   `toml:"projects"`
	Sessions []Session   `toml:"sessions"` // deprecated alias; mirrored from Projects on load
	Hooks    HooksConfig `toml:"hooks"`

	// HarnessEmojis maps a harness name (normalized) to a single emoji
	// glyph used as the harness badge in the Kitty tab title. Looked up
	// by Config.HarnessEmoji.
	HarnessEmojis map[string]string `toml:"harness_emojis"`

	// HarnessColors maps a harness name (normalized) to a palette color
	// name. Used for harness-name cells in the dashboard and launch picker.
	HarnessColors map[string]string `toml:"harness_colors"`

	// Deprecations records any legacy TOML keys encountered during Load.
	// Populated from the raw decoded struct before normalizeAliases erases
	// the distinction between deprecated and canonical fields. Surfaced by
	// the TUI as a one-line banner pointing the user at the migration.
	Deprecations []Deprecation `toml:"-"`

	harnessEmojiKeyConflicts []string
	harnessColorKeyConflicts []string
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
	Sort                 string               `toml:"sort"`
	RefreshInterval      int                  `toml:"refresh_interval"`
	GuardDuration        int                  `toml:"guard_duration"`
	IdleThreshold        int                  `toml:"idle_threshold"`         // seconds before a session is considered idle; 0 → default (300)
	StaleActiveThreshold int                  `toml:"stale_active_threshold"` // deprecated: use [defaults.watchdog].active; mirrored on load
	Watchdog             WatchdogConfig       `toml:"watchdog"`               // per-state stale-detection thresholds (seconds)
	StopConfirm          StopConfirmConfig    `toml:"stop_confirm"`           // confirmed-idle gate before a native stop paints the terminal icon
	SessionArchive       SessionArchiveConfig `toml:"session_archive"`        // retention policy for the searchable session-transcript index
	Handoff              HandoffConfig        `toml:"handoff"`                // bounded cross-harness context-pack generation
	Diagnostics          DiagnosticsConfig    `toml:"diagnostics"`            // private event journal and native/daemon log retention
	Args                 []string             `toml:"args"`                   // preferred shared launch args
	ClaudeFlags          []string             `toml:"claude_flags"`           // legacy alias for args
	ConfirmDestructive   bool                 `toml:"confirm_destructive"`    // direct dashboard r always confirms; when true, close/actions-overlay restart use modal confirm instead of toast paths
	Notifications        NotificationConfig   `toml:"notifications"`
	Preview              PreviewConfig        `toml:"preview"`
	Jump                 JumpConfig           `toml:"jump"`            // bracket-key (]/[ and }/{) attention-state navigation
	Editor               string               `toml:"editor"`          // overrides $VISUAL/$EDITOR for the `O` keybinding (Open in editor)
	PlansDirectory       string               `toml:"plans_directory"` // root for the `P` keybinding's plans index; defaults to ~/.local/share/agent-plans
}

// DiagnosticsConfig controls private diagnostic persistence. Pointer booleans
// preserve the difference between an omitted key (the safe observable default
// is enabled) and an explicit false value.
type DiagnosticsConfig struct {
	JournalEnabled      *bool  `toml:"journal_enabled"`
	JournalKeepDays     int    `toml:"journal_keep_days"`
	JournalMaxMiB       int    `toml:"journal_max_mib"`
	JournalDetail       string `toml:"journal_detail"` // "standard" (default) samples success-path hook stages; "full" persists every stage
	NativeLogging       *bool  `toml:"native_logging"`
	NativeKeepDays      int    `toml:"native_keep_days"`
	NativeMaxMiB        int    `toml:"native_max_mib"`
	NativeSessionMaxMiB int    `toml:"native_session_max_mib"`
}

func (d DiagnosticsConfig) JournalEnabledValue() bool {
	return d.JournalEnabled == nil || *d.JournalEnabled
}

// JournalDetailFullValue reports whether journal_detail requests unsampled
// per-stage hook persistence. Anything other than an explicit "full" keeps the
// sampled "standard" detail so diagnostics remain fail-open on bad input.
func (d DiagnosticsConfig) JournalDetailFullValue() bool {
	return strings.EqualFold(strings.TrimSpace(d.JournalDetail), "full")
}

func (d DiagnosticsConfig) NativeLoggingValue() bool {
	return d.NativeLogging == nil || *d.NativeLogging
}

// JumpConfig controls which session states the TUI bracket-key navigation
// (]/[ and }/{) treats as "attention". When AttentionStates is empty,
// applyDefaults fills it with the canonical set
// ["key", "error", "warning", "question"]. Validation rejects unknown state
// names (anything outside palette.StateOrder()).
type JumpConfig struct {
	AttentionStates []string `toml:"attention_states"`
}

// WatchdogConfig holds per-state stale-detection thresholds, in seconds. A
// zero value means "use the default" — applyDefaults populates each field
// independently so users can override any subset. The legacy
// `defaults.stale_active_threshold` mirrors into Active when [defaults.watchdog]
// is absent.
//
// HookSilence is the per-account "no hook event at all" threshold consumed by
// the hook-silence sweep added in session 5; values < HookSilence are still
// accepted but cause noisy detection.
type WatchdogConfig struct {
	Active      int `toml:"active"`       // PostToolUse-driven active icon; 0 → default (600)
	Subagent    int `toml:"subagent"`     // SubagentStart-driven active icon; 0 → default (1800)
	Compact     int `toml:"compact"`      // PreCompact-driven active icon; 0 → default (180)
	HookSilence int `toml:"hook_silence"` // account-level hook-silence threshold; 0 → default (14400)

	// ClaudeContextLimitProbe enables the periodic screen-text probe that
	// detects Claude context-limit stalls and swaps the bolt ⚡ icon to the
	// error ❗ icon. *bool so the load path can distinguish "user omitted the
	// key" (nil → default true) from "user set claude_context_limit_probe =
	// false" (&false → suppressed). Only consulted from the global
	// [defaults.watchdog] block — per-profile overrides do not apply.
	ClaudeContextLimitProbe *bool `toml:"claude_context_limit_probe"`
}

// StopConfirmConfig gates the terminal 🛑 icon behind a confirmed-idle hold: a
// native stop hook is held while the tab keeps showing ⚡ until a quiet period
// elapses with no further activity, with a hard deadline so a window whose
// harness never produces confirming evidence still converges. Zero values mean
// "use the default" so the section can be omitted entirely.
//
// Enabled is *bool so the load path can distinguish "user omitted the key"
// (nil → default true) from "user set enabled = false" (&false → immediate stop,
// the pre-hold behaviour).
type StopConfirmConfig struct {
	Delay   int   `toml:"delay"`   // quiet seconds before a held stop may commit; 0 → default (6)
	Timeout int   `toml:"timeout"` // hard deadline in seconds; commits unconfirmed; 0 → default (15)
	Enabled *bool `toml:"enabled"`
}

// SessionArchiveConfig controls retention for the searchable session-transcript
// index. A zero value means "use the default" — applyDefaults populates
// KeepDays so users can omit the section entirely.
type SessionArchiveConfig struct {
	KeepDays       int `toml:"keep_days"`         // days to retain index entries; 0 → default (30)
	ScanMaxAgeDays int `toml:"scan_max_age_days"` // 0 → fall back to KeepDays; <0 → unlimited (scan everything)
}

// HandoffConfig controls the size and transcript window used when generating
// local handoff packs. Zero values are filled by applyDefaults so existing
// configurations keep working without a [defaults.handoff] block.
type HandoffConfig struct {
	MaxBytes       int `toml:"max_bytes"`
	RecentMessages int `toml:"recent_messages"`
}

// NotificationConfig controls which macOS notifications fire.
//
// StopFailure is *bool so the load path can distinguish "user omitted the key"
// (nil → defaults to true so pre-existing configs still get terminal-failure
// notifications) from "user set stop_failure = false" (&false → suppressed).
type NotificationConfig struct {
	AlerterPath          string `toml:"alerter_path"`
	Error                bool   `toml:"error"`
	Permission           bool   `toml:"permission"`
	Stop                 bool   `toml:"stop"`
	StopFailure          *bool  `toml:"stop_failure"`
	StaleActive          *bool  `toml:"stale_active"` // notify on stale-active detection; nil → default true
	HookSilence          *bool  `toml:"hook_silence"` // notify on hook-silence detection; nil → default true
	PermissionDebounceMs int    `toml:"permission_debounce_ms"`
}

// AnyEnabled reports whether at least one native-notification category is
// enabled. Nil pointer fields retain their backward-compatible default of true.
func (c NotificationConfig) AnyEnabled() bool {
	return c.Error || c.Permission || c.Stop ||
		c.StopFailure == nil || *c.StopFailure ||
		c.StaleActive == nil || *c.StaleActive ||
		c.HookSilence == nil || *c.HookSilence
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
	// Color is the required per-profile palette color used to render the
	// project title (picker, main list, detail header, preview headers,
	// account chips/badges) and inactive Kitty tab-bar accent. Active tab
	// colors come from kitty.conf. Validation rejects an empty or invalid
	// value.
	Color    string         `toml:"color"`
	Args     []string       `toml:"args"`
	Watchdog WatchdogConfig `toml:"watchdog"` // per-profile override; zero fields fall back to defaults at read time
}

// Session defines a configured project / launch template. Sessions remain as a
// deprecated internal type name because much of the codebase still uses it
// while the user-facing config is moving to `projects`.
type Session struct {
	Title       string            `toml:"title"`
	Directory   string            `toml:"directory"`
	Profile     string            `toml:"profile"`
	Account     string            `toml:"account"`
	Args        []string          `toml:"args"`         // preferred per-project args
	ClaudeFlags []string          `toml:"claude_flags"` // legacy alias for args
	Env         map[string]string `toml:"env"`
	Groups      []string          `toml:"groups"`
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

// HarnessEmoji returns the configured tab-title emoji badge for the named
// harness, or "" if none is set. Normalizes the harness name so callers
// can pass raw or effective harness names interchangeably.
func (c *Config) HarnessEmoji(name string) string {
	if c == nil {
		return ""
	}
	return c.HarnessEmojis[harness.Normalize(name)]
}

// HarnessColor returns the configured UI color for the named harness, or ""
// if none is set. Normalizes the harness name so callers can pass raw or
// effective harness names interchangeably.
func (c *Config) HarnessColor(name string) string {
	if c == nil {
		return ""
	}
	return c.HarnessColors[harness.Normalize(name)]
}

// ProfileColor returns the palette color name configured on the matched
// profile. Returns "" when no profile matches; validation guarantees a
// non-empty Color for every matched profile.
func (c *Config) ProfileColor(profileName string) string {
	if c == nil {
		return ""
	}
	for _, p := range c.Profiles {
		if strings.EqualFold(p.Name, profileName) {
			return p.Color
		}
	}
	return ""
}

// DefaultProfileFor returns the configured default profile name for the
// project whose title matches projectTitle, or "" if none matches.
// Override-aware callers use this to look up the project's identity color
// after the in-memory session has been rewritten by sessionWithProfile.
// Matches case-sensitively because project titles are user-authored and used
// as the dedup key elsewhere.
func (c *Config) DefaultProfileFor(projectTitle string) string {
	if c == nil || projectTitle == "" {
		return ""
	}
	for _, p := range c.Projects {
		if p.Title == projectTitle {
			return p.EffectiveProfile()
		}
	}
	return ""
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
