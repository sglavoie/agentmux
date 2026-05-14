package config

// Config is the top-level structure for config.toml.
type Config struct {
	Defaults Defaults             `toml:"defaults"`
	Accounts []Account            `toml:"accounts"`
	Sessions []Session            `toml:"sessions"`
	Hooks    HooksConfig          `toml:"hooks"`
}

// Defaults holds global default settings.
type Defaults struct {
	Sort            string              `toml:"sort"`
	RefreshInterval int                 `toml:"refresh_interval"`
	GuardDuration   int                 `toml:"guard_duration"`
	IdleThreshold   int                 `toml:"idle_threshold"` // seconds before a session is considered idle; 0 → default (300)
	ClaudeFlags     []string            `toml:"claude_flags"`
	Notifications   NotificationConfig  `toml:"notifications"`
}

// NotificationConfig controls which macOS notifications fire.
type NotificationConfig struct {
	Error      bool `toml:"error"`
	Permission bool `toml:"permission"`
	Stop       bool `toml:"stop"`
}

// Account defines a Claude account.
type Account struct {
	Name      string `toml:"name"`
	ConfigDir string `toml:"config_dir"`
	Color     string `toml:"color"`
}

// Session defines a single Claude Code session tab.
type Session struct {
	Title          string            `toml:"title"`
	Directory      string            `toml:"directory"`
	Account        string            `toml:"account"`
	ClaudeFlags    []string          `toml:"claude_flags"`
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

// ValidColor reports whether s is a valid palette color name.
func ValidColor(s string) bool { return validColors[s] }

// ValidSort reports whether s is a valid sort value.
func ValidSort(s string) bool { return validSorts[s] }
