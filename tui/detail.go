package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// detailChrome is the vertical-line budget the detail pane reserves outside
// the scrollable event body: header line 1 (1) + header line 2 (1) + blank
// after header (1) + scroll-up indicator slot (1) + scroll-down indicator
// slot (1) + pane footer (1) + the dashboard's render() trailing newline +
// footer (1) = 7. The two indicator slots are reserved unconditionally so
// the layout does not jitter as the user scrolls.
const detailChrome = 7

// detailMinVisible is the floor on the visible body height. Even on very
// short terminals we keep at least this many event rows visible so the pane
// remains useful.
const detailMinVisible = 3

// Detail-row column widths. The Tool column flexes to fill the rest of the
// terminal width; everything else is fixed so columns line up across rows.
const (
	detailColTime   = 8  // "12s ago" etc.
	detailColEvent  = 20 // raw hook event name
	detailColArrow  = 1  // "→" glyph
	detailColAction = 12 // resolved action name
	detailColMarker = 1  // "⊘" when suppressed
)

// detailColumnGap is the number of spaces inserted between adjacent
// columns. Splitting the gap out from the column widths makes the
// chrome-vs-flex maths in detailRow easier to reason about.
const detailColumnGap = 1

// detailScrollBounds returns the total number of events and the visible
// page size at the model's current height. Pre-WindowSizeMsg the visible
// page equals the total so scroll keys are no-ops; this mirrors the
// pre-resize behavior of helpScrollBounds.
func detailScrollBounds(m Model) (total, visible int) {
	total = len(m.detailEvents)
	if m.height == 0 {
		return total, total
	}
	visible = m.height - detailChrome
	if visible < detailMinVisible {
		visible = detailMinVisible
	}
	if visible > total {
		visible = total
	}
	return total, visible
}

// formatRelativeAge renders a compact "N{unit} ago" label for an event
// timestamp. Negative durations clamp to zero so a clock skew between the
// daemon and the TUI does not surface a misleading negative count.
func formatRelativeAge(at, now time.Time) string {
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

// detailSession looks up the currently-targeted session in the model. The
// detail-pane header relies on this to surface a title/account/state for
// the user even though only the WindowID survives across enterDetailMode.
func detailSession(m Model) (session.RunningSession, bool) {
	for _, s := range m.sessions {
		if s.WindowID == m.detailWindowID {
			return s, true
		}
	}
	return session.RunningSession{}, false
}

// padRight pads s to exactly width visual cells, truncating with `…` if it
// is too long. Used for fixed-width detail-row columns where downstream
// layout depends on stable widths.
func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	truncated := runewidth.Truncate(s, width, "…")
	w := runewidth.StringWidth(truncated)
	if w < width {
		truncated += strings.Repeat(" ", width-w)
	}
	return truncated
}

// detailHeader renders the two-line pane header: badge + state icon +
// title + window id on line 1, and a dim metadata line on line 2 carrying
// the current state, threshold age (when known), and event count.
func detailHeader(m Model) string {
	s, ok := detailSession(m)
	if !ok {
		// Fallback: the session disappeared between query dispatch and
		// render (closed mid-flight). Keep the pane usable instead of
		// blanking the header.
		header := fmt.Sprintf("session detail — window %d", m.detailWindowID)
		meta := fmt.Sprintf("session not found · %d events", len(m.detailEvents))
		return styleHeader.Render(header) + "\n" + styleDim.Render(meta)
	}

	acctBadge := " "
	if r, sz := utf8.DecodeRuneInString(s.Account); sz > 0 && r != utf8.RuneError {
		acctBadge = strings.ToUpper(string(r))
	}
	colorName := buildAccountColorMap(m)[s.Account]
	badge := accountStyle(colorName).Render("[" + acctBadge + "]")

	icon := ""
	if s.State != "" {
		icon = palette.StateIcon(s.State) + " "
	}

	line1 := fmt.Sprintf("%s %s%s — window %d", badge, icon, s.Base, s.WindowID)

	state := s.State
	if state == "" {
		state = "idle"
	}
	meta := []string{fmt.Sprintf("state=%s", state)}
	if ws, found := m.watchdog[s.WindowID]; found && ws.LastAction != "" {
		ageText, _ := formatAge(ws, time.Now())
		meta = append(meta, fmt.Sprintf("age=%s", ageText))
	}
	meta = append(meta, fmt.Sprintf("%d events", len(m.detailEvents)))

	return styleHeader.Render(line1) + "\n" + styleDim.Render(strings.Join(meta, " · "))
}

// detailRow renders one event row. Suppressed events dim the arrow and
// append a marker glyph; non-suppressed events render in the neutral row
// style with an accented tool name. The Tool column flexes to fill any
// remaining width.
func detailRow(ev daemon.SessionEvent, now time.Time, width int) string {
	timeCell := padRight(formatRelativeAge(time.Unix(ev.At, 0), now), detailColTime)
	eventCell := padRight(ev.Event, detailColEvent)
	actionCell := padRight(ev.Action, detailColAction)

	arrowGlyph := "→"
	var arrowCell string
	if ev.Suppressed {
		arrowCell = styleDim.Render(arrowGlyph)
	} else {
		arrowCell = arrowGlyph
	}

	markerGlyph := " "
	if ev.Suppressed {
		markerGlyph = "⊘"
	}

	fixed := detailColTime + detailColEvent + detailColArrow + detailColAction + detailColMarker
	gaps := 5 * detailColumnGap // five inter-column gaps
	toolWidth := width - fixed - gaps
	if toolWidth < 0 {
		toolWidth = 0
	}
	toolStr := ""
	if ev.ToolName != "" && toolWidth > 0 {
		toolStr = runewidth.Truncate(ev.ToolName, toolWidth, "…")
	}

	gap := strings.Repeat(" ", detailColumnGap)
	row := timeCell + gap + eventCell + gap + arrowCell + gap + actionCell + gap + detailToolStyle.Render(toolStr) + gap + markerGlyph
	return detailRowStyle.Render(row)
}

// detailFooter renders the bottom hint strip: scroll position, total event
// count, and the key hints. Mirrors the dimmed footer used by the rest of
// the dashboard.
func detailFooter(m Model, total, visible int) string {
	pos := ""
	switch {
	case total == 0:
		pos = "0 events"
	case visible >= total:
		pos = fmt.Sprintf("%d events", total)
	default:
		start := m.detailScroll + 1
		end := m.detailScroll + visible
		if end > total {
			end = total
		}
		pos = fmt.Sprintf("%d–%d of %d", start, end, total)
	}
	hints := "↑/↓ scroll · pgup/pgdn page · g/G top/bottom · d/esc close"
	return styleDim.Render(pos + "   " + hints)
}

// renderDetail renders the per-session hook event detail pane. The layout
// mirrors renderHelp: header + blank, optional ▲/▼ indicators bracketing
// the scrollable body, footer. On short or pre-resize terminals the
// indicator slots collapse but the rest of the chrome still renders so the
// user has the keymap and the event count.
func renderDetail(m Model) string {
	var sb strings.Builder
	sb.WriteString(detailHeader(m))
	sb.WriteString("\n\n")

	// Error short-circuits the body. Loading and empty likewise — the
	// indicator slots stay empty so the pane shape is stable.
	if m.detailErr != nil {
		sb.WriteString(styleError.Render(fmt.Sprintf("error: %v", m.detailErr)))
		sb.WriteByte('\n')
		sb.WriteString(styleDim.Render("try `amx daemon status` to inspect the daemon"))
		sb.WriteString("\n\n\n")
		sb.WriteString(detailFooter(m, 0, 0))
		sb.WriteByte('\n')
		return sb.String()
	}
	if m.detailLoading && len(m.detailEvents) == 0 {
		sb.WriteString("\n")
		sb.WriteString(styleDim.Render("Loading…"))
		sb.WriteString("\n\n\n")
		sb.WriteString(detailFooter(m, 0, 0))
		sb.WriteByte('\n')
		return sb.String()
	}
	if len(m.detailEvents) == 0 {
		sb.WriteString("\n")
		sb.WriteString(styleDim.Render("No events recorded for this session yet."))
		sb.WriteString("\n\n\n")
		sb.WriteString(detailFooter(m, 0, 0))
		sb.WriteByte('\n')
		return sb.String()
	}

	total, visible := detailScrollBounds(m)

	// Most-recent-first: the daemon serves events oldest-first, so we walk
	// the slice in reverse for display. detailScroll is then a count of
	// rows skipped from the top of the reversed view.
	reversed := make([]daemon.SessionEvent, total)
	for i, ev := range m.detailEvents {
		reversed[total-1-i] = ev
	}

	offset := m.detailScroll
	if offset+visible > total {
		offset = total - visible
	}
	if offset < 0 {
		offset = 0
	}

	if offset > 0 {
		sb.WriteString(styleDim.Render("  ▲"))
	}
	sb.WriteByte('\n')

	rowWidth := m.width
	if rowWidth <= 0 {
		rowWidth = 80
	}
	now := time.Now()
	end := offset + visible
	if end > total {
		end = total
	}
	for _, ev := range reversed[offset:end] {
		sb.WriteString(detailRow(ev, now, rowWidth))
		sb.WriteByte('\n')
	}

	if end < total {
		sb.WriteString(styleDim.Render("  ▼"))
	}
	sb.WriteByte('\n')

	sb.WriteString(detailFooter(m, total, visible))
	sb.WriteByte('\n')

	return sb.String()
}
