package tui

import (
	"fmt"
	"strings"

	runewidth "github.com/mattn/go-runewidth"
)

// renderPlanView renders the full ModePlanView layout: a one-line header
// summarising the plan and a scrollable list of file content lines below.
func renderPlanView(m Model) string {
	var sb strings.Builder

	header := "Plan"
	if m.viewerPlan != nil {
		name := m.viewerPlan.Name
		if name == "" {
			name = "(unnamed)"
		}
		header = fmt.Sprintf("Plan: %s  (%d file%s)",
			name, len(m.viewerPlan.Files), plural(len(m.viewerPlan.Files)))
	}
	if m.planViewerLoading {
		header += "  " + styleDim.Render("(loading…)")
	} else if len(m.planViewerMatchLines) > 0 {
		header += "  " + styleSearch.Render(fmt.Sprintf("match %d/%d",
			m.planViewerMatchIdx+1, len(m.planViewerMatchLines)))
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if err := m.planViewerLoadErr; err != nil {
		sb.WriteString(styleError.Render("read error: "+err.Error()) + "\n")
	}

	height := planViewerVisibleHeight(m)
	sb.WriteString(renderPlanViewBody(m, height))
	return sb.String()
}

// renderPlanViewBody renders the windowed line list. Mirrors
// renderSessionViewBody's windowing contract: at most height rows, with
// ▲ when offset > 0 and ▼ when more rows remain past the window.
func renderPlanViewBody(m Model, height int) string {
	var sb strings.Builder

	total := len(m.planViewerLines)
	if total == 0 {
		if m.planViewerLoading {
			sb.WriteString(styleDim.Render("  (loading…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no content)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	startIdx := 0
	endIdx := total
	showTop := false
	showBottom := false

	if height > 0 {
		offset := m.planViewerScroll
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		startIdx = offset

		budget := height
		if startIdx > 0 {
			showTop = true
			budget--
		}
		if budget < 0 {
			budget = 0
		}
		endIdx = startIdx + budget
		if endIdx > total {
			endIdx = total
		}
		if endIdx < total {
			showBottom = true
			if budget > 0 {
				budget--
			}
			endIdx = startIdx + budget
			if endIdx > total {
				endIdx = total
			}
		}
	}

	if showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	maxWidth := m.width
	if maxWidth <= 0 {
		maxWidth = 120
	}

	isMatchLine := make(map[int]bool, len(m.planViewerMatchLines))
	for _, idx := range m.planViewerMatchLines {
		isMatchLine[idx] = true
	}
	cursorLine := m.planViewerCursor

	for i := startIdx; i < endIdx; i++ {
		raw := m.planViewerLines[i]
		isSeparator := strings.HasPrefix(raw, "--- ") && strings.HasSuffix(raw, " ---")
		line := runewidth.Truncate(raw, maxRunes(maxWidth), "…")
		switch {
		case i == cursorLine:
			sb.WriteString(styleSelected.Render(line))
		case isMatchLine[i]:
			sb.WriteString(styleSearch.Render(line))
		case isSeparator:
			sb.WriteString(styleDim.Render(line))
		default:
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
}
