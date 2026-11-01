package config

import (
	"fmt"
	"strings"
)

// RecapConfig chooses one summarizer for conversations from every harness.
// Profile omitted selects the first configured Claude profile. No profile's
// launch args are inherited: recap generation has its own tool-free invocation.
type RecapConfig struct {
	Enabled *bool  `toml:"enabled"`
	Profile string `toml:"profile"`
	Model   string `toml:"model"`
}

func (r RecapConfig) EnabledValue() bool { return r.Enabled == nil || *r.Enabled }

func (c *Config) RecapProfile() (Account, error) {
	for _, p := range c.Profiles {
		if c.Defaults.Recaps.Profile != "" && p.Name != c.Defaults.Recaps.Profile {
			continue
		}
		if p.Harness == "claude" {
			return p, nil
		}
		if c.Defaults.Recaps.Profile != "" {
			return Account{}, fmt.Errorf("recaps profile %q must use Claude", p.Name)
		}
	}
	return Account{}, fmt.Errorf("recaps need a configured Claude profile; set [defaults.recaps] profile to its name")
}

func validateRecaps(c *Config) []string {
	if c.Defaults.Recaps.Profile != "" {
		if _, err := c.RecapProfile(); err != nil {
			return []string{err.Error()}
		}
	}
	if strings.HasPrefix(c.Defaults.Recaps.Model, "-") || len(c.Defaults.Recaps.Model) > 200 {
		return []string{"defaults.recaps.model must be a model name, not a CLI option"}
	}
	return nil
}
