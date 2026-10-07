package worktrees

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ListInput is everything the Worktrees view needs to paint itself. The root
// model owns the state; this value is the projection of it that the leaf
// renders from, so the package never names the Bubble Tea model. Height is the
// terminal's own height and BodyHeight is what the root has left after its
// footer and status bar; they are distinct because a positive but very short
// terminal can drive BodyHeight to zero and still owe the list one row.
type ListInput struct {
	Width      int
	Height     int
	BodyHeight int

	// PathRow is the root-rendered "worktree path: …" row for the selected
	// worktree, or the empty string when nothing is selected. The root owns
	// it because the same row is drawn under the plan and session searches.
	PathRow string

	Entries  []actions.WorktreeView
	Filtered []actions.WorktreeView
	Cursor   int
	Offset   int

	Filter     string
	FilterMode bool

	Loading         bool
	ActionInFlight  bool
	RefreshErr      error
	RefreshWarnings []string
}

// VisibleHeight returns the number of worktree rows that fit in the root body
// area. Chrome is header (1) + filter bar (1) + blank (1), plus any selected
// worktree path row and the inline refresh error and warning rows.
func (in ListInput) VisibleHeight() int {
	if in.Height <= 0 {
		return 0
	}
	chrome := 3 + pathRowLines(in.PathRow) + in.noticeLines()
	available := in.BodyHeight - chrome
	if available < 1 {
		return 1
	}
	return available
}

// noticeLines reports how many wrapped rows the inline refresh error and
// warnings occupy at the current width.
func (in ListInput) noticeLines() int {
	width := layout.WrapWidth(in.Width)
	lines := 0
	if in.RefreshErr != nil {
		lines += len(layout.WrapHard("refresh error: "+in.RefreshErr.Error(), width))
	}
	for _, warning := range in.RefreshWarnings {
		lines += len(layout.WrapHard("warning: "+warning, width))
	}
	return lines
}

// Render renders the Worktrees view: header, filter bar, selected path, inline
// notices, and the scrollable worktree list.
func (in ListInput) Render() string {
	var sb strings.Builder
	header := "Worktrees"
	if in.ActionInFlight {
		header += "  " + styleDim.Render("(working…)")
	} else if in.Loading {
		header += "  " + styleDim.Render("(refreshing…)")
	} else {
		header += "  " + styleDim.Render(fmt.Sprintf("[%d/%d visible]", len(in.Filtered), len(in.Entries)))
	}
	sb.WriteString(layout.ClampLine(styleHeader.Render(header), in.Width))
	sb.WriteByte('\n')

	var filter string
	switch {
	case in.FilterMode:
		filter = styleSearch.Render("/") + in.Filter + "█"
	case in.Filter != "":
		filter = styleSearch.Render("/") + in.Filter
	default:
		filter = styleDim.Render("press / to filter worktrees")
	}
	sb.WriteString(layout.ClampLine(filter, in.Width))
	sb.WriteByte('\n')
	if in.PathRow != "" {
		sb.WriteString(in.PathRow)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	width := layout.WrapWidth(in.Width)
	if in.RefreshErr != nil {
		for _, line := range layout.WrapHard("refresh error: "+in.RefreshErr.Error(), width) {
			sb.WriteString(styleError.Render(line))
			sb.WriteByte('\n')
		}
	}
	for _, warning := range sortedStrings(in.RefreshWarnings) {
		for _, line := range layout.WrapHard("warning: "+warning, width) {
			sb.WriteString(styleStateWarning.Render(line))
			sb.WriteByte('\n')
		}
	}

	sb.WriteString(in.RenderList(in.VisibleHeight()))
	return sb.String()
}

// RenderList renders the scrollable worktree list at the given per-call row
// budget; non-positive height means "unbounded" and scroll indicators are
// suppressed.
func (in ListInput) RenderList(height int) string {
	if len(in.Filtered) == 0 {
		if in.Loading {
			return styleDim.Render("  (loading worktrees…)") + "\n"
		}
		if len(in.Entries) > 0 {
			return styleDim.Render(layout.ClampLine("  (no worktrees match — esc clears the filter)", in.Width)) + "\n"
		}
		return styleDim.Render(layout.ClampLine("  (no managed worktrees — w in Past Sessions forks a conversation into one)", in.Width)) + "\n"
	}

	window := layout.ListWindowFor(len(in.Filtered), in.Offset, height)
	var sb strings.Builder
	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}
	for i := window.Start; i < window.End; i++ {
		line := formatRow(in.Filtered[i], in.Width)
		if i == in.Cursor {
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

// pathRowLines reports how many rows a root-rendered path row occupies.
func pathRowLines(row string) int {
	if strings.TrimSpace(row) == "" {
		return 0
	}
	return 1
}

// sortedStrings copies and orders values so the global warning block renders
// deterministically.
func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
