package cmd

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/config"
)

func loadRunnableConfig() (*config.Config, error) {
	return loadAndValidateConfig(config.LoadDefault)
}

func loadAndValidateConfig(load func() (*config.Config, error)) (*config.Config, error) {
	cfg, err := load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
