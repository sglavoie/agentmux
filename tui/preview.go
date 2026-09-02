package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// previewTickMsg is sent by the preview ticker while preview is enabled.
type previewTickMsg struct{}

// previewContentMsg carries the result of a scrollback fetch.
type previewContentMsg struct {
	windowID int
	text     string
	err      error
}

// previewTickCmd schedules a single previewTickMsg after d.
func previewTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return previewTickMsg{}
	})
}

// fetchPreviewCmd asynchronously fetches the full scrollback for a window.
// windowID == 0 (no running window) short-circuits with an empty text and
// no error so callers can render a "(session not running)" placeholder.
func fetchPreviewCmd(client kitty.KittyClient, windowID int) tea.Cmd {
	return func() tea.Msg {
		if windowID == 0 {
			return previewContentMsg{windowID: 0, text: "", err: nil}
		}
		text, err := client.GetText(kitty.MatchWindowByID(windowID), "all")
		return previewContentMsg{windowID: windowID, text: text, err: err}
	}
}

// renderPreview formats the preview pane for the given width/height. The
// final return is exactly height lines tall (no trailing newline). When the
// session is not running (WindowID == 0) it renders a placeholder.
func renderPreview(width, height int, sess session.RunningSession, text, errText string, acctColor map[string]string, cfg *config.Config) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	// One-line header with project-identity-colored title. Pictographic
	// titles reserve the fixed 2-cell title-icon column so emoji-prefixed
	// projects align with each other across surfaces.
	colorName := dashboard.ProjectIdentityColor(sess, acctColor, cfg)
	title := layout.Truncate(layout.RenderTitleIcon(dashboard.SessionDisplayTitle(sess)), width, "…")
	header := accountStyle(colorName).Render(title)

	body := previewBody(width, height-1, sess, text, errText)
	return header + "\n" + body
}

// previewBody returns exactly height lines of tail content, padded with blanks.
func previewBody(width, height int, sess session.RunningSession, text, errText string) string {
	if height <= 0 {
		return ""
	}
	var lines []string
	switch {
	case errText != "":
		lines = []string{styleError.Render(layout.Truncate("error: "+errText, width, "…"))}
	case sess.WindowID == 0:
		lines = []string{styleDim.Render(layout.Truncate("(session not running)", width, "…"))}
	case text == "":
		lines = []string{styleDim.Render(layout.Truncate("(loading…)", width, "…"))}
	default:
		raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
		// Take the last `height` lines.
		if len(raw) > height {
			raw = raw[len(raw)-height:]
		}
		lines = make([]string, len(raw))
		for i, ln := range raw {
			lines[i] = layout.Truncate(ln, width, "…")
		}
	}
	// Pad up to height.
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// previewFits reports whether the current terminal dimensions allow the
// configured preview direction. footerLines plus the sticky status row are
// reserved at the bottom.
func previewFits(width, height, footerLines, minCols, minRows int, direction string) bool {
	switch direction {
	case "vertical":
		return height-footerLines-statusBarLines >= minRows
	default:
		return width >= minCols
	}
}
