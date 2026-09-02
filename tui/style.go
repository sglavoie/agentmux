package tui

import (
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/palette"
)

const footerStripeBackgroundHex = "#2b2b2b"

var (
	styleSelected  = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleError     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleDim       = lipgloss.NewStyle().Faint(true)
	styleSearch    = lipgloss.NewStyle().Bold(true)
	styleFooter    = lipgloss.NewStyle().Faint(true)
	styleHeader    = lipgloss.NewStyle().Bold(true)
	styleFooterKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4ec9b0")).
			Bold(true)

	// styleOverlayBox wraps overlay popups (chip picker, delete-choice
	// prompt) in a rounded border with the de-facto accent teal. Padding
	// adds a single column of breathing room on each side; combined with
	// the border, the box adds 2 columns + 2 rows of chrome to inner
	// content.
	styleOverlayBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4ec9b0")).
			Padding(0, 1)

	// styleFooterUnavailable renders actions that do not apply to the current
	// cursor row. The dim grey foreground keeps the key visible while signaling
	// that pressing it would be a no-op.
	styleFooterUnavailable    = lipgloss.NewStyle().Foreground(lipgloss.Color("#5a5a5a"))
	styleFooterUnavailableKey = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#5a5a5a")).
					Bold(true)

	styleAttentionRow = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))

	// Per-state accent styles used by the footer gloss. bolt picks up the
	// success/active teal; stop/key/warning reuse the attention orange; terminal
	// error stays on the stronger red accent.
	styleStateBolt    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleStateStop    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateError   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))

	// styleChip is the neutral base for substring chips. Account chips use
	// accountStyle(colorName) on top; state chips with attention semantics
	// (key/stop/warning/error) pick up their state accent. Focused chips overlay
	// an underline regardless of the base color.
	styleChip = lipgloss.NewStyle()

	// Status bar styles own the fixed bottom row. Each style sets a
	// background so the padded row reads as a full-width mode/status strip.
	styleStatusIdle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#d4d4d4")).
			Background(lipgloss.Color("#1f2933"))
	styleStatusInfo = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#d8f3ff")).
			Background(lipgloss.Color("#12313b"))
	styleStatusSuccess = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#d8ffe8")).
				Background(lipgloss.Color("#123927"))
	styleStatusWarning = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fff1b8")).
				Background(lipgloss.Color("#3a2d05"))
	styleStatusError = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffe1e1")).
				Background(lipgloss.Color("#3a1515"))

	// Health pip and deprecation banner styles. OK reuses the active-teal
	// from styleStateBolt, Warn reuses the attention amber, and Err reuses
	// the inline error red. styleBanner is a yellow-foreground accent so it
	// reads as a warning without consuming a full background block.
	stylePipOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	stylePipWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	stylePipErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleBanner  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Bold(true)

	// Detail-pane styles (ModeDetail). detailRowStyle is intentionally
	// neutral so row text inherits the terminal's default foreground;
	// keeping the call site explicit makes it easy to recolor every row
	// later without hunting through detail.go. detailToolStyle picks up the
	// active-teal already established for `bolt` so the tool-name column
	// reads as an accent without introducing a new palette color.
	detailRowStyle  = lipgloss.NewStyle()
	detailToolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
)

// Timeline phase header and annotation styles (ModeDetail timeline sub-view).
var (
	styleTimelineActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#4ec9b0"))
	styleTimelineCompact  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffaa00"))
	styleTimelineStop     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleTimelineError    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff5555"))
	styleTimelineGap      = lipgloss.NewStyle().Faint(true)
	styleTimelineSep      = lipgloss.NewStyle().Faint(true)
	styleTimelineTree     = lipgloss.NewStyle().Faint(true)
	styleTimelineTool     = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleTimelineExpanded = lipgloss.NewStyle().Faint(true).Italic(true)
)

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
	case "warning":
		return styleStateWarning
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
