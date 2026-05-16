package config

import (
	"os"

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
	applyDefaults(&cfg)
	return &cfg, nil
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
	// StopFailure defaults to true so configs predating this field still
	// surface terminal-failure notifications. See NotificationConfig docs for
	// the opt-out path (per-account hooks override).
	cfg.Defaults.Notifications.StopFailure = true
	if cfg.Defaults.Notifications.PermissionDebounceMs == 0 {
		cfg.Defaults.Notifications.PermissionDebounceMs = 500
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
