package dashboard

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// RenderPreview formats the dashboard's scrollback preview pane for the given
// width/height. The final return is exactly height lines tall (no trailing
// newline). When the session is not running (WindowID == 0) it renders a
// placeholder.
//
// Whether the pane fits at all is a root decision, because that budget charges
// the root's footer and status bar; this package only paints the pane once the
// root has sized it.
func RenderPreview(width, height int, sess session.RunningSession, text, errText string, acctColor map[string]string, cfg *config.Config) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	// One-line header with project-identity-colored title. Pictographic
	// titles reserve the fixed 2-cell title-icon column so emoji-prefixed
	// projects align with each other across surfaces.
	colorName := ProjectIdentityColor(sess, acctColor, cfg)
	title := layout.Truncate(layout.RenderTitleIcon(SessionDisplayTitle(sess)), width, "…")
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
		lines = []string{stylePreviewError.Render(layout.Truncate("error: "+errText, width, "…"))}
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
