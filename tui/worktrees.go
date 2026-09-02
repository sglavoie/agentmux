package tui

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/tui/layout"
	tuiworktrees "github.com/sglavoie/agentmux/tui/worktrees"
)

func worktreeVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	chrome := 3 + pathRowLines(selectedWorktreePath(m)) + worktreeNoticeLines(m)
	available := rootBodyHeight(m) - chrome
	if available < 1 {
		return 1
	}
	return available
}

func worktreeNoticeLines(m Model) int {
	width := layout.WrapWidth(m.width)
	lines := 0
	if m.worktreeErr != nil {
		lines += len(layout.WrapHard("refresh error: "+m.worktreeErr.Error(), width))
	}
	for _, warning := range m.worktreeWarnings {
		lines += len(layout.WrapHard("warning: "+warning, width))
	}
	return lines
}

func renderWorktrees(m Model) string {
	var sb strings.Builder
	header := "Worktrees"
	if m.worktreeActionInFlight {
		header += "  " + styleDim.Render("(working…)")
	} else if m.worktreeLoading {
		header += "  " + styleDim.Render("(refreshing…)")
	} else {
		header += "  " + styleDim.Render(fmt.Sprintf("[%d/%d visible]", len(m.worktreeFiltered), len(m.worktreeEntries)))
	}
	sb.WriteString(layout.ClampLine(styleHeader.Render(header), m.width))
	sb.WriteByte('\n')

	var filter string
	switch {
	case m.worktreeFilterMode:
		filter = styleSearch.Render("/") + m.worktreeFilter + "█"
	case m.worktreeFilter != "":
		filter = styleSearch.Render("/") + m.worktreeFilter
	default:
		filter = styleDim.Render("press / to filter worktrees")
	}
	sb.WriteString(layout.ClampLine(filter, m.width))
	sb.WriteByte('\n')
	if row := renderPathRow("worktree path", selectedWorktreePath(m), m.width); row != "" {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	width := layout.WrapWidth(m.width)
	if m.worktreeErr != nil {
		for _, line := range layout.WrapHard("refresh error: "+m.worktreeErr.Error(), width) {
			sb.WriteString(styleError.Render(line))
			sb.WriteByte('\n')
		}
	}
	for _, warning := range sortStrings(m.worktreeWarnings) {
		for _, line := range layout.WrapHard("warning: "+warning, width) {
			sb.WriteString(styleStateWarning.Render(line))
			sb.WriteByte('\n')
		}
	}

	sb.WriteString(renderWorktreeList(m, worktreeVisibleHeight(m)))
	return sb.String()
}

func renderWorktreeList(m Model, height int) string {
	if len(m.worktreeFiltered) == 0 {
		if m.worktreeLoading {
			return styleDim.Render("  (loading worktrees…)") + "\n"
		}
		if len(m.worktreeEntries) > 0 {
			return styleDim.Render("  (no worktrees match)") + "\n"
		}
		return styleDim.Render("  (no managed worktrees)") + "\n"
	}

	window := layout.ListWindowFor(len(m.worktreeFiltered), m.worktreeOffset, height)
	var sb strings.Builder
	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}
	for i := window.Start; i < window.End; i++ {
		line := tuiworktrees.FormatRow(m.worktreeFiltered[i], m.width)
		if i == m.worktreeCursor {
			line = renderSelectedRow(line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if window.ShowBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}
	return sb.String()
}
