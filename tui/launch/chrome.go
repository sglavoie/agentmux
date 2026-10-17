package launch

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/theme"
)

// Launcher styles stay local while deriving their colors from the shared palette.
var (
	styleSelected     = theme.Selected()
	styleError        = lipgloss.NewStyle().Foreground(theme.Color(theme.Red))
	styleDim          = lipgloss.NewStyle().Foreground(theme.Color(theme.Muted))
	styleSearch       = lipgloss.NewStyle().Foreground(theme.Color(theme.Primary)).Bold(true)
	styleHeader       = lipgloss.NewStyle().Foreground(theme.Color(theme.Secondary)).Bold(true)
	styleAttentionRow = lipgloss.NewStyle().Foreground(theme.Color(theme.Amber))

	// styleOverlayBox wraps the profile-override overlay in a rounded border
	// with the de-facto accent teal. Padding adds a single column of breathing
	// room on each side; combined with the border, the box adds 2 columns +
	// 2 rows of chrome to inner content.
	styleOverlayBox = theme.Popup()
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

// accountStyle returns a Lipgloss style with the account's foreground color.
// Falls back to the default style if the color name is unknown.
func accountStyle(colorName string) lipgloss.Style {
	c, err := palette.Lookup(colorName)
	if err != nil {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c.ForegroundHex()))
}

// findAccountByName is a private duplicate of the root helper of the same
// name, which the dashboard action handlers still need. Sharing it would mean
// either a utility package or a leaf-to-root edge.
//
// Case-insensitive compare: SessionKey lowercases profile names at runtime and
// config validation rejects case-only duplicates, so a user var like "Work"
// (from a launch before a config rename) must still resolve to the "work"
// profile.
func findAccountByName(accounts []config.Account, name string) (config.Account, bool) {
	for _, a := range accounts {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return config.Account{}, false
}

// configSessionDisplayTitle mirrors the root helper of the same name so the
// launcher's row, preview, and batch labels all read a configured project's
// display title through one call site.
func configSessionDisplayTitle(sess config.Session) string {
	return sess.Title
}

// sessionWithProfile stamps a one-time profile choice onto a configured
// project. It is a private duplicate of the root helper the dashboard restart
// path still uses.
func sessionWithProfile(sess config.Session, profileName string) config.Session {
	out := sess
	out.Profile = profileName
	out.Account = profileName
	return out
}
