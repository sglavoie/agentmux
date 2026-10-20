// This file is the dashboard's project-scope vocabulary: what the live session
// set looks like when it is grouped by project, and how that grouping is drawn
// as a selectable list.
//
// The aggregation is deliberately ignorant of the project selector the operator
// currently has applied. Its caller removes only that one filter component
// before supplying rows, so every other component still constrains the tallies
// and the scope a narrowed list offers to switch to is a scope that still has
// sessions in it. That contextual property is why the aggregation is worth a
// package-level name at all, and it outlived the vertical rail it was first
// written for.

package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ProjectScope is one selectable project scope. Project is empty for All and is
// the project SELECTOR the filter will carry, which is also the identity a
// selection is preserved by across a refresh. Label is always operator-facing.
// Lead supplies the state icon for the aggregate.
//
// Project and Label are the same string for every real scope, and that is not
// redundancy waiting to be collapsed: the identity is matched case-insensitively
// against a filter the operator may have typed in any casing, while the label is
// the casing the leading session actually reported.
type ProjectScope struct {
	Project string
	Label   string
	Lead    session.RunningSession
	Counts  [3]int
	Group   int
}

// Key is the case-folded identity a scope is recognized by across a rebuild.
// All folds to the empty string, which no real project can produce because
// BuildProjectScopes skips a session with no label at all.
func (s ProjectScope) Key() string { return strings.ToLower(s.Project) }

// BuildProjectScopes aggregates the contextual session set into the project
// scopes the switcher offers. The caller removes only the active project
// selector before supplying rows, so every other filter continues to constrain
// the tallies.
func BuildProjectScopes(rows []session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) []ProjectScope {
	type aggregate struct {
		key string
		row ProjectScope
	}

	all := ProjectScope{Label: "All projects"}
	hasAllLead := false
	ordered := make([]*aggregate, 0, len(rows))
	for _, row := range rows {
		group := StateGroup(row, lastActivity, threshold)
		if group >= 0 && group < len(all.Counts) {
			all.Counts[group]++
		}
		if !hasAllLead || compareRowPriority(row, all.Lead, lastActivity, threshold) < 0 {
			all.Lead = row
			all.Group = group
			hasAllLead = true
		}

		label := ProjectLabel(row)
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		var agg *aggregate
		for _, candidate := range ordered {
			if strings.EqualFold(candidate.row.Label, label) {
				agg = candidate
				break
			}
		}
		if agg == nil {
			agg = &aggregate{key: key, row: ProjectScope{Project: label, Label: label, Lead: row, Group: group}}
			ordered = append(ordered, agg)
		} else if compareRowPriority(row, agg.row.Lead, lastActivity, threshold) < 0 {
			agg.row.Project = label
			agg.row.Label = label
			agg.row.Lead = row
			agg.row.Group = group
		}
		if group >= 0 && group < len(agg.row.Counts) {
			agg.row.Counts[group]++
		}
	}

	sort.SliceStable(ordered, func(i, j int) bool {
		if cmp := compareRowPriority(ordered[i].row.Lead, ordered[j].row.Lead, lastActivity, threshold); cmp != 0 {
			return cmp < 0
		}
		if ordered[i].key != ordered[j].key {
			return ordered[i].key < ordered[j].key
		}
		return ordered[i].row.Label < ordered[j].row.Label
	})

	out := make([]ProjectScope, 1, len(ordered)+1)
	out[0] = all
	for _, agg := range ordered {
		out = append(out, agg.row)
	}
	return out
}

// FindProjectScope returns the index of the scope whose identity is key, or -1.
// This is how a selection survives a refresh that reordered, added, or removed
// scopes: the overlay stores the key and asks the rebuilt list where it went,
// rather than carrying an index that named a different project a tick later.
func FindProjectScope(scopes []ProjectScope, key string) int {
	for i, scope := range scopes {
		if scope.Key() == key {
			return i
		}
	}
	return -1
}

// FilterProjectScopes narrows scopes to those whose label contains query,
// case-insensitively. All survives every query: it is the way back out of a
// scope, so a search that could hide it would be a search that could strand the
// operator inside a filter.
func FilterProjectScopes(scopes []ProjectScope, query string) []ProjectScope {
	if query == "" {
		return append([]ProjectScope(nil), scopes...)
	}
	q := strings.ToLower(query)
	out := make([]ProjectScope, 0, len(scopes))
	for _, scope := range scopes {
		if scope.Project == "" || strings.Contains(strings.ToLower(scope.Label), q) {
			out = append(out, scope)
		}
	}
	return out
}

// projectScopeTallyWidth is the width the three right-aligned counts occupy,
// including the two single-space gutters between them. Each count is as wide as
// the heading it sits under — need, act, idle — so the numbers line up with the
// words at every label length, and the column is padded rather than clipped.
// Four, three and four digits is far more live sessions in one urgency bucket of
// one project than a Kitty instance holds.
//
// It must stay equal to the width projectSwitcherHeader reserves on the right
// and to the width renderProjectScopeRow's tally actually renders at: the row
// derives its label budget by subtracting this from the content width, so a
// constant one column short pushes the row past the box and the final truncation
// eats the last tally instead of the padding it was meant to trim.
const projectScopeTallyWidth = 13

// ProjectSwitcherInput is the complete, explicit rendering state for the
// project switcher's inner content. Width is the content width inside the
// overlay box, and Height is the rows the scope list may spend including its
// ▲/▼ markers — the header and the search row are drawn outside that budget.
type ProjectSwitcherInput struct {
	Scopes    []ProjectScope
	Cursor    int
	Offset    int
	Width     int
	Height    int
	Query     string
	Searching bool
	// Applied is the project selector currently on the dashboard filter, which
	// is what the ● marker reports. It is independent of the cursor: the row the
	// keys are on and the scope the list is actually narrowed to are different
	// facts, and an overlay that showed only one of them could not say what
	// pressing enter would change.
	Applied string
}

// ProjectSwitcherHit names one rendered scope row.
type ProjectSwitcherHit struct {
	Rect    HitRect
	Project string
	Label   string
}

// ProjectSwitcherRender is the text of the switcher's inner content and the row
// rectangles the same pagination walk produced, in content-relative
// coordinates. The caller translates them by whatever inset its box and its
// overlay placement add.
type ProjectSwitcherRender struct {
	Text string
	Hits []ProjectSwitcherHit
}

// RenderProjectSwitcher draws the switcher's inner content: a heading carrying
// the tally legend, the transient search row, and a window of scopes.
func RenderProjectSwitcher(in ProjectSwitcherInput) ProjectSwitcherRender {
	width := in.Width
	if width < 0 {
		width = 0
	}
	var sb strings.Builder
	var hits []ProjectSwitcherHit
	y := 0

	sb.WriteString(projectSwitcherHeader(width))
	sb.WriteByte('\n')
	y++

	if in.Searching || in.Query != "" {
		line := styleDim.Render("/") + in.Query
		if in.Searching {
			line += "█"
		}
		sb.WriteString(layout.StyledTruncate(line, width, "…"))
		sb.WriteByte('\n')
		y++
	}

	if len(in.Scopes) == 0 {
		sb.WriteString(styleDim.Render(layout.ClampLine("  "+layout.Placeholder(layout.NoMatchText("projects", layout.SearchEditHint(in.Searching))), width)))
		sb.WriteByte('\n')
		return ProjectSwitcherRender{Text: sb.String()}
	}

	window := layout.ListWindowFor(len(in.Scopes), in.Offset, in.Height)
	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
		y++
	}
	for i := window.Start; i < window.End; i++ {
		sb.WriteString(renderProjectScopeRow(in.Scopes[i], i == in.Cursor, in.Applied, width))
		sb.WriteByte('\n')
		hits = append(hits, ProjectSwitcherHit{
			Rect: HitRect{Y: y, Width: width, Height: 1}, Project: in.Scopes[i].Project, Label: in.Scopes[i].Label,
		})
		y++
	}
	if window.ShowBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return ProjectSwitcherRender{Text: sb.String(), Hits: hits}
}

// projectSwitcherHeader names the list on the left and the three tally columns
// on the right, in the same words the status bar's breakdown uses. The rail this
// replaced abbreviated them to "N A I" because it had twenty columns to work
// with; an overlay can afford to say what the numbers are.
func projectSwitcherHeader(width int) string {
	right := layout.PadLeft("need", 4) + " " + layout.PadLeft("act", 3) + " " + layout.PadLeft("idle", 4)
	leftWidth := width - projectScopeTallyWidth - 1
	if leftWidth < 0 {
		leftWidth = 0
	}
	line := styleDim.Render(layout.PadRight(layout.Truncate("Project", leftWidth, "…"), leftWidth) + " " + right)
	return layout.StyledTruncate(line, width, "…")
}

// renderProjectScopeRow draws one scope: the cursor gutter, the applied marker,
// the aggregate's state icon, the label, and the three right-aligned tallies.
func renderProjectScopeRow(scope ProjectScope, selected bool, applied string, width int) string {
	cursor := " "
	if selected {
		cursor = "›"
	}
	isApplied := strings.EqualFold(scope.Project, applied)
	if scope.Project == "" {
		isApplied = applied == ""
	}
	scopeMark := " "
	if isApplied {
		scopeMark = "●"
	}
	icon := layout.PadIconCell(palette.StateIcon(scope.Lead.State))
	tally := layout.PadLeft(fmt.Sprintf("%d", scope.Counts[0]), 4) + " " +
		layout.PadLeft(fmt.Sprintf("%d", scope.Counts[1]), 3) + " " +
		layout.PadLeft(fmt.Sprintf("%d", scope.Counts[2]), 4)
	labelWidth := width - 7 - projectScopeTallyWidth
	if labelWidth < 0 {
		labelWidth = 0
	}
	label := layout.PadRight(layout.Truncate(scope.Label, labelWidth, "…"), labelWidth)
	line := layout.Truncate(cursor+scopeMark+" "+icon+" "+label+" "+tally, width, "…")
	if selected {
		return renderSelectedRow(line, false)
	}
	if isApplied {
		return styleEmptyHero.Render(line)
	}
	if scope.Group == attentionGroup {
		return styleAttentionRow.Render(line)
	}
	return line
}
