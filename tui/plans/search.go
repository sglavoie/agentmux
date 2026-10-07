package plans

import (
	"fmt"
	"strings"

	planindex "github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/tui/layout"
)

// SearchInput is everything the Plans browser needs to paint itself. The root
// model owns the state; this value is the projection of it that the leaf
// renders from, so the package never names the Bubble Tea model.
//
// Height is the terminal's own height and BodyHeight is what the root has left
// after its footer and status bar. They are distinct on purpose: a positive but
// very short terminal can drive BodyHeight to zero and still owe the list one
// row, exactly as the original root helper did.
type SearchInput struct {
	Width      int
	Height     int
	BodyHeight int

	// PathRow is the root-rendered "plan path: …" row for the selected
	// result, or the empty string when nothing is selected. The root owns it
	// because the same row is drawn under the session search and the
	// worktrees list.
	PathRow string

	Query      string
	SearchMode bool
	Loading    bool

	Results []planindex.Result
	Cursor  int
	Offset  int

	SortMode      planindex.SortMode
	FilterProject planindex.ProjectFilter

	RefreshErr error
	SearchErr  error
}

// VisibleHeight returns the number of result rows that fit in the root body
// area. Chrome is header (1) + project chip (1) + query bar (1) + blank (1),
// plus any selected plan path row and inline error rows.
func (in SearchInput) VisibleHeight() int {
	if in.Height <= 0 {
		return 0
	}
	avail := in.BodyHeight - 4 - pathRowLines(in.PathRow) - in.errorLines()
	if avail < 1 {
		return 1
	}
	return avail
}

// errorLines reports how many wrapped rows the inline refresh and search
// errors occupy at the current width.
func (in SearchInput) errorLines() int {
	width := layout.WrapWidth(in.Width)
	n := 0
	if err := in.RefreshErr; err != nil {
		n += len(layout.WrapHard("refresh error: "+err.Error(), width))
	}
	if err := in.SearchErr; err != nil {
		n += len(layout.WrapHard("search error: "+err.Error(), width))
	}
	return n
}

// Render renders the plan browser: header, project chip, query bar, and
// results list.
func (in SearchInput) Render() string {
	var sb strings.Builder

	header := "Plans"
	count := len(in.Results)
	if in.Loading {
		header += "  " + styleDim.Render("(scanning…)")
	} else {
		header += "  " + styleDim.Render(fmt.Sprintf("[%d result%s · sort: %s]",
			count, plural(count), sortLabel(in.SortMode)))
	}
	sb.WriteString(layout.ClampLine(styleHeader.Render(header), in.Width))
	sb.WriteByte('\n')
	sb.WriteString(in.renderProjectChip())
	sb.WriteByte('\n')

	var queryBar string
	switch {
	case in.SearchMode:
		queryBar = styleSearch.Render("/") + in.Query + "█"
	case in.Query != "":
		queryBar = styleSearch.Render("/") + in.Query
	default:
		queryBar = styleDim.Render("press / to search plan content")
	}
	sb.WriteString(layout.ClampLine(queryBar, in.Width))
	sb.WriteByte('\n')
	if in.PathRow != "" {
		sb.WriteString(in.PathRow)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	wrapWidth := layout.WrapWidth(in.Width)
	if err := in.RefreshErr; err != nil {
		for _, line := range layout.WrapHard("refresh error: "+err.Error(), wrapWidth) {
			sb.WriteString(styleError.Render(line) + "\n")
		}
	}
	if err := in.SearchErr; err != nil {
		for _, line := range layout.WrapHard("search error: "+err.Error(), wrapWidth) {
			sb.WriteString(styleError.Render(line) + "\n")
		}
	}

	sb.WriteString(in.RenderList(in.VisibleHeight()))
	return sb.String()
}

func (in SearchInput) renderProjectChip() string {
	label := "any"
	active := false
	switch in.FilterProject.Kind {
	case planindex.ProjectNamed:
		label = in.FilterProject.Name
		active = true
	case planindex.ProjectUnknown:
		label = "Unknown"
		active = true
	}
	chip := "[r] project: " + label
	if active {
		chip = styleChip.Render(chip)
	} else {
		chip = styleDim.Render(chip)
	}
	return layout.ClampLine("  "+chip, in.Width)
}

// emptyHint names the ways out of an empty result list that apply right now:
// esc clears only the text query, and a project filter survives it.
func (in SearchInput) emptyHint() string {
	var ways []string
	if in.Query != "" {
		ways = append(ways, "esc clears the query")
	}
	if in.FilterProject.Kind != planindex.ProjectAny {
		ways = append(ways, "r changes the project")
	}
	if len(ways) == 0 {
		return ""
	}
	return " — " + strings.Join(ways, " · ")
}

// emptyText is what an empty list says. With nothing narrowing it the list
// is empty because no plan was found at all, which is a different problem from
// a filter that matched nothing, and it points at where plans are read from.
func (in SearchInput) emptyText() string {
	hint := in.emptyHint()
	if hint == "" {
		return "(no saved plans found — plans_directory in config.toml says where to look)"
	}
	return "(no plans match" + hint + ")"
}

// RenderList renders the scrollable results list. height is the per-call row
// budget; non-positive height means "unbounded" and scroll indicators are
// suppressed.
func (in SearchInput) RenderList(height int) string {
	var sb strings.Builder

	total := len(in.Results)
	if total == 0 {
		if in.Loading {
			sb.WriteString(styleDim.Render("  (loading…)"))
		} else {
			sb.WriteString(styleDim.Render(layout.ClampLine("  "+in.emptyText(), in.Width)))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	window := layout.ListWindowFor(total, in.Offset, height)

	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	maxWidth := in.Width
	if maxWidth <= 0 {
		maxWidth = 120
	}

	for i := window.Start; i < window.End; i++ {
		line := FormatRow(in.Results[i], maxWidth)
		if i == in.Cursor {
			sb.WriteString(renderSelectedRow(line))
		} else {
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

// FormatRow returns a single-line summary of one plan result. Format:
// "  <when>  [<project>]  <name>  <N files>  [m matches]?  <preview>"
func FormatRow(res planindex.Result, maxWidth int) string {
	when := formatWhen(res.Meta.ModTime)
	name := res.Meta.Name
	if name == "" {
		name = "(unnamed)"
	}
	fileFrag := fmt.Sprintf("%d file%s", len(res.Meta.Files), plural(len(res.Meta.Files)))

	var matchFrag string
	if res.Matches > 0 {
		matchFrag = "  " + styleSearch.Render(fmt.Sprintf("(%d match%s)", res.Matches, matchPlural(res.Matches)))
	}

	project := projectLabel(res.Meta)
	projectFrag := styleChip.Render("[" + project + "]")
	if len(res.Meta.ProjectNames()) == 0 {
		projectFrag = styleDim.Render("[" + project + "]")
	}
	head := fmt.Sprintf("  %s  %s  %s  %s", when, projectFrag, name, styleDim.Render(fileFrag))
	body := head + matchFrag

	preview := res.Preview
	if res.Matches == 0 {
		preview = ""
	}
	if preview != "" {
		body += "  " + styleDim.Render(layout.Truncate(preview, maxRunes(maxWidth-layout.StyledWidth(body)-2), "…"))
	}
	return layout.StyledTruncate(body, maxWidth, "…")
}

// pathRowLines reports how many rows a root-rendered path row occupies.
func pathRowLines(row string) int {
	if strings.TrimSpace(row) == "" {
		return 0
	}
	return 1
}
