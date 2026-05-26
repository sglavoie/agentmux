package tui

import (
	"fmt"
	"strings"
)

// Consequence strings shown on line 2 of the confirm overlay so the user
// understands what pressing `y` will actually do. The transcript path is
// omitted because RunningSession does not currently expose it.
const (
	closeConsequence   = "The Claude process exits; transcript persists."
	restartConsequence = "The Claude process restarts; transcript persists and resumes via --resume if configured."
)

// renderConfirm renders the two-line confirmation overlay:
//
//	Close "<title>"?
//	<consequence describing what happens on y>
//
// The footer (rendered separately) carries the y/n/esc affordances.
func renderConfirm(m Model) string {
	var verb string
	switch m.confirmAction {
	case "close":
		verb = "Close"
	case "restart":
		verb = "Restart"
	default:
		verb = m.confirmAction
	}

	var sb strings.Builder
	sb.WriteString(styleHeader.Render(fmt.Sprintf("%s %q?", verb, runningSessionDisplayTitle(m.targetSession))))
	sb.WriteByte('\n')
	if m.confirmConsequence != "" {
		sb.WriteString(styleDim.Render(m.confirmConsequence))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// renderConfirmBox wraps renderConfirm in styleOverlayBox so the y/n confirm
// prompt can be composited as a centered modal on top of the dimmed
// dashboard. The inner width is capped at 70 columns and clamped to
// m.width-4 on narrow terminals.
func renderConfirmBox(m Model) string {
	innerWidth := 70
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(renderConfirm(m), "\n"))
}
