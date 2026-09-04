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

const (
	RailMinWidth = 20
	RailMaxWidth = 32
)

// RailRow is one selectable project scope. Project is empty for All; Label is
// always operator-facing. Lead supplies the state icon for the aggregate.
type RailRow struct {
	Project string
	Label   string
	Lead    session.RunningSession
	Counts  [3]int
	Group   int
}

// BuildRailRows aggregates the contextual session set into the project scopes
// shown beside Urgency. The caller removes only the active project selector
// before supplying rows, so every other filter continues to constrain the rail.
func BuildRailRows(rows []session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) []RailRow {
	type aggregate struct {
		key string
		row RailRow
	}

	all := RailRow{Label: "All"}
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
			agg = &aggregate{key: key, row: RailRow{Project: label, Label: label, Lead: row, Group: group}}
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

	out := make([]RailRow, 1, len(ordered)+1)
	out[0] = all
	for _, agg := range ordered {
		out = append(out, agg.row)
	}
	return out
}

// RailWidth returns the natural rail width clamped to its responsive contract.
func RailWidth(rows []RailRow) int {
	w := RailMinWidth
	for _, row := range rows {
		tally := fmt.Sprintf("%d %d %d", row.Counts[0], row.Counts[1], row.Counts[2])
		natural := 7 + layout.Width(row.Label) + layout.Width(tally)
		if natural > w {
			w = natural
		}
	}
	if w > RailMaxWidth {
		return RailMaxWidth
	}
	return w
}

// RailInput contains the complete, explicit rendering state for the rail.
type RailInput struct {
	Rows           []RailRow
	Cursor         int
	Offset         int
	Width          int
	Height         int
	AppliedProject string
	Focused        bool
}

// RailVisibleRange resolves the independently scrollable rail viewport. The
// header is outside the range; available room is spent on a top marker, rows,
// then a bottom marker without ever exceeding Height.
func RailVisibleRange(total, offset, height int) (start, end int, top, bottom bool) {
	if total <= 0 {
		return 0, 0, false, false
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		offset = total - 1
	}
	if height <= 0 {
		return offset, total, offset > 0, false
	}
	body := height - 1
	if body <= 0 {
		return offset, offset, false, false
	}
	top = offset > 0 && body >= 2
	room := body
	if top {
		room--
	}
	end = offset + room
	if end > total {
		end = total
	}
	bottom = end < total && room >= 2
	if bottom {
		end--
	}
	if end <= offset && room > 0 {
		end = offset + 1
		bottom = false
	}
	return offset, end, top, bottom
}

// ScrollRailToCursor returns an offset that keeps cursor visible.
func ScrollRailToCursor(total, cursor, offset, height int) int {
	if total <= 0 {
		return 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	if offset > cursor {
		offset = cursor
	}
	if offset < 0 {
		offset = 0
	}
	for offset < cursor {
		start, end, _, _ := RailVisibleRange(total, offset, height)
		if cursor >= start && cursor < end {
			break
		}
		offset++
	}
	return offset
}

// RenderRail draws a one-line header plus a window of aggregate scopes.
func RenderRail(in RailInput) string {
	width := in.Width
	if width < 0 {
		width = 0
	}
	start, end, top, bottom := RailVisibleRange(len(in.Rows), in.Offset, in.Height)
	var sb strings.Builder
	sb.WriteString(railHeader(width))
	sb.WriteByte('\n')
	if top {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		sb.WriteString(renderRailRow(in.Rows[i], i == in.Cursor, in.Focused, in.AppliedProject, width))
		sb.WriteByte('\n')
	}
	if bottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func railHeader(width int) string {
	right := "N A I"
	leftWidth := width - layout.Width(right) - 1
	if leftWidth < 0 {
		leftWidth = 0
	}
	line := styleDim.Render(layout.PadRight(layout.Truncate("Projects", leftWidth, "…"), leftWidth) + " " + right)
	return layout.StyledTruncate(line, width, "…")
}

func renderRailRow(row RailRow, selected, focused bool, appliedProject string, width int) string {
	cursor := " "
	if selected && focused {
		cursor = "›"
	}
	applied := strings.EqualFold(row.Project, appliedProject)
	if row.Project == "" {
		applied = appliedProject == ""
	}
	scope := " "
	if applied {
		scope = "●"
	}
	icon := layout.PadIconCell(palette.StateIcon(row.Lead.State))
	tally := fmt.Sprintf("%d %d %d", row.Counts[0], row.Counts[1], row.Counts[2])
	labelWidth := width - 7 - layout.Width(tally)
	if labelWidth < 0 {
		labelWidth = 0
	}
	label := layout.PadRight(layout.Truncate(row.Label, labelWidth, "…"), labelWidth)
	line := cursor + scope + " " + icon + " " + label + " " + tally
	line = layout.Truncate(line, width, "…")
	if selected && focused {
		return renderSelectedRow(line)
	}
	if applied {
		return styleEmptyHero.Render(line)
	}
	if row.Group == attentionGroup {
		return styleAttentionRow.Render(line)
	}
	return line
}
