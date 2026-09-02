// This file is the root's visual vocabulary: the lipgloss theme every root
// renderer paints with, the normalized selected-row renderer every selectable
// list routes its highlight through, and the small shared text helpers —
// pluralization, rune clamping, relative times, title display, the search bar,
// and the path row — that draw with that theme.

package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/layout"
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

	// styleFooterUnavailableKey renders actions that do not apply to the current
	// cursor row. The dim grey foreground keeps the key visible while signaling
	// that pressing it would be a no-op.
	styleFooterUnavailableKey = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#5a5a5a")).
					Bold(true)

	// Per-state accent styles used by the footer gloss. bolt picks up the
	// success/active teal; stop/key/warning reuse the attention orange; terminal
	// error stays on the stronger red accent.
	styleStateBolt    = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleStateStop    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
	styleStateError   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))

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

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// reverse-video wrapper can terminate the selection early and leave stale
// inverse cells behind when the cursor moves. The selected copy therefore
// keeps only its visible content before applying the shared selection style.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}

// The helpers below are the root's shared count and timestamp labels. They
// used to live alongside the Past Sessions list; that view moved into
// tui/history, which carries its own private copies, so what remains here is
// what the plan search, the plan viewer, and the status bar still need.

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// formatSessionWhen returns a short human-readable representation of a
// timestamp. Entries newer than 24h show as "Xh", older up to 7d as "Xd", and
// anything older falls back to YYYY-MM-DD.
func formatSessionWhen(t time.Time) string {
	if t.IsZero() {
		return "          "
	}
	delta := time.Since(t)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < time.Hour:
		mins := int(delta.Minutes())
		if mins < 1 && delta > 0 {
			mins = 1
		}
		return fmt.Sprintf("%2dm   ago", mins)
	case delta < 24*time.Hour:
		return fmt.Sprintf("%2dh   ago", int(delta.Hours()))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("%2dd   ago", int(delta.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func renderPathRow(label, path string, maxWidth int) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = 120
	}
	row := "  " + label + ": " + path
	return styleDim.Render(layout.Truncate(row, maxWidth, "…"))
}

func selectedSessionSearchPath(m Model) string {
	if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
		return ""
	}
	return m.sessionResults[m.sessionCursor].Meta.Path
}

func currentSessionViewPath(m Model) string {
	if m.viewerSession == nil {
		return ""
	}
	return m.viewerSession.Path
}

func selectedPlanSearchPath(m Model) string {
	if m.planCursor < 0 || m.planCursor >= len(m.planResults) {
		return ""
	}
	return m.planResults[m.planCursor].Meta.DirPath
}

func currentPlanViewPath(m Model) string {
	if m.viewerPlan == nil {
		return ""
	}
	return m.viewerPlan.DirPath
}

func selectedWorktreePath(m Model) string {
	if m.worktreeCursor < 0 || m.worktreeCursor >= len(m.worktreeFiltered) {
		return ""
	}
	return m.worktreeFiltered[m.worktreeCursor].Path
}

// renderSearch renders the filter input bar (shown when in ModeSearch) and
// the sigil-aware completion popup when it is open. The DSL is documented in
// tui/filter.go.
func renderSearch(m Model) string {
	prompt := styleSearch.Render("/") + m.filterText + "█"
	out := prompt + "\n"
	if popup := renderCompletion(m); popup != "" {
		out += popup
	}
	return out
}

func configSessionDisplayTitle(sess config.Session) string {
	return sess.Title
}
