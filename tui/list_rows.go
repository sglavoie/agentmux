package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/palette"
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

// harnessColWidth is the visual width of the harness badge text
// ("[copilot]" = 9 cells). The column is always reserved so rows stay
// aligned regardless of which harness (or none) a session uses.
const harnessColWidth = 9

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
	harnessColor := buildHarnessColorByProfileMap(m)

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
		row := formatRow(s, acctColor, harnessColor, rowWidth, ageCell)
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
// behavior.
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

func centerInWidth(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	pad := (width - w) / 2
	return strings.Repeat(" ", pad) + s
}

// groupBorder returns the colored 1-cell left border glyph for the given
// group (0 = attention, 1 = active, 2 = idle).
func groupBorder(group int) string {
	switch group {
	case 0:
		return styleGroupBorderAttention.Render(groupBorderGlyph)
	case 1:
		return styleGroupBorderActive.Render(groupBorderGlyph)
	default:
		return styleGroupBorderIdle.Render(groupBorderGlyph)
	}
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

// stateGroup classifies a session into one of three buckets:
//
//	0 → Needs Attention (key, stop, error, question)
//	1 → Active (bolt, warning, subagent, compact, or empty state within the
//	    idle threshold, or empty state with no activity record)
//	2 → Idle (empty state with stale activity, beyond the threshold)
//
// The warning state is grouped under Active so a session whose last tool
// call failed (carrying the ⚠️ icon) stays visible alongside bolt rows
// instead of hiding under Idle. The terminal error state (❗) stays under
// Needs Attention so stop failures remain high-signal. subagent and compact
// are mid-task states the daemon emits while work is in progress, so they
// classify alongside bolt; question is the elicitation prompt, which requires
// user input and so groups under Needs Attention.
//
// Without a daemon-recorded activity timestamp a session is treated as
// Active rather than Idle so a fresh session does not flicker into the
// Idle bucket the moment it appears.
func stateGroup(s session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	switch s.State {
	case "key", "stop", "error", "question":
		return 0
	case "bolt", "warning", "subagent", "compact":
		return 1
	}
	if s.State != "" {
		// Unknown/legacy state names — keep the previous behavior (Idle).
		return 2
	}
	ts, ok := lastActivity[s.WindowID]
	if !ok {
		return 1
	}
	if threshold > 0 && time.Since(ts) >= threshold {
		return 2
	}
	return 1
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

// harnessLabel returns the lowercase harness display name used in the
// badge column, or "" when the harness is unknown/empty.
func harnessLabel(h string) string {
	switch strings.ToLower(h) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	case "copilot":
		return "copilot"
	}
	return ""
}

// formatRow formats a single session row.
// Format: state_icon | account_prefix | [harness] | title | [age] | directory
// ageCell is empty when no row in the current view carries a watchdog entry;
// otherwise it is a pre-rendered fixed-width cell (ageColumnWidth cells,
// trailing space included) inserted immediately before the directory column.
func formatRow(s session.RunningSession, acctColor, harnessColor map[string]string, width int, ageCell string) string {
	// Lock indicator column. Always reserved (2 cells) so column alignment is
	// stable; only filled when the session is locked.
	lockCol := "  "
	if s.Locked {
		lockCol = palette.LockIcon()
		if w := runewidth.StringWidth(lockCol); w < lockColumnWidth {
			lockCol += strings.Repeat(" ", lockColumnWidth-w)
		}
	}

	// Mute indicator column. Always reserved (2 cells) so column alignment is
	// stable; only filled when the session is muted.
	muteCol := "  "
	if s.Muted {
		muteCol = palette.MuteIcon()
		if w := runewidth.StringWidth(muteCol); w < muteColumnWidth {
			muteCol += strings.Repeat(" ", muteColumnWidth-w)
		}
	}

	// State icon at the start for instant scanning.
	iconCol := "  "
	if s.State != "" {
		icon := palette.StateIcon(s.State)
		iconW := iconDisplayWidth(icon)
		if pad := 2 - iconW; pad > 0 {
			icon += strings.Repeat(" ", pad)
		}
		iconCol = icon
	}

	// Account prefix badge (first rune of account, uppercased). Use the first
	// rune rather than the first byte so a multibyte account name produces a
	// valid UTF-8 badge instead of a truncated invalid byte.
	acctBadge := " "
	if r, sz := utf8.DecodeRuneInString(s.Account); sz > 0 && r != utf8.RuneError {
		acctBadge = strings.ToUpper(string(r))
	}

	colorName := acctColor[s.Account]
	style := accountStyle(colorName)

	badge := style.Render("[" + acctBadge + "]")

	// Harness badge column, always reserved so rows stay aligned regardless
	// of which harness (or none) the session uses.
	var harnessCell string
	if label := harnessLabel(s.Harness); label != "" {
		text := "[" + label + "]"
		pad := harnessColWidth - runewidth.StringWidth(text)
		if pad < 0 {
			pad = 0
		}
		harnessCell = accountStyle(harnessColor[s.Account]).Render(text) + strings.Repeat(" ", pad)
	} else {
		harnessCell = strings.Repeat(" ", harnessColWidth)
	}

	// Title: use the project title padded to titleColWidth.
	title := runewidth.Truncate(runningSessionDisplayTitle(s), titleColWidth, "…")
	paddedTitle := title + strings.Repeat(" ", titleColWidth-runewidth.StringWidth(title))
	title = accountStyle(acctColor[s.Account]).Render(paddedTitle)

	// Directory: truncated.
	dir := s.Directory
	maxDir := dirMaxWidth
	if width > 0 {
		used := lockColumnWidth + muteColumnWidth + 3 + 4 + harnessColWidth + 1 + titleColWidth + 3
		// lock (2) + mute (2) + icon+space (3) + "[X] " badge (4) + harness (9)
		//   + space (1) + title (20) + separators before dir (3)
		available := width - used
		if available > 10 {
			maxDir = available
		}
	}
	if runewidth.StringWidth(dir) > maxDir {
		runes := []rune(dir)
		for runewidth.StringWidth(string(runes))+1 > maxDir {
			runes = runes[1:]
		}
		dir = "…" + string(runes)
	}
	dir = styleDim.Render(dir)

	return lockCol + muteCol + iconCol + " " + badge + " " + harnessCell + " " + title + "  " + ageCell + dir
}

// iconDisplayWidth returns the visual cell width of a state icon. We rely
// on go-runewidth's measurement; FE0F variation selectors are reported as
// zero-width which matches how Kitty actually renders these glyphs as a
// single cell. The caller (padIconCell / formatRow) is responsible for
// padding to the 2-cell icon column.
func iconDisplayWidth(icon string) int {
	return runewidth.StringWidth(icon)
}

// padIconCell returns icon padded to exactly 2 visual cells so the icon
// column stays a stable width regardless of which state glyph is rendered.
func padIconCell(icon string) string {
	w := iconDisplayWidth(icon)
	if w >= 2 {
		return icon
	}
	return icon + strings.Repeat(" ", 2-w)
}

// buildProfileColorMap maps profile name → palette color name resolved from
// the profile's required Color field. Used for elements that should track the
// profile (project title, [X] account badge, account chips, detail/preview
// header).
func buildProfileColorMap(m Model) map[string]string {
	result := make(map[string]string, len(m.cfg.Accounts))
	for _, acc := range m.cfg.Accounts {
		result[acc.Name] = acc.Color
	}
	return result
}

// buildHarnessColorByProfileMap maps profile name → palette color name
// resolved via the profile's harness. Used to color elements that should
// track the harness (e.g. the [claude] harness cell and the [profile/harness]
// picker badge).
func buildHarnessColorByProfileMap(m Model) map[string]string {
	result := make(map[string]string, len(m.cfg.Accounts))
	for _, acc := range m.cfg.Accounts {
		result[acc.Name] = m.cfg.HarnessColor(acc.EffectiveHarness())
	}
	return result
}
