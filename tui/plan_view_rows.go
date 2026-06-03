package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const planViewerFallbackWidth = 120

// planViewerRow is one rendered terminal row mapped back to its logical
// source line in planViewerLines.
type planViewerRow struct {
	text       string
	sourceLine int
}

type planViewerRowWindow struct {
	start      int
	end        int
	showTop    bool
	showBottom bool
}

func planViewerRows(m Model) []planViewerRow {
	return planViewerRowsForWidth(m.planViewerLines, m.width)
}

func planViewerRowsForWidth(lines []string, width int) []planViewerRow {
	wrapWidth := planViewerWrapWidth(width)
	rows := make([]planViewerRow, 0, len(lines))
	for i, line := range lines {
		wrapped := strings.Split(lipgloss.Wrap(line, wrapWidth, ""), "\n")
		for _, text := range wrapped {
			rows = append(rows, planViewerRow{
				text:       text,
				sourceLine: i,
			})
		}
	}
	return rows
}

func planViewerWrapWidth(width int) int {
	if width <= 0 {
		return planViewerFallbackWidth
	}
	return width
}

func planViewerFirstRowForLine(rows []planViewerRow, sourceLine int) int {
	for i, row := range rows {
		if row.sourceLine == sourceLine {
			return i
		}
	}
	return -1
}

func planViewerRowWindowFor(total, offset, height int) planViewerRowWindow {
	window := planViewerRowWindow{start: 0, end: total}
	if total <= 0 {
		window.end = 0
		return window
	}
	if height <= 0 {
		return window
	}

	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	window.start = offset

	budget := height
	if window.start > 0 {
		window.showTop = true
		budget--
	}
	if budget < 0 {
		budget = 0
	}
	window.end = window.start + budget
	if window.end > total {
		window.end = total
	}
	if window.end < total {
		window.showBottom = true
		if budget > 0 {
			budget--
		}
		window.end = window.start + budget
		if window.end > total {
			window.end = total
		}
	}
	return window
}

func planViewerPageStep(total, offset, height int) int {
	window := planViewerRowWindowFor(total, offset, height)
	step := window.end - window.start
	if step < 1 {
		return 1
	}
	return step
}

func planViewerMaxScrollOffset(total, height int) int {
	if total <= 0 || height <= 0 || total <= height {
		return 0
	}
	tailRows := height - 1
	if tailRows < 1 {
		tailRows = 1
	}
	maxOffset := total - tailRows
	if maxOffset < 0 {
		return 0
	}
	return maxOffset
}

func isPlanViewerSeparator(line string) bool {
	return strings.HasPrefix(line, "--- ") && strings.HasSuffix(line, " ---")
}

func (r planViewerRow) isSeparator(lines []string) bool {
	return r.sourceLine >= 0 &&
		r.sourceLine < len(lines) &&
		isPlanViewerSeparator(lines[r.sourceLine])
}

func (r planViewerRow) isMatch(matchLines []int) bool {
	for _, idx := range matchLines {
		if r.sourceLine == idx {
			return true
		}
	}
	return false
}
