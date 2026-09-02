package tui

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/tui/layout"
	tuiplans "github.com/sglavoie/agentmux/tui/plans"
)

// renderPlanView renders the full ModePlanView layout: a one-line header
// summarising the plan and a scrollable list of wrapped visual rows below.
func renderPlanView(m Model) string {
	var sb strings.Builder

	header := "Plan"
	if m.viewerPlan != nil {
		name := m.viewerPlan.Name
		if name == "" {
			name = "(unnamed)"
		}
		header = fmt.Sprintf("Plan: %s  [project: %s]  (%d file%s)",
			layout.RenderTitleIcon(name), tuiplans.ProjectLabel(*m.viewerPlan), len(m.viewerPlan.Files), plural(len(m.viewerPlan.Files)))
	}
	if m.planViewerLoading {
		header += "  " + styleDim.Render("(loading…)")
	} else if len(m.planViewerMatchLines) > 0 {
		header += "  " + styleSearch.Render(fmt.Sprintf("match %d/%d",
			m.planViewerMatchIdx+1, len(m.planViewerMatchLines)))
	}
	renderedHeader := styleHeader.Render(header)
	if m.width > 0 {
		renderedHeader = layout.StyledTruncate(renderedHeader, m.width, "…")
	}
	sb.WriteString(renderedHeader)
	sb.WriteByte('\n')
	if row := renderPathRow("plan path", currentPlanViewPath(m), m.width); row != "" {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	if err := m.planViewerLoadErr; err != nil {
		for _, line := range layout.WrapHard("read error: "+err.Error(), layout.WrapWidth(m.width)) {
			sb.WriteString(styleError.Render(line))
			sb.WriteByte('\n')
		}
	}

	height := planViewerVisibleHeight(m)
	sb.WriteString(renderPlanViewBody(m, height))
	return sb.String()
}

// renderPlanViewBody renders wrapped visual rows derived from the logical
// source lines in planViewerLines. Mirrors renderSessionViewBody's windowing
// contract: at most height rows, with ▲ when offset > 0 and ▼ when more rows
// remain past the window.
func renderPlanViewBody(m Model, height int) string {
	var sb strings.Builder

	rows := planViewerRows(m)
	total := len(rows)
	if total == 0 {
		if m.planViewerLoading {
			sb.WriteString(styleDim.Render("  (loading…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no content)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	window := layout.ListWindowFor(total, m.planViewerScroll, height)

	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	cursorLine := m.planViewerCursor

	for i := window.Start; i < window.End; i++ {
		row := rows[i]
		line := row.Text
		switch {
		case row.SourceLine == cursorLine:
			sb.WriteString(renderSelectedRow(line))
		case row.IsMatch(m.planViewerMatchLines):
			sb.WriteString(styleSearch.Render(line))
		case row.IsSeparator(m.planViewerLines):
			sb.WriteString(styleDim.Render(line))
		default:
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if window.ShowBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
}
