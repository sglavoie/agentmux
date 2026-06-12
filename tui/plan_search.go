package tui

import (
	"fmt"
	"strings"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/plans"
)

// planSearchVisibleHeight returns the number of result rows that fit in the
// root body area. Plans has no chip bar, so the chrome is one row lighter
// than session search: header (1) + query bar (1) + blank (1), plus any
// selected plan path row and inline error rows.
func planSearchVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	avail := rootBodyHeight(m) - 3 - pathRowLines(selectedPlanSearchPath(m)) - planSearchErrorLines(m)
	if avail < 1 {
		return 1
	}
	return avail
}

func planSearchErrorLines(m Model) int {
	n := 0
	if m.planRefreshErr != nil {
		n++
	}
	if m.planSearchErr != nil {
		n++
	}
	return n
}

// planSortLabel returns the short label rendered in the Plans header to
// signal which ordering is active. Kept distinct from the SortMode
// constants so the on-screen labels can evolve without touching the
// internal type.
func planSortLabel(mode plans.SortMode) string {
	switch mode {
	case plans.SortByModTime:
		return "recent"
	default:
		return "matches"
	}
}

// renderPlanSearch renders the plan browser: header, query bar, and the
// results list. Mirrors renderSessionSearch without the chip bar because plans
// have no categorical filters.
func renderPlanSearch(m Model) string {
	var sb strings.Builder

	header := planSearchHeader(m)
	count := len(m.planResults)
	if m.planLoading {
		header += "  " + styleDim.Render("(scanning…)")
	} else {
		header += "  " + styleDim.Render(fmt.Sprintf("[%d result%s · sort: %s]",
			count, plural(count), planSortLabel(m.planSortMode)))
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteByte('\n')

	switch {
	case m.planSearchMode:
		sb.WriteString(styleSearch.Render("/") + m.planQuery + "█")
	case m.planQuery != "":
		sb.WriteString(styleSearch.Render("/") + m.planQuery)
	default:
		sb.WriteString(styleDim.Render("press / to search plan content"))
	}
	sb.WriteByte('\n')
	if row := renderPathRow("plan path", selectedPlanSearchPath(m), m.width); row != "" {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	if err := m.planRefreshErr; err != nil {
		sb.WriteString(styleError.Render("refresh error: "+err.Error()) + "\n")
	}
	if err := m.planSearchErr; err != nil {
		sb.WriteString(styleError.Render("search error: "+err.Error()) + "\n")
	}

	height := planSearchVisibleHeight(m)
	sb.WriteString(renderPlanSearchList(m, height))
	return sb.String()
}

func planSearchHeader(m Model) string {
	return "Plans"
}

// renderPlanSearchList renders the scrollable results list. height is the
// per-call row budget; non-positive height means "unbounded" and scroll
// indicators are suppressed. Mirrors renderSessionSearchList.
func renderPlanSearchList(m Model, height int) string {
	var sb strings.Builder

	total := len(m.planResults)
	if total == 0 {
		if m.planLoading {
			sb.WriteString(styleDim.Render("  (loading…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no plans match)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	window := flatListWindowFor(total, m.planOffset, height)

	if window.showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	maxWidth := m.width
	if maxWidth <= 0 {
		maxWidth = 120
	}

	for i := window.start; i < window.end; i++ {
		res := m.planResults[i]
		line := formatPlanRow(res, maxWidth)
		if i == m.planCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if window.showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
}

// formatPlanRow returns a single-line summary of one plan result. Format:
// "  <when>  <name>  <N files>  [m matches]?  <preview>"
func formatPlanRow(res plans.Result, maxWidth int) string {
	when := formatSessionWhen(res.Meta.ModTime)
	name := res.Meta.Name
	if name == "" {
		name = "(unnamed)"
	}
	fileFrag := fmt.Sprintf("%d file%s", len(res.Meta.Files), plural(len(res.Meta.Files)))

	var matchFrag string
	if res.Matches > 0 {
		matchFrag = "  " + styleSearch.Render(fmt.Sprintf("(%d match%s)", res.Matches, matchPlural(res.Matches)))
	}

	head := fmt.Sprintf("  %s  %s  %s", when, name, styleDim.Render(fileFrag))
	body := head + matchFrag

	preview := res.Preview
	if res.Matches == 0 {
		preview = ""
	}
	if preview != "" {
		body += "  " + styleDim.Render(runewidth.Truncate(preview, maxRunes(maxWidth-runewidth.StringWidth(body)-2), "…"))
	}
	return runewidth.Truncate(body, maxWidth, "…")
}
