package dashboard

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/theme"
)

// Styles stay local to the dashboard while their colors come from the shared
// adaptive palette. Semantic account and attention colors remain independent.
var (
	// The tinted focused row is one normalized span; blurred focus is quieter.
	styleSelected        = theme.Selected()
	styleSelectedBlurred = theme.Blurred()
	styleText            = lipgloss.NewStyle().Foreground(theme.Color(theme.Text))
	styleDim             = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleFooter          = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleAttentionRow    = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleEmptyHero       = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)

	// styleHeroKey paints the keys inside the empty hero's hint. It is the
	// footer's key color deliberately: the hero names two of the same bindings
	// the footer names, and an operator who has learned to look for teal in the
	// bottom row should not have to learn a second spelling for the middle of
	// the screen.
	styleHeroKey = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary)).Bold(true)

	// stylePreviewError colors the inline error the scrollback preview pane
	// renders when a capture fails.
	stylePreviewError = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))

	// styleUnreadNotice colors the notice gutter's unread-completion dot. It
	// wears the attention accent because an unseen stop is exactly what puts a
	// row in the Needs you bucket: the dot names the reason the row is where it
	// is, so it must not read as a third, unrelated color.
	styleUnreadNotice = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))

	// Per-state accent styles used by the row pulse, the chips, and the
	// legend gloss.
	styleStateBolt    = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary))
	styleStateStop    = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleStateKey     = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleStateWarning = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleStateError   = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))

	styleGroupBorderAttention = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	styleGroupBorderActive    = lipgloss.NewStyle().Foreground(theme.Color(theme.Divider))
	styleGroupBorderIdle      = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))

	styleGroupLabelAttention = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber)).Bold(true)
	styleGroupLabelActive    = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleGroupLabelIdle      = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)

	// styleChip is the neutral base for substring chips. Account chips use
	// accountStyle(colorName) on top; state chips with attention semantics
	// (key/stop/warning/error) pick up their state accent.
	styleChip = lipgloss.NewStyle()

	// styleCompletionPopup keeps non-selected completion rows dim so the menu
	// reads as auxiliary UI.
	styleCompletionPopup = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// background wrapper can terminate the selection early and leave stale
// tinted cells behind when the cursor moves. The selected copy therefore
// keeps only its visible content before applying the shared selection style.
//
// blurred picks which selection the row wears. It is a parameter rather than two
// functions because the normalization is the whole point: a caller that reached
// for the blurred style directly would be a caller that could forget to strip.
// Surfaces with no focus of their own — the completion popup — pass
// false, which is the historical behavior said explicitly.
func renderSelectedRow(row string, blurred bool) string {
	if blurred {
		return styleSelectedBlurred.Render(ansi.Strip(row))
	}
	return styleSelected.Render(ansi.Strip(row))
}

// emphasisStyle maps a queue cell's semantic weight onto the attribute it is
// drawn with. It is the one place the redesign's palette rule — neutral names,
// muted metadata, amber attention, red failures — becomes terminal attributes,
// which is what lets queue.go decide the MEANING with no theme in scope and lets
// a test assert the meaning without a terminal.
//
// Neutral app-rendered status uses near-white; captured output never passes
// through this function and retains its own terminal styling.
func emphasisStyle(e Emphasis) lipgloss.Style {
	switch e {
	case EmphasisMuted:
		return styleDim
	case EmphasisAttention:
		return styleAttentionRow
	case EmphasisFailure:
		return styleStateError
	default:
		return styleText
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

// stateAccentStyle returns the per-state foreground style used to color the
// row pulse and the legend gloss. Unknown / idle states fall back to the dim
// footer style so the accent visually disappears.
func stateAccentStyle(state string) lipgloss.Style {
	switch state {
	case "bolt":
		return styleStateBolt
	case "stop":
		return styleStateStop
	case "key":
		return styleStateKey
	case "warning":
		return styleStateWarning
	case "error":
		return styleStateError
	default:
		return styleFooter
	}
}
