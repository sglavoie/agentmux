package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

// renderList renders the session list for the current model state at the
// model's terminal width, bounded by listVisibleHeight so the windowed walk
// honors the current viewport. Pre-WindowSizeMsg (height==0) the budget is
// unbounded so tests and the first paint render every row.
func renderList(m Model) string {
	if m.height == 0 {
		return renderListAt(m, m.width, 0)
	}
	return renderListAt(m, m.width, listVisibleHeight(m))
}

// pulseColumnWidth is the visual width consumed by the recency-pulse column
// (1-cell glyph + 1-cell space). Only reserved when at least one row in the
// current view has an active pulse.
const pulseColumnWidth = 2

// lockColumnWidth is the visual width reserved for the per-row lock indicator
// (🔒 = 2 cells, or 2 spaces when unlocked) prepended to every row so columns
// stay aligned regardless of which rows in the view are locked.
const lockColumnWidth = 2

// muteColumnWidth is the visual width reserved for the per-row mute indicator
// (🔇 = 2 cells, or 2 spaces when unmuted) rendered alongside the lock column so
// rows stay aligned regardless of which sessions in the view are muted.
const muteColumnWidth = 2

// minimumTabNumberColumnWidth fits a one-digit tab number, a colon, and one
// trailing space (for example, "1: "). Wider result sets expand this column
// dynamically so multi-digit numbers remain aligned.
const minimumTabNumberColumnWidth = 3

// activeLoopColumnWidth is the visual width reserved for the active
// agent-session-loop indicator (🔁 = 2 cells + 1 trailing space). It is only
// reserved when at least one rendered row has a matching active loop.
const activeLoopColumnWidth = 3

// ageWarningGlyph is appended to the activity text once the time since the
// last hook event has crossed the 80% warning band. Rendered in
// styleAgeWarning so it reads as "approaching stale" without competing with
// the row's icon column.
const ageWarningGlyph = "⏳"

// harnessColWidth is the visual width reserved for the harness cell. Sized
// to fit the widest possible text — a 2-cell emoji + a 1-cell space + a
// 7-cell label (`copilot`) — so rows stay aligned regardless of which
// harness (or none) a session uses and regardless of the configured emoji's
// cell width.
const harnessColWidth = 10

// renderListAt renders the session list with explicit width and height
// budgets. The width budget controls per-row column truncation; the height
// budget caps the number of lines emitted by the windowed walk. A non-positive
// height means "unbounded": every row in m.filtered is rendered and the
// ▲/▼ scroll indicators are suppressed. This preserves the historical
// behavior for callers (and tests) that do not yet care about windowing.
func renderListAt(m Model, width, height int) string {
	if len(m.filtered) == 0 {
		return renderEmptyHero(m, width)
	}

	acctColor := buildProfileColorMap(m)
	harnessColor := buildHarnessColorMap(m)

	groupCounts := countGroups(m.filtered, m.lastActivity, m.idleThreshold)
	multiGroup := groupCounts > 1
	pulseActive := anyRowHasActivePulse(m)
	activityActive := anyRowHasActivity(m)
	// Fall back to the wall clock for a Model built as a bare literal, which is
	// how most of this package's render tests construct their input.
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}

	total := len(m.filtered)
	startIdx := 0
	endIdx := total
	showTop := false
	showBottom := false

	if height > 0 {
		// Defensive clamp for a stale offset (e.g. after a refresh shrank
		// m.filtered). Mirrors the offset+visible>total clamp in
		// renderDetail (detail.go:310-316).
		offset := m.listOffset
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

		// Two-pass fit: first see how many rows land inside the budget
		// without reserving the bottom indicator. If rows still remain after
		// the fit, re-fit with one fewer line so the ▼ slot is reserved.
		endIdx = fitListRows(m, startIdx, budget, multiGroup)
		if endIdx < total {
			showBottom = true
			if budget > 0 {
				budget--
			}
			endIdx = fitListRows(m, startIdx, budget, multiGroup)
		}
	}
	activeLoopActive := anyVisibleRowHasActiveLoop(m, startIdx, endIdx)
	tabNumberWidth := tabNumberColumnWidth(m.filtered)
	var activityWidth int
	if activityActive {
		activityWidth = activityColumnWidth(m, now)
	}

	var sb strings.Builder
	if showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	prevGroup := -1
	for i := startIdx; i < endIdx; i++ {
		s := m.filtered[i]
		g := stateGroup(s, m.lastActivity, m.idleThreshold)
		if multiGroup && g != prevGroup {
			if prevGroup != -1 {
				sb.WriteByte('\n')
			}
			sb.WriteString(renderGroupLabel(g, countInGroup(m.filtered, g, m.lastActivity, m.idleThreshold)))
			sb.WriteByte('\n')
			prevGroup = g
		}
		rowWidth := width
		if multiGroup && rowWidth > 0 {
			rowWidth -= groupBorderWidth
		}
		if pulseActive && rowWidth > 0 {
			rowWidth -= pulseColumnWidth
		}
		var activityCell string
		if activityActive {
			activityCell = renderActivityCell(s, m.watchdog[s.WindowID], now, activityWidth)
		}
		var activeLoopCell string
		if activeLoopActive {
			activeLoopCell = renderLoopCellFor(m, s)
		}
		tabNumberCell := renderTabNumberCell(s.TabNumber, tabNumberWidth)
		row := formatRow(s, acctColor, harnessColor, m.cfg, rowWidth, tabNumberCell, activeLoopCell, activityCell)
		var styledRow string
		switch {
		case i == m.cursor:
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
			styledRow = pulsePrefix(m, s) + styledRow
		}
		// Safety clamp at the render boundary. formatRow has a fixed-column
		// floor (minListRowWidth) it cannot shrink below, so when the pane is
		// narrower than that floor the composed row would otherwise draw past
		// the terminal edge. displayStyledTruncate is ANSI-aware and keeps the
		// trailing style resets intact after the visible cut.
		if width > 0 {
			styledRow = displayStyledTruncate(styledRow, width, "…")
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

// fitListRows returns the exclusive end index such that rendering rows
// [startIdx, end) of m.filtered consumes at most budget lines. The walk
// mirrors renderListAt: when multiGroup is true, prevGroup initializes to -1
// so the first row at startIdx always charges a group-label line, matching
// the renderer's "begin a window inside group 1 still emits the label"
// behavior. Subsequent visible group transitions also charge one blank
// separator line before the next group label.
func fitListRows(m Model, startIdx, budget int, multiGroup bool) int {
	total := len(m.filtered)
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
			g = stateGroup(m.filtered[i], m.lastActivity, m.idleThreshold)
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

// anyRowHasActivePulse reports whether any filtered row carries an active
// recency-pulse record. Used to decide whether to reserve the pulse column
// for the entire render pass so rows stay aligned.
func anyRowHasActivePulse(m Model) bool {
	for _, s := range m.filtered {
		if m.hasActivePulse(s.WindowID) {
			return true
		}
	}
	return false
}

// anyVisibleRowHasActiveLoop reports whether the daemon's bulk state snapshot
// marks any row in the rendered window as looping. The column is reserved for
// the current window only, so off-screen active loops do not steal horizontal
// space.
func anyVisibleRowHasActiveLoop(m Model, startIdx, endIdx int) bool {
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > len(m.filtered) {
		endIdx = len(m.filtered)
	}
	for i := startIdx; i < endIdx; i++ {
		if m.filtered[i].Looping {
			return true
		}
		if _, ok := aslMarkerFor(m, m.filtered[i]); ok {
			return true
		}
	}
	return false
}

// renderLoopCellFor prefers the ASL state marker when the run is exactly bound
// to this session, and otherwise keeps the daemon's own loop marker. A guessed
// association would put a wrong state next to a session, so a row that ASL
// cannot positively identify keeps the marker it already had.
func renderLoopCellFor(m Model, s session.RunningSession) string {
	if marker, ok := aslMarkerFor(m, s); ok {
		return displayPadRight(marker, activeLoopColumnWidth)
	}
	return renderActiveLoopCell(s.Looping)
}

func renderActiveLoopCell(active bool) string {
	if !active {
		return strings.Repeat(" ", activeLoopColumnWidth)
	}
	return displayPadRight("🔁", activeLoopColumnWidth)
}

// anyRowHasActivity reports whether any filtered row is running a busy
// stretch. Mirrors anyRowHasActivePulse: the activity column is reserved for
// the entire render pass when at least one row carries a timer, otherwise
// the column is omitted so rows recover their full directory budget.
func anyRowHasActivity(m Model) bool {
	for _, s := range m.filtered {
		if !s.BusySince.IsZero() {
			return true
		}
	}
	return false
}

// formatAge returns the detail pane's age label for a single watchdog entry:
// `—` for an entry with no LastAction, otherwise the coarsely rounded time
// since the last hook event (`<n>m` under one hour, `<n>h` beyond).
func formatAge(ws protocol.WatchdogState, now time.Time) string {
	if ws.LastAction == "" {
		return "—"
	}
	elapsed := now.Sub(time.Unix(ws.LastEventAt, 0))
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed < time.Hour {
		return fmt.Sprintf("%dm", int(elapsed/time.Minute))
	}
	return fmt.Sprintf("%dh", int(elapsed/time.Hour))
}

// Run-timer display contract. Every band rolls over before its leading count
// reaches three digits and the last one saturates, which is what bounds any
// elapsed duration — however absurd — to runTimerMaxCells.
const (
	runTimerMaxCells      = 5
	runTimerHourBandLimit = 100 * time.Hour
	runTimerDayBandLimit  = 100 * 24 * time.Hour
	runTimerOverflowText  = "99d+"
)

// formatRunElapsed renders how long a row's current busy stretch has been
// running, in at most runTimerMaxCells cells: `—` for a row with no anchor,
// `M:SS` below one hour, `<n>h<MM>` below 100 hours, `<n>d<HH>` below 100 days,
// and the saturating `99d+` beyond that. Each band rolls over before its
// leading count reaches three digits, so the activity column can never outgrow
// its reserved width. Negative durations clamp to zero so clock skew between
// the daemon and the TUI cannot surface a count running backwards, mirroring
// formatRelativeAge.
func formatRunElapsed(busySince, now time.Time) string {
	if busySince.IsZero() {
		return "—"
	}
	elapsed := now.Sub(busySince)
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Hour:
		return fmt.Sprintf("%d:%02d", int(elapsed/time.Minute), int(elapsed/time.Second)%60)
	case elapsed < runTimerHourBandLimit:
		return fmt.Sprintf("%dh%02d", int(elapsed/time.Hour), int(elapsed/time.Minute)%60)
	case elapsed < runTimerDayBandLimit:
		return fmt.Sprintf("%dd%02d", int(elapsed/(24*time.Hour)), int(elapsed/time.Hour)%24)
	default:
		return runTimerOverflowText
	}
}

// activityCellText returns a row's rendered activity text and whether the
// staleness warning fires. The warning keeps the watchdog's 80%-of-threshold
// rule (ThresholdSec ≤ 0 disables it) but applies only to a row that has a
// timer: the reducer is authoritative for what the row displays, so a row
// whose legacy LastAction scalar has diverged from the reduced icon must not
// render a bare `— ⏳`.
func activityCellText(s session.RunningSession, ws protocol.WatchdogState, now time.Time) (text string, warning bool) {
	text = formatRunElapsed(s.BusySince, now)
	if s.BusySince.IsZero() || ws.LastAction == "" || ws.ThresholdSec <= 0 {
		return text, false
	}
	elapsed := now.Sub(time.Unix(ws.LastEventAt, 0))
	threshold := time.Duration(ws.ThresholdSec) * time.Second
	if elapsed*5 < threshold*4 {
		return text, false
	}
	return text + " " + ageWarningGlyph, true
}

// activityColumnWidth returns a stable width for every row in the current
// filtered result set: the widest rendered activity text plus one trailing
// space. Mirrors tabNumberColumnWidth — one scan per render pass, so the
// column cannot jitter between rows of the same frame.
func activityColumnWidth(m Model, now time.Time) int {
	widest := displayWidth("—")
	for _, s := range m.filtered {
		text, _ := activityCellText(s, m.watchdog[s.WindowID], now)
		if w := displayWidth(text); w > widest {
			widest = w
		}
	}
	return widest + 1
}

// renderActivityCell builds one activity cell padded to width. The trailing
// space is part of the cell so callers can concatenate it before the
// directory without an extra separator. Warning rows get the warning glyph
// appended and the whole cell styled in styleAgeWarning.
func renderActivityCell(s session.RunningSession, ws protocol.WatchdogState, now time.Time, width int) string {
	text, warning := activityCellText(s, ws, now)
	pad := width - 1 - displayWidth(text)
	if pad < 0 {
		pad = 0
	}
	cell := text + strings.Repeat(" ", pad)
	if warning {
		cell = styleAgeWarning.Render(cell)
	}
	return cell + " "
}

// pulsePrefix returns the colored glyph (+ trailing space) for a pulsing
// row, or two spaces for a non-pulsing row so column alignment is preserved
// across the render pass.
func pulsePrefix(m Model, s session.RunningSession) string {
	if !m.hasActivePulse(s.WindowID) {
		return strings.Repeat(" ", pulseColumnWidth)
	}
	return stateAccentStyle(s.State).Render(groupBorderGlyph) + " "
}

// renderEmptyHero renders a centered hero block when there are no rows to
// show. The hint copy distinguishes "no sessions running" from "all rows
// filtered out".
func renderEmptyHero(m Model, width int) string {
	hasSessions := len(m.sessions) > 0
	hasFilters := !m.activeFilter.IsEmpty()

	title := "No running sessions."
	var hint string
	if hasSessions && hasFilters {
		title = "No sessions match the active filter."
		hint = "press  esc  to clear filters     press  ?  for all keys"
	} else {
		hint = "press  l  to launch     press  ?  for all keys"
	}

	if width <= 0 {
		width = displayStyledWidth(title)
		if w := displayStyledWidth(hint); w > width {
			width = w
		}
	}
	titleLine := displayCenter(styleEmptyHero.Render(title), width)
	hintLine := displayCenter(styleDim.Render(hint), width)
	return titleLine + "\n\n" + hintLine + "\n"
}

// renderGroupLabel emits the left-aligned tag above the first row of a group.
// The label is indented to line up with the row content (border + space).
func renderGroupLabel(group, count int) string {
	label := fmt.Sprintf("%s (%d)", groupLabels[group], count)
	var styled string
	switch group {
	case 0:
		styled = styleGroupLabelAttention.Render(label)
	case 1:
		styled = styleGroupLabelActive.Render(label)
	default:
		styled = styleGroupLabelIdle.Render(label)
	}
	return strings.Repeat(" ", groupBorderWidth) + styled
}

func countGroups(sessions []session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	seen := make(map[int]bool)
	for _, s := range sessions {
		seen[stateGroup(s, lastActivity, threshold)] = true
	}
	return len(seen)
}

func countInGroup(sessions []session.RunningSession, group int, lastActivity map[int]time.Time, threshold time.Duration) int {
	n := 0
	for _, s := range sessions {
		if stateGroup(s, lastActivity, threshold) == group {
			n++
		}
	}
	return n
}

var groupLabels = [3]string{"Needs Attention", "Active", "Idle"}
