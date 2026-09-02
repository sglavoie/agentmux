package tui

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// Consequence strings shown on line 2 of the confirm overlay so the user
// understands what pressing `y` will actually do. The transcript path is
// omitted because RunningSession does not currently expose it.
const (
	closeConsequence        = "The Claude process exits; transcript persists."
	restartConsequence      = "The Claude process restarts; transcript persists and resumes via --resume if configured."
	worktreeForkConsequence = "Creates a tracked Git worktree and forks there. Dirty sources are refused; CLI --allow-dirty omits uncommitted changes."
)

// renderConfirm renders the two-line confirmation overlay:
//
//	Close "<title>"?
//	<consequence describing what happens on y>
//
// The footer (rendered separately) carries the y/n/esc affordances.
func renderConfirm(m Model) string {
	var verb string
	title := dashboard.SessionDisplayTitle(m.targetSession)
	switch m.confirmAction {
	case "close":
		verb = "Close"
	case "restart":
		verb = "Restart"
	case "fork worktree":
		verb = "Fork in new worktree"
	case "worktree remove":
		verb = "Remove worktree checkout"
		title = worktreeConfirmationTitle(m.worktreeActionTarget)
	case "worktree delete branch":
		verb = "Remove worktree and delete branch"
		title = worktreeConfirmationTitle(m.worktreeActionTarget)
	case "worktree forget":
		verb = "Forget worktree metadata"
		title = worktreeConfirmationTitle(m.worktreeActionTarget)
	case aslConfirmStop:
		verb = "Stop after the current session"
		title = aslConfirmationTitle(m.aslActionTarget)
	case aslConfirmAbort:
		verb = "Abort the in-flight turn"
		title = aslConfirmationTitle(m.aslActionTarget)
	default:
		verb = m.confirmAction
	}

	var sb strings.Builder
	sb.WriteString(styleHeader.Render(fmt.Sprintf("%s %q?", verb, layout.RenderTitleIcon(title))))
	sb.WriteByte('\n')
	if m.confirmConsequence != "" {
		sb.WriteString(styleDim.Render(m.confirmConsequence))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// aslConfirmationTitle names the captured run. It reads the capture rather than
// the current selection, so the overlay keeps describing what the operator
// reviewed even if a refresh moves the list underneath it.
func aslConfirmationTitle(target *aslActionTarget) string {
	if target == nil {
		return "selected run"
	}
	if strings.TrimSpace(target.Label) != "" {
		return target.Label
	}
	return target.RowID
}

func worktreeConfirmationTitle(target *actions.WorktreeView) string {
	if target == nil {
		return "selected worktree"
	}
	if strings.TrimSpace(target.ProjectLabel) != "" {
		return target.ProjectLabel + " · " + target.Branch
	}
	if strings.TrimSpace(target.Branch) != "" {
		return target.Branch
	}
	return target.ID
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
