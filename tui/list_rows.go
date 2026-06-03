package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/daemon"
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

// ageColumnWidth is the visual width consumed by the per-row age column
// (5 cells of text + 1 trailing space). Sized to fit a warning cell like
// "9m ⏳"; only reserved when at least one row carries a sweep-eligible
// LastAction in m.watchdog.
const ageColumnWidth = 6

// ageWarningGlyph is appended to the age text once the elapsed time has
// crossed the 80% warning band. Rendered in styleAgeWarning so it reads as
// "approaching stale" without competing with the row's icon column.
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
	ageActive := anyRowHasAge(m)
	now := time.Now()

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
		if ageActive && rowWidth > 0 {
			rowWidth -= ageColumnWidth
		}
		var ageCell string
		if ageActive {
			ageCell = renderAgeCell(m.watchdog[s.WindowID], now)
		}
		row := formatRow(s, acctColor, harnessColor, m.cfg, rowWidth, ageCell)
		var styledRow string
		switch {
		case i == m.cursor:
			styledRow = styleSelected.Render(row)
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

// anyRowHasAge reports whether any filtered row carries a sweep-eligible
// watchdog entry. Mirrors anyRowHasActivePulse: the age column is reserved
// for the entire render pass when at least one row has a non-empty
// LastAction, otherwise the column is omitted so rows recover their full
// directory budget.
func anyRowHasAge(m Model) bool {
	if len(m.watchdog) == 0 {
		return false
	}
	for _, s := range m.filtered {
		if ws, ok := m.watchdog[s.WindowID]; ok && ws.LastAction != "" {
			return true
		}
	}
	return false
}

// formatAge returns the per-row age label and the warning flag for a single
// watchdog entry. The text is `—` for rows with no LastAction; otherwise a
// coarsely rounded duration (`<n>m` under one hour, `<n>h` beyond). warning
// is true once the elapsed time has reached 80% of the resolved threshold,
// so the caller can flip the cell to styleAgeWarning. ThresholdSec ≤ 0
// disables the warning (no threshold configured for that window).
func formatAge(ws daemon.WatchdogState, now time.Time) (text string, warning bool) {
	if ws.LastAction == "" {
		return "—", false
	}
	elapsed := now.Sub(time.Unix(ws.LastEventAt, 0))
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed < time.Hour {
		text = fmt.Sprintf("%dm", int(elapsed/time.Minute))
	} else {
		text = fmt.Sprintf("%dh", int(elapsed/time.Hour))
	}
	if ws.ThresholdSec > 0 {
		threshold := time.Duration(ws.ThresholdSec) * time.Second
		if elapsed*5 >= threshold*4 {
			warning = true
		}
	}
	return text, warning
}

// renderAgeCell builds a fixed-width age cell sized to ageColumnWidth. The
// trailing space is part of the cell so callers can concatenate it before
// the directory without an extra separator. Warning rows get the warning
// glyph appended and the whole text styled in styleAgeWarning.
func renderAgeCell(ws daemon.WatchdogState, now time.Time) string {
	text, warning := formatAge(ws, now)
	if warning {
		text = text + " " + ageWarningGlyph
	}
	pad := ageColumnWidth - 1 - runewidth.StringWidth(text)
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
		width = lipgloss.Width(title)
		if w := lipgloss.Width(hint); w > width {
			width = w
		}
	}
	titleLine := centerInWidth(styleEmptyHero.Render(title), width)
	hintLine := centerInWidth(styleDim.Render(hint), width)
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
