// Package dashboard provides the interactive dashboard, overview, history,
// and customization surfaces opened by amx in a tmux terminal.
package dashboard

import (
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui"
)

// Run starts the dashboard in this terminal.
func Run(backendName string, view View) error {
	cfg, err := loadRunnableConfig()
	if err != nil {
		return err
	}
	if err := selectTerminalsForConfig(backendName, cfg); err != nil {
		return err
	}
	selected, err := terminalView(view)
	if err != nil {
		return err
	}
	return runDashboard(cfg, selected)
}

var (
	runDashboard = func(cfg *config.Config, selected int) error {
		return tui.RunFromWindow(cfg, nil, selected)
	}
	loadRunnableConfig = cli.LoadRunnableConfig
)
