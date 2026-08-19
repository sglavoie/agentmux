package tui

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
)

func renderWorktreeSessionPicker(m Model) string {
	var sb strings.Builder
	title := worktreeConfirmationTitle(m.worktreeActionTarget)
	sb.WriteString(styleHeader.Render("Choose live session: " + title))
	sb.WriteString("\n\n")
	if len(m.worktreeSessionCandidates) == 0 {
		sb.WriteString(styleDim.Render("No live-session candidates remain."))
		sb.WriteByte('\n')
		return sb.String()
	}
	contentWidth := m.width - 10
	if contentWidth < 1 {
		contentWidth = 1
	}
	window := flatListWindowFor(len(m.worktreeSessionCandidates), m.worktreeSessionOffset, worktreeSessionVisibleRows(m))
	if window.showTop {
		sb.WriteString(styleDim.Render("  ▲ more sessions"))
		sb.WriteByte('\n')
	}
	for index := window.start; index < window.end; index++ {
		candidate := m.worktreeSessionCandidates[index]
		line := formatWorktreeSessionCandidate(candidate, contentWidth)
		if index == m.worktreeSessionCursor {
			line = styleSelected.Render(line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if window.showBottom {
		sb.WriteString(styleDim.Render("  ▼ more sessions"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func worktreeSessionVisibleRows(m Model) int {
	rows := rootBodyHeight(m) - 4
	if rows < 1 {
		return 1
	}
	return rows
}

func formatWorktreeSessionCandidate(candidate actions.WorktreeLiveSession, width int) string {
	project := strings.TrimSpace(candidate.Project)
	if project == "" {
		project = "(unknown project)"
	}
	identity := strings.Trim(strings.Join([]string{candidate.Profile, candidate.Harness}, "/"), "/")
	if identity == "" {
		identity = "unknown profile"
	}
	location := fmt.Sprintf("tab %d", candidate.TabID)
	if candidate.WindowID != 0 {
		location += fmt.Sprintf(" / window %d", candidate.WindowID)
	}
	line := fmt.Sprintf("%-18s  %-20s  %-22s  candidate %s", project, identity, location, candidate.ID)
	if candidate.HarnessSessionID != "" {
		line += "  native " + candidate.HarnessSessionID
	}
	return displayStyledTruncate(line, width, "…")
}

func renderWorktreeSessionPickerBox(m Model) string {
	content := strings.TrimRight(renderWorktreeSessionPicker(m), "\n")
	boxWidth := popupWidth(content) + styleOverlayBox.GetHorizontalFrameSize()
	if boxWidth < 64 {
		boxWidth = 64
	}
	if available := m.width - 4; available < boxWidth {
		boxWidth = available
	}
	if boxWidth < 1 {
		boxWidth = 1
	}
	return styleOverlayBox.Width(boxWidth).Render(content)
}
