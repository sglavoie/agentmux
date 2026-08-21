package tui

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
)

func renderLiveSessionPicker(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Choose live session: " + m.livePickerTitle))
	sb.WriteString("\n\n")
	if len(m.livePickerCandidates) == 0 {
		sb.WriteString(styleDim.Render("No live-session candidates remain."))
		sb.WriteByte('\n')
		return sb.String()
	}
	contentWidth := m.width - 10
	if contentWidth < 1 {
		contentWidth = 1
	}
	window := flatListWindowFor(len(m.livePickerCandidates), m.livePickerOffset, livePickerVisibleRows(m))
	if window.showTop {
		sb.WriteString(styleDim.Render("  ▲ more sessions"))
		sb.WriteByte('\n')
	}
	for index := window.start; index < window.end; index++ {
		candidate := m.livePickerCandidates[index]
		line := formatLiveSessionCandidate(candidate, contentWidth)
		if index == m.livePickerCursor {
			line = renderSelectedRow(line)
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

func livePickerVisibleRows(m Model) int {
	rows := rootBodyHeight(m) - 4
	if rows < 1 {
		return 1
	}
	return rows
}

func formatLiveSessionCandidate(candidate actions.WorktreeLiveSession, width int) string {
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

func renderLiveSessionPickerBox(m Model) string {
	content := strings.TrimRight(renderLiveSessionPicker(m), "\n")
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
