package dashboard

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/palette"
)

// The styles below are the dashboard's private copy of the presentation
// attributes it paints with. They deliberately duplicate the corresponding
// entries in the root package's theme rather than importing it: the root
// theme is a ~50-style private surface shared by every TUI mode, and
// exporting it to serve a leaf would give every leaf a theme edge that the
// intended import graph does not draw. Only the attributes the dashboard
// itself renders are duplicated here.
var (
	// styleSelected marks the row the keyboard is on. Reverse video and nothing
	// else: it used to double reverse with bold, which is two claims about the
	// same row, and "restrained selection" is the redesign's word for spending one.
	//
	// Reverse rather than a background color on purpose. A hex background is a bet
	// on the terminal's own background being dark, and a wrong bet paints dark
	// text on a dark bar; reverse is defined against whatever the terminal already
	// uses, so it is legible in both themes without the theme having to be known.
	styleSelected = lipgloss.NewStyle().Reverse(true)
	// styleSelectedBlurred marks the same row when the keys are somewhere else.
	// Weight alone, with no reverse bar: the selection is still where the operator
	// left it, but the pane it is in will not answer j/k. Distinguishing the two is
	// what keeps a bar from promising navigation a blurred list cannot perform.
	styleSelectedBlurred = lipgloss.NewStyle().Bold(true)
	styleDim             = lipgloss.NewStyle().Faint(true)
	styleFooter          = lipgloss.NewStyle().Faint(true)
	styleAttentionRow    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleEmptyHero       = lipgloss.NewStyle().Bold(true)

	// stylePreviewError colors the inline error the scrollback preview pane
	// renders when a capture fails.
	stylePreviewError = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))

	// styleUnreadNotice colors the notice gutter's unread-completion dot. It
	// wears the attention accent because an unseen stop is exactly what puts a
	// row in the Needs you bucket: the dot names the reason the row is where it
	// is, so it must not read as a third, unrelated color.
	styleUnreadNotice = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))

	// Per-state accent styles used by the row pulse, the chips, and the
	// legend gloss.
	styleStateBolt    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleStateStop    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateError   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))

	styleGroupBorderAttention = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleGroupBorderActive    = lipgloss.NewStyle()
	styleGroupBorderIdle      = lipgloss.NewStyle().Faint(true)

	styleGroupLabelAttention = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)
	styleGroupLabelActive    = lipgloss.NewStyle().Bold(true)
	styleGroupLabelIdle      = lipgloss.NewStyle().Faint(true).Bold(true)

	// styleChip is the neutral base for substring chips. Account chips use
	// accountStyle(colorName) on top; state chips with attention semantics
	// (key/stop/warning/error) pick up their state accent.
	styleChip = lipgloss.NewStyle()

	// styleCompletionPopup keeps non-selected completion rows dim so the menu
	// reads as auxiliary UI.
	styleCompletionPopup = lipgloss.NewStyle().Faint(true)
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// reverse-video wrapper can terminate the selection early and leave stale
// inverse cells behind when the cursor moves. The selected copy therefore
// keeps only its visible content before applying the shared selection style.
//
// blurred picks which selection the row wears. It is a parameter rather than two
// functions because the normalization is the whole point: a caller that reached
// for the blurred style directly would be a caller that could forget to strip.
// Surfaces with no focus of their own — the completion popup, the rail — pass
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
// Neutral is the zero style rather than an explicit foreground. A row that is
// simply working should read as ordinary text, and an explicit "default"
// foreground is a color that fights the operator's own terminal theme.
func emphasisStyle(e Emphasis) lipgloss.Style {
	switch e {
	case EmphasisMuted:
		return styleDim
	case EmphasisAttention:
		return styleAttentionRow
	case EmphasisFailure:
		return styleStateError
	default:
		return lipgloss.NewStyle()
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
