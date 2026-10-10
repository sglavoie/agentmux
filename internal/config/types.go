package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/palette"
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

	// UnknownKeys lists every TOML key in the file that maps to no setting,
	// in file order. A misspelled key decodes to nothing and leaves its
	// setting at the default, so this is the only trace a typo leaves; it is
	// reported by `amx config validate` rather than refused, because a config
	// that loaded yesterday must still load today.
	UnknownKeys []string `toml:"-"`

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
	ConfirmDestructive   bool                 `toml:"confirm_destructive"`    // when true, the close/restart session actions use the modal confirm instead of the toast+undo path, on every surface that offers them
	Notifications        NotificationConfig   `toml:"notifications"`
	Preview              PreviewConfig        `toml:"preview"`
	Overview             OverviewConfig       `toml:"overview"`        // live session overview (F2) opening mode
	Jump                 JumpConfig           `toml:"jump"`            // deprecated: the bracket chords it configured are retired; accepted, validated, and read by nothing
	Editor               string               `toml:"editor"`          // overrides $VISUAL/$EDITOR for the "Open editor" session action and the worktrees browser
	PlansDirectory       string               `toml:"plans_directory"` // root for the Plans browser's index; defaults to ~/.local/share/agent-plans
	ASL                  ASLConfig            `toml:"asl"`             // optional agent-session-loop bridge integration
	Release              ReleaseConfig        `toml:"release"`         // paired ASL/Agent Mux release gate
	LaunchBackend        string               `toml:"launch_backend"`  // "kitty" (default) or "tmux": where launches go when --backend is not given
	Tmux                 TmuxConfig           `toml:"tmux"`            // the private tmux backend's executable

	// GitProbe enables the cheap per-directory Git probe behind the dashboard's
	// branch column (internal/gitstate). *bool so the load path can distinguish
	// "user omitted the key" (nil → default true) from "user set
	// git_probe = false" (&false → the daemon runs no git subprocess at all).
	//
	// It exists because the probe's cost is not uniform: on a local checkout it
	// is two short commands per session directory per ten seconds, and on a
	// network filesystem it is two round trips that can take the whole probe
	// timeout. Anyone paying the second price must be able to buy the column
	// back out, and the only honest way to do that is to stop running git.
	GitProbe *bool `toml:"git_probe"`
	// GitProbeTTL and GitProbeTimeout tune that probe for the same users,
	// in seconds: how long an answer is reused, and how long one probe may
	// take. Zero keeps the gitstate defaults (10s and 2s). A slow mount that
	// still wants the column raises both instead of turning it off.
	GitProbeTTL     int `toml:"git_probe_ttl"`
	GitProbeTimeout int `toml:"git_probe_timeout"`
}

// GitProbeValue reports whether the per-directory Git probe runs. An omitted
// key means enabled, matching the other *bool toggles here.
func (d Defaults) GitProbeValue() bool { return d.GitProbe == nil || *d.GitProbe }

// TmuxConfig configures the private tmux backend.
type TmuxConfig struct {
	// Path names the tmux executable to run, instead of the one bundled with
	// Agent Mux.app or found on PATH. It must be absolute (a leading ~ is
	// expanded). When set it is the only one tried.
	Path string `toml:"path"`
}

// The launch backends `launch_backend` and --backend accept.
const (
	LaunchBackendKitty = "kitty"
	LaunchBackendTmux  = "tmux"
)

// LaunchBackendOrDefault is where a launch goes when nothing on the command
// line says otherwise: `[defaults] launch_backend`, else Kitty, so a
// configuration written before the tmux backend existed keeps launching into
// Kitty.
func (c *Config) LaunchBackendOrDefault() string {
	if c != nil && strings.EqualFold(strings.TrimSpace(c.Defaults.LaunchBackend), LaunchBackendTmux) {
		return LaunchBackendTmux
	}
	return LaunchBackendKitty
}

// TmuxPath is `[defaults.tmux] path` with a leading ~ expanded; empty when
// the operator named no executable.
func (c *Config) TmuxPath() string {
	if c == nil {
		return ""
	}
	path := strings.TrimSpace(c.Defaults.Tmux.Path)
	if path == "" {
		return ""
	}
	return ExpandTilde(path)
}

// ReleaseConfig configures the paired release gate. It is operator tooling, not
// a runtime feature: nothing in the daemon, the dashboard, or any session path
// reads it, and an installation that never cuts a release can leave it unset.
type ReleaseConfig struct {
	// EvidenceDir is where signed release records are stored. Empty derives
	// ~/.local/share/agent-mux/release-evidence at use time rather than being
	// filled in on load, so a record's location is decided by the machine that
	// runs the gate rather than by whichever machine last wrote the config.
	EvidenceDir string `toml:"evidence_dir"`
}

// ASLConfig controls the optional agent-session-loop bridge. Enabled is a
// pointer boolean because the three states are observably different: omitted
// probes and stays quiet when ASL is absent, explicit true reports an
// unavailable integration, and explicit false never starts a subprocess at all.
type ASLConfig struct {
	Enabled *bool  `toml:"enabled"`
	Command string `toml:"command"`
	// StateRoot is the canonical directory agent-session-loop persists run
	// state under. Discovery reads the projection from there, so a value that
	// disagrees with ASL's own makes every run look absent rather than
	// producing a visible error. It is left empty by default and derived from
	// XDG at read time; pairing records the peer's actual root here so the two
	// sides cannot silently drift apart.
	StateRoot string `toml:"state_root"`
}

// ASLModeValue reports the tri-state as one of "auto", "required", or
// "disabled".
func (a ASLConfig) ASLModeValue() string {
	switch {
	case a.Enabled == nil:
		return "auto"
	case *a.Enabled:
		return "required"
	default:
		return "disabled"
	}
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

// JumpConfig is retired and influences nothing.
//
// It named the session states the TUI's bracket navigation (]/[ and }/{)
// treated as "attention". Those chords are unbound at the dashboard now, and
// what replaced them takes no configuration: `n`/`N` walk the Needs You group
// in the order the list already shows it, so the set of states that counts as
// attention is the grouping rule itself rather than a second, separately
// configured one that could disagree with it.
//
// The field survives so a config written against the old dashboard still loads:
// applyDefaults still fills AttentionStates with the canonical set
// ["key", "error", "warning", "question"] when it is empty, and validation
// still rejects unknown state names (anything outside palette.StateOrder()), so
// a file that was valid yesterday is valid today and a typo is still reported
// rather than silently absorbed. Nothing reads the result. Load records a
// Deprecation for the table whenever it is present, which is what tells the
// operator the setting has stopped selecting anything; see collectDeprecations.
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
	Elicitation          *bool  `toml:"elicitation"` // notify when a session asks for input; nil → follows Permission
	Stop                 bool   `toml:"stop"`
	StopFailure          *bool  `toml:"stop_failure"`
	StaleActive          *bool  `toml:"stale_active"` // notify on stale-active detection; nil → default true
	HookSilence          *bool  `toml:"hook_silence"` // notify on hook-silence detection; nil → default true
	PermissionDebounceMs int    `toml:"permission_debounce_ms"`
	CooldownSeconds      int    `toml:"cooldown_seconds"` // minimum gap per (tab, kind); 0 → default 10
	QuietHours           string `toml:"quiet_hours"`      // "HH:MM-HH:MM" local time, may wrap midnight; "" → never quiet
}

// QuietWindow is a parsed [defaults.notifications].quiet_hours: minutes after
// local midnight. End before Start means the window wraps past midnight.
type QuietWindow struct {
	Start, End int
}

// ParseQuietHours reads "HH:MM-HH:MM". The empty string means no quiet hours
// and returns ok=false with no error. Equal ends are refused: "always quiet"
// is spelled by turning the categories off, not by a window of no width.
func ParseQuietHours(spec string) (window QuietWindow, ok bool, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return QuietWindow{}, false, nil
	}
	from, to, found := strings.Cut(spec, "-")
	if !found {
		return QuietWindow{}, false, fmt.Errorf("quiet_hours %q must look like 22:00-08:00", spec)
	}
	start, err := parseClockMinutes(from)
	if err != nil {
		return QuietWindow{}, false, fmt.Errorf("quiet_hours %q: %w", spec, err)
	}
	end, err := parseClockMinutes(to)
	if err != nil {
		return QuietWindow{}, false, fmt.Errorf("quiet_hours %q: %w", spec, err)
	}
	if start == end {
		return QuietWindow{}, false, fmt.Errorf("quiet_hours %q starts and ends at the same time", spec)
	}
	return QuietWindow{Start: start, End: end}, true, nil
}

// Contains reports whether the local time t falls inside the window. The start
// is inclusive and the end exclusive, so 22:00-08:00 is quiet at 22:00 and
// loud again at 08:00.
func (w QuietWindow) Contains(t time.Time) bool {
	minute := t.Hour()*60 + t.Minute()
	if w.Start < w.End {
		return minute >= w.Start && minute < w.End
	}
	return minute >= w.Start || minute < w.End
}

func parseClockMinutes(clock string) (int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(clock))
	if err != nil {
		return 0, fmt.Errorf("%q is not a 24-hour HH:MM time", strings.TrimSpace(clock))
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

// ElicitationEnabled reports whether "input requested" notifications fire.
// They followed `permission` before they had a key of their own, and an
// omitted key still does, so existing configs keep their behavior.
func (c NotificationConfig) ElicitationEnabled() bool {
	if c.Elicitation == nil {
		return c.Permission
	}
	return *c.Elicitation
}

// AnyEnabled reports whether at least one native-notification category is
// enabled. Nil pointer fields retain their backward-compatible default of true.
func (c NotificationConfig) AnyEnabled() bool {
	return c.Error || c.Permission || c.ElicitationEnabled() || c.Stop ||
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

// OverviewConfig configures the live session overview (`amx overview`, F2).
type OverviewConfig struct {
	// Compact chooses the mode the overview opens in. *bool so an omitted key
	// (nil) keeps the compact default while `compact = false` opens the full
	// preview grid. The `c` toggle still wins once pressed: the daemon remembers
	// it until it restarts.
	Compact *bool `toml:"compact"`
}

// CompactValue reports whether the overview opens compact. An omitted key
// means compact.
func (o OverviewConfig) CompactValue() bool { return o.Compact == nil || *o.Compact }

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

	// Archived keeps a project in config.toml without offering it: the
	// launcher, `project list`, and `project launch` leave it out, and the
	// directory checks in `config validate` and `doctor` do not warn about
	// it, since a project kept for later commonly names a checkout that is
	// gone. It decides nothing about authorization — overseer policy still
	// reads an archived project's declarations, so archiving one can never
	// lift an exclusion — and session rows are still labelled by it.
	Archived bool `toml:"archived"`

	// ASLOverseer is the project's explicit opt-in to overseer supervision of
	// its agent-session-loop runs. *bool rather than bool because the three
	// states are not interchangeable to anything that has to justify acting on
	// someone's repository: omitted is "never asked", explicit false is "asked
	// and declined", and only explicit true is consent. Nothing may collapse
	// the first two into a default, and installation and the overseer itself
	// must never write this key — enrollment is the operator's decision, made
	// in their own config file.
	//
	// Only explicit true grants supervision (see ASLOverseerValue), so an
	// unreadable, absent, or malformed value fails closed. The pointer is
	// deep-copied by cloneSessions so the deprecated [[sessions]] alias can
	// never mutate the canonical [[projects]] answer.
	ASLOverseer *bool `toml:"asl_overseer"`
}

// HooksConfig is the top-level [hooks] table.
type HooksConfig struct {
	Overrides map[string]AccountHookOverride `toml:"overrides"`
}

// AccountHookOverride holds per-account hook customizations.
type AccountHookOverride struct {
	DisabledEvents []string `toml:"disabled_events"`
}

// validSorts is the set of allowed sort values.
var validSorts = map[string]bool{
	"alpha": true, "account": true,
}

// validPreviewDirections is the set of allowed preview directions.
var validPreviewDirections = map[string]bool{
	"horizontal": true, "vertical": true,
}

// ValidColor reports whether s is a valid palette color name. The palette is
// the only list of colors: a name added to palette.json is valid here without
// a second edit.
func ValidColor(s string) bool {
	_, err := palette.Lookup(s)
	return err == nil
}

// validColorsHint is the "→ valid colors: …" line a color error ends with,
// listing the palette's names in sorted order.
func validColorsHint() string {
	return "\n  → valid colors: " + strings.Join(palette.Names(), ", ")
}

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

// ASLOverseerValue reports whether the project opted in to overseer
// supervision. Unlike the other pointer booleans in this file, an omitted key
// means false: those toggles default to their safe observable behavior, and the
// safe behavior for supervision of someone else's repository is to stay out of
// it. False, omitted, and unconfigured are all "excluded"; callers that must
// tell them apart read the ASLOverseer pointer directly.
func (s Session) ASLOverseerValue() bool {
	return s.ASLOverseer != nil && *s.ASLOverseer
}

// ASLOverseerConfigured reports whether the project stated an answer at all.
// It separates "declined" from "never asked" for diagnostics and enrollment
// surfaces; it does not grant anything on its own.
func (s Session) ASLOverseerConfigured() bool { return s.ASLOverseer != nil }

// EffectiveArgs returns the normalized per-project args, preferring args over
// legacy claude_flags.
func (s Session) EffectiveArgs() []string {
	if len(s.Args) > 0 {
		return s.Args
	}
	return s.ClaudeFlags
}
