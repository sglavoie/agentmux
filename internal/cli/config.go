package cli

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/config"
)

// LoadRunnableConfig loads the operator's configuration and refuses to hand
// back one the rest of the CLI cannot act on. Every command family that reads
// configuration goes through it, so "loaded" and "valid" cannot drift apart
// between surfaces.
func LoadRunnableConfig() (*config.Config, error) {
	return loadAndValidate(config.LoadDefault)
}

// loadAndValidate is the load-and-validate boundary itself, parameterized over
// the loader so this package's tests can drive the failure paths without
// staging a config file. It is unexported because no command family needs a
// second loader: they all want the default one.
func loadAndValidate(load func() (*config.Config, error)) (*config.Config, error) {
	cfg, err := load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
