package plans

import (
	"fmt"
	"strings"

	planindex "github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ViewerInput is everything the plan viewer needs to paint itself. Like
// SearchInput it is a projection of the root model, so the leaf never names
// the Bubble Tea model, and it carries both the terminal's own Height and the
// BodyHeight the root has left after its chrome.
type ViewerInput struct {
	Width      int
	Height     int
	BodyHeight int

	// PathRow is the root-rendered "plan path: …" row for the open plan, or
	// the empty string when no plan is open.
	PathRow string

	Plan *planindex.PlanMeta

	Lines   []string
	Loading bool
	LoadErr error

	MatchLines []int
	MatchIdx   int

	Cursor int
	Scroll int
}

// VisibleHeight returns the number of body rows that fit in the root body
// area. The viewer's chrome matches the session viewer: header (1) + blank
// (1), plus the plan path when available and any inline read-error row.
func (in ViewerInput) VisibleHeight() int {
	if in.Height <= 0 {
		return 0
	}
	avail := in.BodyHeight - 2 - pathRowLines(in.PathRow) - in.errorLines()
	if avail < 1 {
		return 1
	}
	return avail
}

// errorLines reports how many rows the inline read error occupies.
func (in ViewerInput) errorLines() int {
	if in.LoadErr != nil {
		return 1
	}
	return 0
}

// Rows wraps the loaded plan lines to the current width.
func (in ViewerInput) Rows() []ViewerRow {
	return ViewerRows(in.Lines, in.Width)
}

// Render renders the full plan-viewer layout: a one-line header summarising
// the plan and a scrollable list of wrapped visual rows below.
func (in ViewerInput) Render() string {
	var sb strings.Builder

	header := "Plan"
	if in.Plan != nil {
		name := in.Plan.Name
		if name == "" {
			name = "(unnamed)"
		}
		header = fmt.Sprintf("Plan: %s  [project: %s]  (%d file%s)",
			layout.RenderTitleIcon(name), projectLabel(*in.Plan), len(in.Plan.Files), plural(len(in.Plan.Files)))
	}
	if in.Loading {
		header += "  " + styleDim.Render("(loading…)")
	} else if len(in.MatchLines) > 0 {
		header += "  " + styleSearch.Render(fmt.Sprintf("match %d/%d",
			in.MatchIdx+1, len(in.MatchLines)))
	}
	renderedHeader := styleHeader.Render(header)
	if in.Width > 0 {
		renderedHeader = layout.StyledTruncate(renderedHeader, in.Width, "…")
	}
	sb.WriteString(renderedHeader)
	sb.WriteByte('\n')
	if in.PathRow != "" {
		sb.WriteString(in.PathRow)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	if err := in.LoadErr; err != nil {
		for _, line := range layout.WrapHard("read error: "+err.Error(), layout.WrapWidth(in.Width)) {
			sb.WriteString(styleError.Render(line))
			sb.WriteByte('\n')
		}
	}

	sb.WriteString(in.RenderBody(in.VisibleHeight()))
	return sb.String()
}

// RenderBody renders wrapped visual rows derived from the logical source
// lines. Mirrors the transcript viewer's windowing contract: at most height
// rows, with ▲ when offset > 0 and ▼ when more rows remain past the window.
func (in ViewerInput) RenderBody(height int) string {
	var sb strings.Builder

	rows := in.Rows()
	total := len(rows)
	if total == 0 {
		if in.Loading {
			sb.WriteString(styleDim.Render("  (loading…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no content)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	window := layout.ListWindowFor(total, in.Scroll, height)

	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	for i := window.Start; i < window.End; i++ {
		row := rows[i]
		line := row.Text
		switch {
		case row.SourceLine == in.Cursor:
			sb.WriteString(renderSelectedRow(line))
		case row.isMatch(in.MatchLines):
			sb.WriteString(styleSearch.Render(line))
		case row.isSeparator(in.Lines):
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
