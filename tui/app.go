package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
)

// Run launches the dashboard TUI. It blocks until the user quits.
func Run(cfg *config.Config, client kitty.KittyClient) error {
	m := NewModel(cfg, client)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
