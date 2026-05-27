package tui

import (
	"fmt"
	"strings"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/transcript"
)

// sessionSearchVisibleHeight returns the number of result rows that fit in
// the current viewport. Mirrors pickerVisibleHeight: total height minus the
// header + filter bar + query bar + a trailing newline + the two-line footer.
func sessionSearchVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	// header (1) + chip bar (1) + query bar (1) + blank (1) + trailing (1) + footer (2) = 7
	avail := m.height - 7
	if avail < 1 {
		return 1
	}
	return avail
}

// renderSessionSearch renders the entire ModeSessionSearch view: header,
// chip bar, query bar, and the results list. Mirrors renderPicker's
// single-pane layout (no preview pane in v1).
func renderSessionSearch(m Model) string {
	var sb strings.Builder

	header := "Past Sessions"
	count := len(m.sessionResults)
	if m.sessionLoading {
		header += "  " + styleDim.Render("(scanning…)")
	} else {
		header += fmt.Sprintf("  [%d result%s]", count, plural(count))
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
	sb.WriteByte('\n')

	if err := m.sessionRefreshErr; err != nil {
		sb.WriteString(styleError.Render("refresh error: "+err.Error()) + "\n")
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
		if m.sessionLoading {
			sb.WriteString(styleDim.Render("  (loading…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no sessions match)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	startIdx := 0
	endIdx := total
	showTop := false
	showBottom := false

	if height > 0 {
		offset := m.sessionOffset
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

	acctColor := buildProfileColorMap(m)

	for i := startIdx; i < endIdx; i++ {
		res := m.sessionResults[i]
		line := formatSessionRow(res, acctColor, maxWidth)
		if i == m.sessionCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
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
	switch {
	case delta < time.Hour:
		mins := int(delta.Minutes())
		if mins < 1 {
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
