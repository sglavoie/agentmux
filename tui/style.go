package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/sglavoie/cc-manager-kitty/internal/palette"
)

var (
	styleSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleSearch   = lipgloss.NewStyle().Bold(true)
	styleFooter   = lipgloss.NewStyle().Faint(true)
	styleHeader   = lipgloss.NewStyle().Bold(true)

	// styleFooterUnavailable renders palette entries whose action does not
	// apply to the current cursor row. The dim grey foreground keeps the
	// key visible (so the palette order stays stable) while signaling that
	// pressing it would be a no-op.
	styleFooterUnavailable = lipgloss.NewStyle().Foreground(lipgloss.Color("#5a5a5a"))

	styleAttentionRow = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))

	styleGroupBorderAttention = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleGroupBorderActive    = lipgloss.NewStyle()
	styleGroupBorderIdle      = lipgloss.NewStyle().Faint(true)

	styleGroupLabelAttention = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)
	styleGroupLabelActive    = lipgloss.NewStyle().Bold(true)
	styleGroupLabelIdle      = lipgloss.NewStyle().Faint(true).Bold(true)

	styleEmptyHero = lipgloss.NewStyle().Bold(true)

	// styleChip is the neutral base for substring chips. Account chips use
	// accountStyle(colorName) on top; state chips with attention semantics
	// (key/stop/error) reuse styleAttentionRow. Focused chips overlay an
	// underline regardless of the base color.
	styleChip = lipgloss.NewStyle()

	// Toast severity styles. Info is neutral foreground (no styling so it
	// reads as a status line); success picks up the palette's green, error
	// reuses the inline error red.
	styleToastInfo    = lipgloss.NewStyle()
	styleToastSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleToastError   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
)

// chipStyle returns the rendering style for a chip, layering the focus
// underline on top of the kind-specific base color when applicable.
func chipStyle(c chipComponent, acctColor map[string]string, focused bool) lipgloss.Style {
	var base lipgloss.Style
	switch c.kind {
	case chipAccount:
		base = accountStyle(acctColor[c.raw])
	case chipState:
		switch c.raw {
		case "key", "stop", "error":
			base = styleAttentionRow
		default:
			base = styleChip
		}
	default:
		base = styleChip
	}
	if focused {
		base = base.Underline(true)
	}
	return base
}

// accountStyle returns a Lipgloss style with the account's foreground color.
// Falls back to the default style if the color name is unknown.
func accountStyle(colorName string) lipgloss.Style {
	c, err := palette.Lookup(colorName)
	if err != nil {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.ForegroundHex()))
}
