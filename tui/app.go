package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// Run launches the dashboard TUI. It blocks until the user quits.
func Run(cfg *config.Config, client kitty.KittyClient) error {
	return RunFromWindow(cfg, client, 0)
}

// RunFromWindow launches the dashboard and initially selects the managed
// session in the Kitty tab containing sourceWindowID when one exists.
func RunFromWindow(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) error {
	m := NewModelFromWindow(cfg, client, sourceWindowID)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
