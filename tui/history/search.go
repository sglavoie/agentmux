package history

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

// SearchInput carries everything the Past Sessions view needs. The root owns
// the model, the transcript cache, and the chip picker; it projects them onto
// this value at the seam.
type SearchInput struct {
	Results []transcript.Result
	Cursor  int
	Offset  int

	Query      string
	SearchMode bool

	// Loading, CacheLoadInFlight, and IndexReady together decide which
	// in-progress label the header and the empty list show.
	Loading           bool
	CacheLoadInFlight bool
	IndexReady        bool
	RefreshInFlight   bool

	RefreshErr error
	// RefreshErrLabel prefixes RefreshErr. The root supplies it because the
	// status bar renders the same label outside this view.
	RefreshErrLabel string
	SearchErr       error

	Width      int
	Height     int
	BodyHeight int

	// Path is the transcript path of the selected result, rendered as a dim
	// row above the list. Empty collapses the row.
	Path string

	// Chip values for the harness/profile/project/message filters. Empty means "any".
	HarnessChip       string
	ProfileChip       string
	ProjectChip       string
	MessageSourceChip string

	// ProfileColors maps a profile name to its palette color name.
	ProfileColors map[string]string

	// ContextualMeta lets the root fold live-session context into a cached
	// transcript's metadata before the row is formatted. Nil is identity.
	ContextualMeta func(transcript.SessionMeta) transcript.SessionMeta
}

// VisibleHeight returns the number of result rows that fit in the root body
// area. The root owns the footer and sticky status row, so this budget
// subtracts only the lines Render writes above the scrollable results plus any
// inline error rows.
func (in SearchInput) VisibleHeight() int {
	if in.Height <= 0 {
		return 0
	}
	// header (1) + chip bar (1) + query bar (1) + blank (1), plus the
	// selected transcript path when available.
	avail := in.BodyHeight - 4 - pathRowLines(in.Path) - in.errorLines()
	if avail < 1 {
		return 1
	}
	return avail
}

func (in SearchInput) errorLines() int {
	n := 0
	if in.RefreshErr != nil {
		n++
	}
	if in.SearchErr != nil {
		n++
	}
	return n
}

// Render renders the entire Past Sessions view: header, chip bar, query bar,
// and the results list. Mirrors the picker's single-pane layout.
func (in SearchInput) Render() string {
	var sb strings.Builder

	header := "Past Sessions"
	count := len(in.Results)
	if count > 0 || !in.Loading {
		header += fmt.Sprintf("  [%d result%s]", count, plural(count))
	}
	var states []string
	if label := in.loadingLabel(); label != "" {
		states = append(states, label)
	}
	if in.RefreshInFlight {
		states = append(states, styleBanner.Render("scanning transcripts…"))
	}
	if len(states) > 0 {
		header += "  (" + strings.Join(states, ", ") + ")"
	}
	sb.WriteString(layout.ClampLine(styleHeader.Render(header), in.Width))
	sb.WriteByte('\n')

	sb.WriteString(in.renderChips())
	sb.WriteByte('\n')

	var queryBar string
	switch {
	case in.SearchMode:
		queryBar = styleSearch.Render("/") + in.Query + "█"
	case in.Query != "":
		queryBar = styleSearch.Render("/") + in.Query
	default:
		queryBar = styleDim.Render("press / to search · enter view · c continue · f fork · w worktree fork · H handoff · E edit handoff")
	}
	sb.WriteString(layout.ClampLine(queryBar, in.Width))
	sb.WriteByte('\n')

	if row := renderPathRow("session path", in.Path, in.Width); row != "" {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	if err := in.RefreshErr; err != nil {
		sb.WriteString(styleError.Render(in.RefreshErrLabel+": "+err.Error()) + "\n")
	}
	if err := in.SearchErr; err != nil {
		sb.WriteString(styleError.Render("search error: "+err.Error()) + "\n")
	}

	sb.WriteString(in.RenderList(in.VisibleHeight()))
	return sb.String()
}

// renderChips returns a one-line summary of the active chip filters. Empty
// filters render as a single dim "(no filters)" placeholder so the line still
// occupies vertical space (keeps the layout stable).
func (in SearchInput) renderChips() string {
	var parts []string
	if v := in.HarnessChip; v != "" {
		parts = append(parts, styleChip.Render("[h] harness: "+v))
	} else {
		parts = append(parts, styleDim.Render("[h] harness: any"))
	}
	if v := in.ProfileChip; v != "" {
		parts = append(parts, styleChip.Render("[p] profile: "+v))
	} else {
		parts = append(parts, styleDim.Render("[p] profile: any"))
	}
	if v := in.ProjectChip; v != "" {
		parts = append(parts, styleChip.Render("[r] project: "+v))
	} else {
		parts = append(parts, styleDim.Render("[r] project: any"))
	}
	if v := in.MessageSourceChip; v != "" {
		parts = append(parts, styleChip.Render("[m] message: "+v))
	} else {
		parts = append(parts, styleDim.Render("[m] message: any"))
	}
	chips := "  " + strings.Join(parts, "   ")
	if in.Width > 0 {
		chips = layout.StyledTruncate(chips, in.Width, "…")
	}
	return chips
}

// RenderList renders the scrollable results list. height is the per-call row
// budget; non-positive height means "unbounded" and scroll indicators are
// suppressed.
func (in SearchInput) RenderList(height int) string {
	var sb strings.Builder

	total := len(in.Results)
	if total == 0 {
		if label := in.loadingLabel(); label != "" {
			sb.WriteString(styleDim.Render("  (" + label + ")"))
		} else if in.RefreshInFlight {
			sb.WriteString(styleDim.Render("  (scanning transcripts…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no sessions match)"))
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
		res := in.Results[i]
		if in.ContextualMeta != nil {
			res.Meta = in.ContextualMeta(res.Meta)
		}
		line := formatRow(res, in.ProfileColors, maxWidth, rowFilters{
			project: in.ProjectChip,
			profile: in.ProfileChip,
			harness: in.HarnessChip,
		})
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

func (in SearchInput) loadingLabel() string {
	if !in.Loading {
		return ""
	}
	if in.CacheLoadInFlight || !in.IndexReady {
		return "loading cache…"
	}
	return "searching…"
}
