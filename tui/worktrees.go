package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/worktree"
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
	width := planViewerWrapWidth(m.width)
	lines := 0
	if m.worktreeErr != nil {
		lines += len(displayWrapHard("refresh error: "+m.worktreeErr.Error(), width))
	}
	for _, warning := range m.worktreeWarnings {
		lines += len(displayWrapHard("warning: "+warning, width))
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
	sb.WriteString(clampSearchLine(styleHeader.Render(header), m.width))
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
	sb.WriteString(clampSearchLine(filter, m.width))
	sb.WriteByte('\n')
	if row := renderPathRow("worktree path", selectedWorktreePath(m), m.width); row != "" {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	width := planViewerWrapWidth(m.width)
	if m.worktreeErr != nil {
		for _, line := range displayWrapHard("refresh error: "+m.worktreeErr.Error(), width) {
			sb.WriteString(styleError.Render(line))
			sb.WriteByte('\n')
		}
	}
	for _, warning := range sortStrings(m.worktreeWarnings) {
		for _, line := range displayWrapHard("warning: "+warning, width) {
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

	window := flatListWindowFor(len(m.worktreeFiltered), m.worktreeOffset, height)
	var sb strings.Builder
	if window.showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}
	for i := window.start; i < window.end; i++ {
		line := formatWorktreeRow(m.worktreeFiltered[i], m.width)
		if i == m.worktreeCursor {
			line = styleSelected.Render(line)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if window.showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func formatWorktreeRow(entry actions.WorktreeView, width int) string {
	if width <= 0 {
		width = 120
	}
	project := strings.TrimSpace(entry.ProjectLabel)
	if project == "" {
		project = "(unknown project)"
	}
	branch := strings.TrimSpace(entry.Branch)
	if branch == "" {
		branch = "(no branch)"
	}
	identity := entry.Profile
	if entry.Harness != "" {
		if identity != "" {
			identity += "/"
		}
		identity += entry.Harness
	}
	if identity == "" {
		identity = "unknown profile"
	}
	age := compactWorktreeAge(entry.CreatedAt, time.Now())
	badges := worktreeBadgeText(entry)
	row := fmt.Sprintf("  %-18s  %-24s  %-18s  %6s  %s", project, branch, identity, age, badges)
	return displayStyledTruncate(row, width, "…")
}

func compactWorktreeAge(created, now time.Time) string {
	if created.IsZero() {
		return "?"
	}
	d := now.Sub(created)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func worktreeBadgeText(entry actions.WorktreeView) string {
	labels := worktreeStateLabels(entry)
	if len(labels) > 0 && labels[0] == string(worktree.ClassificationValid) {
		labels = labels[1:]
	}
	if len(labels) == 0 {
		labels = []string{"clean"}
	}
	return "[" + strings.Join(labels, "][") + "]"
}
