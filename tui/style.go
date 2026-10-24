// This file is the root's visual vocabulary: the lipgloss theme every root
// renderer paints with, the normalized selected-row renderer every selectable
// list routes its highlight through, and the small shared text helpers —
// pluralization, rune clamping, title display, the search bar,
// and the path row — that draw with that theme.

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

var (
	styleSelected  = theme.Selected()
	styleError     = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
	styleDim       = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleSearch    = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary)).Bold(true)
	styleFooter    = chrome.FooterLabel
	styleHeader    = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleFooterKey = chrome.FooterKey

	// Popups retain their existing one-column padding and border geometry.
	styleOverlayBox = theme.Popup()

	// styleFooterUnavailableKey renders actions that do not apply to the current
	// cursor row. The dim grey foreground keeps the key visible while signaling
	// that pressing it would be a no-op.
	styleFooterUnavailableKey = chrome.FooterUnavailableKey

	// Status bar styles own the fixed bottom row. Each style sets a
	// background so the padded row reads as a full-width mode/status strip.
	styleStatusIdle = lipgloss.NewStyle().Foreground(theme.Color(theme.Text)).Background(theme.Color(theme.Footer))
	styleStatusInfo = lipgloss.NewStyle().
			Foreground(theme.Color(theme.Secondary)).
			Background(theme.Color(theme.Footer))
	styleStatusSuccess = lipgloss.NewStyle().
				Foreground(theme.Color(theme.Primary)).
				Background(theme.Color(theme.Footer))
	styleStatusWarning = lipgloss.NewStyle().
				Foreground(theme.Color(theme.Amber)).
				Background(theme.Color(theme.Footer))
	styleStatusError = lipgloss.NewStyle().
				Foreground(theme.Color(theme.Red)).
				Background(theme.Color(theme.Footer))

	// Health pip and deprecation banner styles. OK is the active teal, Warn the
	// attention amber, and Err the inline error red — the same three accents the
	// rest of the dashboard states are painted in. styleBanner is a
	// yellow-foreground accent so it reads as a warning without consuming a full
	// background block.
	stylePipOK   = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary))
	stylePipWarn = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))
	stylePipErr  = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
	styleBanner  = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber)).Bold(true)
)

// renderSelectedRow normalizes a selectable row to one continuous selection
// span. Row renderers may compose semantic foreground, faint, and reset
// sequences for their normal state; retaining those sequences underneath a
// background wrapper can terminate the selection early and leave stale
// tinted cells behind when the cursor moves. The selected copy therefore
// keeps only its visible content before applying the shared selection style.
func renderSelectedRow(row string) string {
	return styleSelected.Render(ansi.Strip(row))
}

// plural is the root's count suffix. Timestamp and age labels are not here:
// every view builds them from tui/layout's age helpers (When, Ago,
// CompactAge), which take the reference instant explicitly.

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
	prompt := styleSearch.Render("/") + filterInputText(m.filterText+"█", m.width)
	out := prompt + "\n" + dashboard.RenderFilterHint(m.filterText, m.width)
	if popup := renderCompletion(m); popup != "" {
		out += popup
	}
	return out
}

// filterInputText fits an input's text and cursor into a line of width cells
// that starts with its one-cell sigil. A long query drops its start rather
// than its end, so what was just typed and the cursor stay on screen. A
// non-positive width (no window size yet) leaves the text whole.
func filterInputText(text string, width int) string {
	if width <= 0 {
		return text
	}
	return layout.TruncateLeft(text, width-1, "…")
}

func configSessionDisplayTitle(sess config.Session) string {
	return sess.Title
}
