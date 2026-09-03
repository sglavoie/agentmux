// Package dashboard owns the live-session dashboard: what a running session
// looks like as a row, which rows the active filter keeps, which state group
// each row belongs to, how many rows fit the current viewport, where the
// cursor lands after a refresh, and how the resulting list, chips, and
// completion popup paint.
//
// It is a domain leaf. It holds no Bubble Tea model and no program state of
// its own: every entry point takes the facts it needs explicitly, so the root
// package keeps ownership of the Model and adapts it at the call site. The
// package must not import the root tui package.
package dashboard

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// pulseColumnWidth is the visual width consumed by the recency-pulse column
// (1-cell glyph + 1-cell space). Only reserved when at least one row in the
// current view has an active pulse.
const pulseColumnWidth = 2

// activeLoopColumnWidth is the visual width reserved for the active
// agent-session-loop indicator (🔁 = 2 cells + 1 trailing space). It is only
// reserved when at least one rendered row has a matching active loop.
const activeLoopColumnWidth = 3

// ListInput is every fact the dashboard list renderer reads. The root builds
// one from its Model at the call site, which is what keeps this package free
// of Bubble Tea state.
//
// A non-positive Height means "unbounded": every row in Rows is rendered and
// the ▲/▼ scroll indicators are suppressed.
type ListInput struct {
	// Rows are the filtered, sorted rows currently on screen.
	Rows []session.RunningSession
	// Loading is true while the initial running-session snapshot is still in
	// flight. Later background refreshes keep rendering the last usable rows (or
	// the confirmed empty state) instead of returning to the loading hero.
	Loading bool
	// SessionCount is the size of the unfiltered running-session set. It only
	// distinguishes "no sessions running" from "all rows filtered out" in the
	// empty hero.
	SessionCount int
	// Filter is the active filter, read only for the empty hero's hint copy.
	Filter Filter

	Cursor int
	Offset int
	Width  int
	Height int

	Now           time.Time
	Cfg           *config.Config
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	Watchdog      map[int]protocol.WatchdogState

	// ActivePulse reports whether a window carries an active recency pulse.
	// Nil means no row pulses.
	ActivePulse func(windowID int) bool
	// LoopMarker returns the supervision marker bound to a row, when one is
	// positively identified. A guessed association would put a wrong state
	// next to a session, so a row this cannot identify keeps the marker it
	// already had. Nil means no row has a marker.
	LoopMarker func(s session.RunningSession) (string, bool)
}

func (in ListInput) group(s session.RunningSession) int {
	return StateGroup(s, in.LastActivity, in.IdleThreshold)
}

func (in ListInput) pulsing(windowID int) bool {
	return in.ActivePulse != nil && in.ActivePulse(windowID)
}

func (in ListInput) marker(s session.RunningSession) (string, bool) {
	if in.LoopMarker == nil {
		return "", false
	}
	return in.LoopMarker(s)
}

// RenderList renders the session list with explicit width and height budgets.
// The width budget controls per-row column truncation; the height budget caps
// the number of lines emitted by the windowed walk.
func RenderList(in ListInput) string {
	if len(in.Rows) == 0 {
		return renderEmptyHero(in)
	}

	acctColor := ProfileColorMap(in.Cfg)
	harnessColor := HarnessColorMap(in.Cfg)

	groupCounts := CountGroups(in.Rows, in.LastActivity, in.IdleThreshold)
	multiGroup := groupCounts > 1
	pulseActive := anyRowHasActivePulse(in)
	activityActive := anyRowHasActivity(in)
	now := in.Now

	total := len(in.Rows)
	startIdx := 0
	endIdx := total
	showTop := false
	showBottom := false

	if in.Height > 0 {
		// Defensive clamp for a stale offset (e.g. after a refresh shrank the
		// row set).
		offset := in.Offset
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		startIdx = offset

		budget := in.Height
		if startIdx > 0 {
			showTop = true
			budget--
		}
		if budget < 0 {
			budget = 0
		}

		// Two-pass fit: first see how many rows land inside the budget
		// without reserving the bottom indicator. If rows still remain after
		// the fit, re-fit with one fewer line so the ▼ slot is reserved.
		endIdx = fitRows(in, startIdx, budget, multiGroup)
		if endIdx < total {
			showBottom = true
			if budget > 0 {
				budget--
			}
			endIdx = fitRows(in, startIdx, budget, multiGroup)
		}
	}
	activeLoopActive := anyVisibleRowHasActiveLoop(in, startIdx, endIdx)
	tabNumberWidth := tabNumberColumnWidth(in.Rows)
	var activityWidth int
	if activityActive {
		activityWidth = activityColumnWidth(in.Rows, in.Watchdog, now)
	}

	var sb strings.Builder
	if showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	prevGroup := -1
	for i := startIdx; i < endIdx; i++ {
		s := in.Rows[i]
		g := in.group(s)
		if multiGroup && g != prevGroup {
			if prevGroup != -1 {
				sb.WriteByte('\n')
			}
			sb.WriteString(renderGroupLabel(g, countInGroup(in.Rows, g, in.LastActivity, in.IdleThreshold)))
			sb.WriteByte('\n')
			prevGroup = g
		}
		rowWidth := in.Width
		if multiGroup && rowWidth > 0 {
			rowWidth -= groupBorderWidth
		}
		if pulseActive && rowWidth > 0 {
			rowWidth -= pulseColumnWidth
		}
		var activityCell string
		if activityActive {
			activityCell = renderActivityCell(s, in.Watchdog[s.WindowID], now, activityWidth)
		}
		var activeLoopCell string
		if activeLoopActive {
			activeLoopCell = renderLoopCellFor(in, s)
		}
		tabNumberCell := renderTabNumberCell(s.TabNumber, tabNumberWidth)
		row := FormatRow(s, acctColor, harnessColor, in.Cfg, rowWidth, tabNumberCell, activeLoopCell, activityCell)
		var styledRow string
		switch {
		case i == in.Cursor:
			styledRow = renderSelectedRow(row)
		case s.Locked:
			styledRow = styleDim.Render(row)
		case g == 0:
			styledRow = styleAttentionRow.Render(row)
		default:
			styledRow = row
		}
		if multiGroup {
			styledRow = groupBorder(g) + " " + styledRow
		}
		if pulseActive {
			styledRow = pulsePrefix(in, s) + styledRow
		}
		// Safety clamp at the render boundary. FormatRow has a fixed-column
		// floor (MinRowWidth) it cannot shrink below, so when the pane is
		// narrower than that floor the composed row would otherwise draw past
		// the terminal edge. layout.StyledTruncate is ANSI-aware and keeps the
		// trailing style resets intact after the visible cut.
		if in.Width > 0 {
			styledRow = layout.StyledTruncate(styledRow, in.Width, "…")
		}
		sb.WriteString(styledRow)
		sb.WriteByte('\n')
	}

	if showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
}

// fitRows returns the exclusive end index such that rendering rows
// [startIdx, end) consumes at most budget lines. The walk mirrors RenderList:
// when multiGroup is true, prevGroup initializes to -1 so the first row at
// startIdx always charges a group-label line, matching the renderer's "begin a
// window inside group 1 still emits the label" behavior. Subsequent visible
// group transitions also charge one blank separator line before the next group
// label.
func fitRows(in ListInput, startIdx, budget int, multiGroup bool) int {
	total := len(in.Rows)
	if budget <= 0 || startIdx >= total {
		return startIdx
	}
	used := 0
	prevGroup := -1
	end := startIdx
	for i := startIdx; i < total; i++ {
		cost := 1
		var g int
		if multiGroup {
			g = in.group(in.Rows[i])
			if g != prevGroup {
				cost++
				if prevGroup != -1 {
					cost++
				}
			}
		}
		if used+cost > budget {
			break
		}
		used += cost
		if multiGroup {
			prevGroup = g
		}
		end = i + 1
	}
	return end
}

// anyRowHasActivePulse reports whether any row carries an active
// recency-pulse record. Used to decide whether to reserve the pulse column
// for the entire render pass so rows stay aligned.
func anyRowHasActivePulse(in ListInput) bool {
	for _, s := range in.Rows {
		if in.pulsing(s.WindowID) {
			return true
		}
	}
	return false
}

// anyVisibleRowHasActiveLoop reports whether the daemon's bulk state snapshot
// marks any row in the rendered window as looping. The column is reserved for
// the current window only, so off-screen active loops do not steal horizontal
// space.
func anyVisibleRowHasActiveLoop(in ListInput, startIdx, endIdx int) bool {
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > len(in.Rows) {
		endIdx = len(in.Rows)
	}
	for i := startIdx; i < endIdx; i++ {
		if in.Rows[i].Looping {
			return true
		}
		if _, ok := in.marker(in.Rows[i]); ok {
			return true
		}
	}
	return false
}

// renderLoopCellFor prefers the supervision marker when the run is exactly
// bound to this session, and otherwise keeps the daemon's own loop marker.
func renderLoopCellFor(in ListInput, s session.RunningSession) string {
	if marker, ok := in.marker(s); ok {
		return layout.PadRight(marker, activeLoopColumnWidth)
	}
	return renderActiveLoopCell(s.Looping)
}

func renderActiveLoopCell(active bool) string {
	if !active {
		return strings.Repeat(" ", activeLoopColumnWidth)
	}
	return layout.PadRight("🔁", activeLoopColumnWidth)
}

// anyRowHasActivity reports whether any row is running a busy stretch.
// Mirrors anyRowHasActivePulse: the activity column is reserved for the
// entire render pass when at least one row carries a timer, otherwise the
// column is omitted so rows recover their full directory budget.
func anyRowHasActivity(in ListInput) bool {
	for _, s := range in.Rows {
		if !s.BusySince.IsZero() {
			return true
		}
	}
	return false
}

// pulsePrefix returns the colored glyph (+ trailing space) for a pulsing
// row, or two spaces for a non-pulsing row so column alignment is preserved
// across the render pass.
func pulsePrefix(in ListInput, s session.RunningSession) string {
	if !in.pulsing(s.WindowID) {
		return strings.Repeat(" ", pulseColumnWidth)
	}
	return stateAccentStyle(s.State).Render(GroupBorderGlyph) + " "
}

// renderEmptyHero renders a centered hero block when there are no rows to
// show. The hint copy distinguishes "no sessions running" from "all rows
// filtered out".
func renderEmptyHero(in ListInput) string {
	hasSessions := in.SessionCount > 0
	hasFilters := !in.Filter.IsEmpty()

	title := "No running sessions."
	var hint string
	if in.Loading {
		title = "Loading sessions…"
		hint = "press  ?  for all keys"
	} else if hasSessions && hasFilters {
		title = "No sessions match the active filter."
		hint = "press  esc  to clear filters     press  ?  for all keys"
	} else {
		hint = "press  l  to launch     press  ?  for all keys"
	}

	width := in.Width
	if width <= 0 {
		width = layout.StyledWidth(title)
		if w := layout.StyledWidth(hint); w > width {
			width = w
		}
	}
	titleLine := layout.Center(styleEmptyHero.Render(title), width)
	hintLine := layout.Center(styleDim.Render(hint), width)
	return titleLine + "\n\n" + hintLine + "\n"
}
