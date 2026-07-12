package tui

import (
	"context"
	"errors"

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
func RunFromWindow(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) (retErr error) {
	ctx, cancel := context.WithCancel(context.Background())
	services := NewDataServices()
	defer func() {
		cancel()
		retErr = errors.Join(retErr, services.Close())
	}()
	m := newModelWithDataServices(ctx, cfg, client, sourceWindowID, ModeWorkspace, services)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, retErr = p.Run()
	return retErr
}
