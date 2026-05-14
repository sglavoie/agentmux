package config

import (
	"os"

	"github.com/BurntSushi/toml"
	ccembed "github.com/sglavoie/cc-manager-kitty/embed"
)

// Load reads and parses config.toml from path, applying defaults for unset fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
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
}

// ExampleConfig is the content written by ccm config init.
var ExampleConfig = string(ccembed.ExampleConfig)
