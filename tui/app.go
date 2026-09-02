// Package tui hosts the live-session dashboard: the Bubble Tea model, the
// theme its views render through, and the update/view wiring that turns key
// presses into session actions. Domain leaves under tui/ own the vocabulary
// the model renders — sizing and compositing in tui/layout, session rows and
// the filter DSL in tui/dashboard, event and transcript history in
// tui/history, the launcher in tui/launch, plan rows in tui/plans, worktree
// rows in tui/worktrees, the ASL Runs surface in tui/asl, and command and
// key-action definitions in tui/commands — while what a keystroke DOES to the
// model stays here. Each leaf takes an explicit Input value rather than the
// model, so no leaf imports this package. tui/customization is a sibling
// rather than a leaf: cmd launches its overlays as their own programs.
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
