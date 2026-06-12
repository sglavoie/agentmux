package tui

import (
	"strings"

	runewidth "github.com/mattn/go-runewidth"
)

func renderPathRow(label, path string, maxWidth int) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if maxWidth <= 0 {
		maxWidth = 120
	}
	row := "  " + label + ": " + path
	return styleDim.Render(runewidth.Truncate(row, maxWidth, "…"))
}

func pathRowLines(path string) int {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	return 1
}

func selectedSessionSearchPath(m Model) string {
	if m.sessionCursor < 0 || m.sessionCursor >= len(m.sessionResults) {
		return ""
	}
	return m.sessionResults[m.sessionCursor].Meta.Path
}

func currentSessionViewPath(m Model) string {
	if m.viewerSession == nil {
		return ""
	}
	return m.viewerSession.Path
}

func selectedPlanSearchPath(m Model) string {
	if m.planCursor < 0 || m.planCursor >= len(m.planResults) {
		return ""
	}
	return m.planResults[m.planCursor].Meta.DirPath
}

func currentPlanViewPath(m Model) string {
	if m.viewerPlan == nil {
		return ""
	}
	return m.viewerPlan.DirPath
}
