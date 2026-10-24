package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// RunFromWindow launches the dashboard and initially selects the managed
// session in the Kitty tab containing sourceWindowID when one exists.
func RunFromWindow(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) error {
	m := NewModelFromWindow(cfg, client, sourceWindowID)
	m.configPath = config.DefaultConfigPath()
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}

// RunHistory launches the TUI in Past Sessions with the conversation wanted
// selected once it is listed; the zero identity selects nothing. It has no session list to return to: leaving
// Past Sessions quits.
func RunHistory(cfg *config.Config, client kitty.KittyClient, wanted conversation.Identity) error {
	m := NewHistoryModel(cfg, client, wanted)
	m.configPath = config.DefaultConfigPath()
	_, err := tea.NewProgram(m).Run()
	return err
}

// RunHistorySearch carries the native conversation browser's query into the
// existing terminal actions without losing the selected conversation.
func RunHistorySearch(cfg *config.Config, client kitty.KittyClient, wanted conversation.Identity, query string) error {
	m := NewHistoryModelWithQuery(cfg, client, wanted, query)
	m.configPath = config.DefaultConfigPath()
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
