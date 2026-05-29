package config

import (
	"os"
	"sort"

	"github.com/BurntSushi/toml"
	ccembed "github.com/sglavoie/agentmux/embed"
	"github.com/sglavoie/agentmux/internal/harness"
)

// Load reads and parses config.toml from path, applying defaults for unset fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseConfig(data)
}

// LoadDefault reads and parses config.toml from the default path, returning an
// actionable error if the file has not been created yet.
func LoadDefault() (*Config, error) {
	data, err := ReadDefault()
	if err != nil {
		return nil, err
	}
	return parseConfig(data)
}

func parseConfig(data []byte) (*Config, error) {
	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, err
	}
	cfg.Deprecations = collectDeprecations(&cfg)
	applyDefaults(&cfg)
	return &cfg, nil
}

// collectDeprecations inspects the raw decoded config and records any
// legacy TOML keys present. Must be called before normalizeAliases, which
// mirrors deprecated/canonical fields and erases the distinction.
func collectDeprecations(cfg *Config) []Deprecation {
	var deps []Deprecation
	// Report the deprecated key whenever it is populated. When the canonical
	// form is *also* populated, normalizeAliases silently discards the
	// deprecated entries (canonical wins), so users need to know their
	// [[accounts]] / [[sessions]] data is being ignored — not only when they
	// use the legacy form exclusively.
	if len(cfg.Accounts) > 0 {
		deps = append(deps, Deprecation{
			Key:         "accounts",
			Replacement: "profiles",
			Location:    "[[accounts]]",
		})
	}
	if len(cfg.Sessions) > 0 {
		deps = append(deps, Deprecation{
			Key:         "sessions",
			Replacement: "projects",
			Location:    "[[sessions]]",
		})
	}
	if len(cfg.Defaults.ClaudeFlags) > 0 {
		deps = append(deps, Deprecation{
			Key:         "defaults.claude_flags",
			Replacement: "defaults.args",
			Location:    "[defaults]",
		})
	}
	// Surface the legacy stale_active_threshold regardless of whether the
	// user also wrote [defaults.watchdog]: when both are present the new
	// block wins (per applyDefaults), and the user should still be told the
	// legacy key has moved so they can drop it on their next edit.
	if cfg.Defaults.StaleActiveThreshold != 0 {
		deps = append(deps, Deprecation{
			Key:         "defaults.stale_active_threshold",
			Replacement: "defaults.watchdog.active",
			Location:    "[defaults]",
		})
	}
	// Per-project: scan both Projects and Sessions for legacy claude_flags
	// and account keys. Report the deprecated key whenever it is populated;
	// when canonical fields are also populated, canonical wins and the legacy
	// value can otherwise look like it is still doing something.
	projectSeen := false
	accountSeen := false
	scan := func(items []Session, table string) {
		for _, p := range items {
			if !projectSeen && len(p.ClaudeFlags) > 0 {
				deps = append(deps, Deprecation{
					Key:         table + ".claude_flags",
					Replacement: table + ".args",
					Location:    table + " " + p.Title,
				})
				projectSeen = true
			}
			if !accountSeen && p.Account != "" {
				deps = append(deps, Deprecation{
					Key:         table + ".account",
					Replacement: table + ".profile",
					Location:    table + " " + p.Title,
				})
				accountSeen = true
			}
		}
	}
	scan(cfg.Projects, "[[projects]]")
	scan(cfg.Sessions, "[[sessions]]")
	return deps
}

func applyDefaults(cfg *Config) {
	if cfg.Defaults.Sort == "" {
		cfg.Defaults.Sort = "alpha"
	}
	if cfg.Defaults.RefreshInterval == 0 {
		cfg.Defaults.RefreshInterval = 2
	}
	if cfg.Defaults.GuardDuration == 0 {
		cfg.Defaults.GuardDuration = 1
	}
	if cfg.Defaults.IdleThreshold == 0 {
		cfg.Defaults.IdleThreshold = 300
	}
	if cfg.Defaults.StaleActiveThreshold == 0 {
		cfg.Defaults.StaleActiveThreshold = 600
	}
	// Mirror legacy stale_active_threshold into Watchdog.Active when the new
	// block omits the field. Done per-field so a user who writes
	// `[defaults.watchdog]` for just `subagent` still inherits their legacy
	// active value rather than getting silently reset to 600.
	if cfg.Defaults.Watchdog.Active == 0 && cfg.Defaults.StaleActiveThreshold > 0 {
		cfg.Defaults.Watchdog.Active = cfg.Defaults.StaleActiveThreshold
	}
	if cfg.Defaults.Watchdog.Active == 0 {
		cfg.Defaults.Watchdog.Active = 600
	}
	if cfg.Defaults.Watchdog.Subagent == 0 {
		cfg.Defaults.Watchdog.Subagent = 1800
	}
	if cfg.Defaults.Watchdog.Compact == 0 {
		cfg.Defaults.Watchdog.Compact = 180
	}
	if cfg.Defaults.Watchdog.HookSilence == 0 {
		cfg.Defaults.Watchdog.HookSilence = 14400
	}
	if cfg.Defaults.Watchdog.ClaudeContextLimitProbe == nil {
		t := true
		cfg.Defaults.Watchdog.ClaudeContextLimitProbe = &t
	}
	if cfg.Defaults.SessionArchive.KeepDays == 0 {
		cfg.Defaults.SessionArchive.KeepDays = 30
	}
	if cfg.Defaults.SessionArchive.ScanMaxAgeDays == 0 {
		cfg.Defaults.SessionArchive.ScanMaxAgeDays = cfg.Defaults.SessionArchive.KeepDays
	}
	if cfg.Defaults.Preview.Direction == "" {
		cfg.Defaults.Preview.Direction = "horizontal"
	}
	if cfg.Defaults.Preview.Ratio == 0 {
		cfg.Defaults.Preview.Ratio = 0.5
	}
	if cfg.Defaults.Preview.RefreshMs == 0 {
		cfg.Defaults.Preview.RefreshMs = 500
	}
	if cfg.Defaults.Preview.MinCols == 0 {
		cfg.Defaults.Preview.MinCols = 100
	}
	if cfg.Defaults.Preview.MinRows == 0 {
		cfg.Defaults.Preview.MinRows = 30
	}
	if cfg.Defaults.Notifications.StopFailure == nil {
		t := true
		cfg.Defaults.Notifications.StopFailure = &t
	}
	if cfg.Defaults.Notifications.StaleActive == nil {
		t := true
		cfg.Defaults.Notifications.StaleActive = &t
	}
	if cfg.Defaults.Notifications.HookSilence == nil {
		t := true
		cfg.Defaults.Notifications.HookSilence = &t
	}
	if cfg.Defaults.Notifications.PermissionDebounceMs == 0 {
		cfg.Defaults.Notifications.PermissionDebounceMs = 500
	}
	if cfg.Defaults.PlansDirectory == "" {
		cfg.Defaults.PlansDirectory = "~/.local/share/agent-plans"
	}

	normalizeAliases(cfg)
}

// ExampleConfig is the starter config content written by amx config init.
var ExampleConfig = string(ccembed.ExampleConfig)

func normalizeAliases(cfg *Config) {
	if len(cfg.Defaults.Args) == 0 && len(cfg.Defaults.ClaudeFlags) > 0 {
		cfg.Defaults.Args = cloneStrings(cfg.Defaults.ClaudeFlags)
	}
	if len(cfg.Defaults.ClaudeFlags) == 0 && len(cfg.Defaults.Args) > 0 {
		cfg.Defaults.ClaudeFlags = cloneStrings(cfg.Defaults.Args)
	}

	cfg.HarnessColors = normalizeHarnessColors(cfg.HarnessColors)

	profiles := cfg.Profiles
	if len(profiles) == 0 {
		profiles = cfg.Accounts
	}
	cfg.Profiles = normalizeProfiles(profiles)
	cfg.Accounts = cloneAccounts(cfg.Profiles)

	projects := cfg.Projects
	if len(projects) == 0 {
		projects = cfg.Sessions
	}
	cfg.Projects = normalizeProjects(projects)
	cfg.Sessions = cloneSessions(cfg.Projects)
}

func normalizeProfiles(in []Account) []Account {
	out := cloneAccounts(in)
	for i := range out {
		if out[i].Harness == "" {
			out[i].Harness = harness.Claude
		}
		out[i].Harness = harness.Normalize(out[i].Harness)
		if out[i].HomeDir == "" {
			out[i].HomeDir = out[i].ConfigDir
		}
		if out[i].HomeDir == "" {
			out[i].HomeDir = harness.DefaultHomeDir(out[i].Harness)
		}
		if out[i].ConfigDir == "" {
			out[i].ConfigDir = out[i].HomeDir
		}
	}
	return out
}

func normalizeProjects(in []Session) []Session {
	out := cloneSessions(in)
	for i := range out {
		if out[i].Profile == "" {
			out[i].Profile = out[i].Account
		}
		if out[i].Account == "" {
			out[i].Account = out[i].Profile
		}
		if len(out[i].Args) == 0 && len(out[i].ClaudeFlags) > 0 {
			out[i].Args = cloneStrings(out[i].ClaudeFlags)
		}
		if len(out[i].ClaudeFlags) == 0 && len(out[i].Args) > 0 {
			out[i].ClaudeFlags = cloneStrings(out[i].Args)
		}
	}
	return out
}

func cloneAccounts(in []Account) []Account {
	if len(in) == 0 {
		return nil
	}
	out := make([]Account, len(in))
	copy(out, in)
	for i := range out {
		out[i].Args = cloneStrings(out[i].Args)
	}
	return out
}

func normalizeHarnessColors(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(in))
	for _, k := range keys {
		out[harness.Normalize(k)] = in[k]
	}
	return out
}

func cloneSessions(in []Session) []Session {
	if len(in) == 0 {
		return nil
	}
	out := make([]Session, len(in))
	copy(out, in)
	for i := range out {
		out[i].Args = cloneStrings(out[i].Args)
		out[i].ClaudeFlags = cloneStrings(out[i].ClaudeFlags)
		out[i].Groups = cloneStrings(out[i].Groups)
		out[i].Env = cloneStringMap(out[i].Env)
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
