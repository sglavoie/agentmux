package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/sglavoie/cc-manager-kitty/internal/palette"
)

var (
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleDir      = lipgloss.NewStyle().Faint(true)
	styleSearch   = lipgloss.NewStyle().Bold(true)
	styleFooter   = lipgloss.NewStyle().Faint(true)
	styleHeader   = lipgloss.NewStyle().Bold(true)

	styleAttentionDivider = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleAttentionRow     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleActiveDivider    = lipgloss.NewStyle()
	styleIdleDivider      = lipgloss.NewStyle().Faint(true)
)

// accountStyle returns a Lipgloss style with the account's foreground color.
// Falls back to the default style if the color name is unknown.
func accountStyle(colorName string) lipgloss.Style {
	c, err := palette.Lookup(colorName)
	if err != nil {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.ForegroundHex()))
}
