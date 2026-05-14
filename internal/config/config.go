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
}

// ExampleConfig is the content written by ccm config init.
var ExampleConfig = string(ccembed.ExampleConfig)
