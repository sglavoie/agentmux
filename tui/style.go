package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/sglavoie/agentmux/internal/palette"
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

	// styleAgeWarning colors the per-row age cell when a sweep-eligible
	// session has been silent for ≥ 80% of its resolved threshold. Reuses
	// the attention amber so the cue reads as "approaching stale" without
	// the full alarm weight of styleError.
	styleAgeWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))

	// Per-state accent styles used by the footer gloss. bolt picks up the
	// success/active teal; key/stop/error reuse the attention orange so the
	// gloss text matches the icon family the user already associates with
	// each state.
	styleStateBolt  = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleStateStop  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateKey   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateError = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))

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

	// Health pip and deprecation banner styles. OK reuses the active-teal
	// from styleStateBolt, Warn reuses the attention amber, and Err reuses
	// the inline error red. styleBanner is a yellow-foreground accent so it
	// reads as a warning without consuming a full background block.
	stylePipOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	stylePipWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	stylePipErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleBanner  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)

	// Completion popup styles. The selected row inverts so it pops without
	// recoloring; non-selected rows use the dim footer style so the menu
	// reads as auxiliary UI rather than competing with the input bar.
	styleCompletionPopup    = lipgloss.NewStyle().Faint(true)
	styleCompletionSelected = lipgloss.NewStyle().Bold(true).Reverse(true)
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

// stateAccentStyle returns the per-state foreground style used to color the
// gloss text in the footer. Unknown / idle states fall back to the dim footer
// style so the gloss visually disappears.
func stateAccentStyle(state string) lipgloss.Style {
	switch state {
	case "bolt":
		return styleStateBolt
	case "stop":
		return styleStateStop
	case "key":
		return styleStateKey
	case "error":
		return styleStateError
	default:
		return styleFooter
	}
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
