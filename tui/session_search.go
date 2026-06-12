package tui

import (
	"fmt"
	"strings"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/transcript"
)

// sessionSearchVisibleHeight returns the number of result rows that fit in
// the root body area. composeRootView owns the footer and sticky status row,
// so this budget subtracts only the lines renderSessionSearch writes above
// the scrollable results plus any inline error rows.
func sessionSearchVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	// header (1) + chip bar (1) + query bar (1) + blank (1), plus the
	// selected transcript path when available.
	avail := rootBodyHeight(m) - 4 - pathRowLines(selectedSessionSearchPath(m)) - sessionSearchErrorLines(m)
	if avail < 1 {
		return 1
	}
	return avail
}

func sessionSearchErrorLines(m Model) int {
	n := 0
	if m.sessionRefreshErr != nil {
		n++
	}
	if m.sessionSearchErr != nil {
		n++
	}
	return n
}

// renderSessionSearch renders the entire ModeSessionSearch view: header,
// chip bar, query bar, and the results list. Mirrors renderPicker's
// single-pane layout (no preview pane in v1).
func renderSessionSearch(m Model) string {
	var sb strings.Builder

	header := "Past Sessions"
	count := len(m.sessionResults)
	if count > 0 || !m.sessionLoading {
		header += fmt.Sprintf("  [%d result%s]", count, plural(count))
	}
	var states []string
	if label := sessionLoadingLabel(m); label != "" {
		states = append(states, label)
	}
	if m.sessionRefreshInFlight {
		states = append(states, "scanning transcripts…")
	}
	if len(states) > 0 {
		header += "  " + styleDim.Render("("+strings.Join(states, ", ")+")")
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteByte('\n')

	sb.WriteString(renderSessionSearchChips(m))
	sb.WriteByte('\n')

	switch {
	case m.sessionSearchMode:
		sb.WriteString(styleSearch.Render("/") + m.sessionQuery + "█")
	case m.sessionQuery != "":
		sb.WriteString(styleSearch.Render("/") + m.sessionQuery)
	default:
		sb.WriteString(styleDim.Render("press / to search transcript content"))
	}
	sb.WriteByte('\n')

	if row := renderPathRow("session path", selectedSessionSearchPath(m), m.width); row != "" {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	if err := m.sessionRefreshErr; err != nil {
		sb.WriteString(styleError.Render(sessionRefreshErrorLabel(m)+": "+err.Error()) + "\n")
	}
	if err := m.sessionSearchErr; err != nil {
		sb.WriteString(styleError.Render("search error: "+err.Error()) + "\n")
	}

	height := sessionSearchVisibleHeight(m)
	sb.WriteString(renderSessionSearchList(m, height))
	return sb.String()
}

// renderSessionSearchChips returns a one-line summary of the active chip
// filters. Empty filters render as a single dim "(no filters)" placeholder
// so the line still occupies vertical space (keeps the layout stable).
func renderSessionSearchChips(m Model) string {
	var parts []string
	if v := singleChipValue(m.filterHarness); v != "" {
		parts = append(parts, styleChip.Render("[h] harness: "+v))
	} else {
		parts = append(parts, styleDim.Render("[h] harness: any"))
	}
	if v := singleChipValue(m.filterProfile); v != "" {
		parts = append(parts, styleChip.Render("[p] profile: "+v))
	} else {
		parts = append(parts, styleDim.Render("[p] profile: any"))
	}
	if v := singleChipValue(m.filterProject); v != "" {
		parts = append(parts, styleChip.Render("[r] project: "+v))
	} else {
		parts = append(parts, styleDim.Render("[r] project: any"))
	}
	return "  " + strings.Join(parts, "   ")
}

func singleChipValue(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// renderSessionSearchList renders the scrollable results list. height is the
// per-call row budget; non-positive height means "unbounded" and scroll
// indicators are suppressed. Mirrors renderPickerList's windowing contract.
func renderSessionSearchList(m Model, height int) string {
	var sb strings.Builder

	total := len(m.sessionResults)
	if total == 0 {
		if label := sessionLoadingLabel(m); label != "" {
			sb.WriteString(styleDim.Render("  (" + label + ")"))
		} else if m.sessionRefreshInFlight {
			sb.WriteString(styleDim.Render("  (scanning transcripts…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no sessions match)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	window := flatListWindowFor(total, m.sessionOffset, height)

	if window.showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	maxWidth := m.width
	if maxWidth <= 0 {
		maxWidth = 120
	}

	acctColor := buildProfileColorMap(m)

	for i := window.start; i < window.end; i++ {
		res := m.sessionResults[i]
		line := formatSessionRow(res, acctColor, maxWidth)
		if i == m.sessionCursor {
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

type flatListWindow struct {
	start      int
	end        int
	showTop    bool
	showBottom bool
}

func flatListWindowFor(total, offset, height int) flatListWindow {
	window := flatListWindow{start: 0, end: total}
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

	if height == 1 {
		window.end = window.start + 1
		if window.end > total {
			window.end = total
		}
		return window
	}

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
		if budget > 1 {
			budget--
		} else {
			window.showBottom = false
		}
		window.end = window.start + budget
		if window.end > total {
			window.end = total
		}
	}
	return window
}

func sessionLoadingLabel(m Model) string {
	if !m.sessionLoading {
		return ""
	}
	if m.sessionCacheLoadInFlight || m.transcriptIndex == nil {
		return "loading cache…"
	}
	return "searching…"
}

func sessionRefreshErrorLabel(m Model) string {
	if m.transcriptIndex == nil {
		return "cache load error"
	}
	return "refresh error"
}

// formatSessionRow returns a single-line summary of one search result.
// Format: "  <when>  <project>  [profile/harness]  matches?  preview"
func formatSessionRow(res transcript.Result, acctColor map[string]string, maxWidth int) string {
	when := formatSessionWhen(res.Meta.LastAt)
	project := res.Meta.Project
	if project == "" {
		project = "(unknown)"
	}
	profile := res.Meta.Profile
	hname := res.Meta.Harness
	tag := "[" + profile + "/" + hname + "]"
	if profile == "" {
		tag = "[" + hname + "]"
	}
	tagColored := accountStyle(acctColor[profile]).Render(tag)

	var matchFrag string
	if res.Matches > 0 {
		matchFrag = "  " + styleSearch.Render(fmt.Sprintf("(%d match%s)", res.Matches, matchPlural(res.Matches)))
	}

	preview := res.Preview
	if preview == "" {
		preview = res.Meta.Snippet
	}

	head := fmt.Sprintf("  %s  %s  ", when, project)
	body := head + tagColored + matchFrag
	if preview != "" {
		body += "  " + styleDim.Render(runewidth.Truncate(preview, maxRunes(maxWidth-runewidth.StringWidth(body)-2), "…"))
	}
	return runewidth.Truncate(body, maxWidth, "…")
}

func maxRunes(n int) int {
	if n < 8 {
		return 8
	}
	return n
}

// formatSessionWhen returns a short human-readable representation of a
// transcript's LastAt timestamp. Sessions newer than 24h show as "Xh", older
// up to 7d as "Xd", and anything older falls back to YYYY-MM-DD.
func formatSessionWhen(t time.Time) string {
	if t.IsZero() {
		return "          "
	}
	delta := time.Since(t)
	if delta < 0 {
		delta = 0
	}
	switch {
	case delta < time.Hour:
		mins := int(delta.Minutes())
		if mins < 1 && delta > 0 {
			mins = 1
		}
		return fmt.Sprintf("%2dm   ago", mins)
	case delta < 24*time.Hour:
		return fmt.Sprintf("%2dh   ago", int(delta.Hours()))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("%2dd   ago", int(delta.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func matchPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}
