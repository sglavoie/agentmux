package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

// detailMinVisible is the floor on the visible body height. Even on very
// short terminals we keep at least this many event rows visible so the pane
// remains useful.
const detailMinVisible = 3

// detailInnerChrome counts the lines renderDetail reserves inside the
// overlay box outside the scrollable event body: header line 1 (1) +
// header line 2 (1) + blank after header (1) + scroll-up indicator slot
// (1) + scroll-down indicator slot (1) + pane footer (1) = 6. The two
// indicator slots are reserved unconditionally so the layout does not
// jitter as the user scrolls.
const detailInnerChrome = 6

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

// clampDetailScroll pins m.detailScroll to the [0, maxOffset] range at the
// model's current dimensions. Called from the WindowSizeMsg handler so a
// resize that shrinks the viewport doesn't leave a stale offset above
// maxOffset — without the clamp the next j/k keypress would have to chew
// through the dead rows before the visible window reacts.
func clampDetailScroll(m *Model) {
	total, visible := detailScrollBounds(*m, detailVisibleHeight(*m))
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.detailScroll > maxOffset {
		m.detailScroll = maxOffset
	}
	if m.detailScroll < 0 {
		m.detailScroll = 0
	}
}

// detailOverlayInnerHeight returns the inner content height of the detail
// overlay box (everything inside the rounded border). The box uses
// Padding(0, 1) so vertical padding is zero; the only chrome it owns is
// the 2 border rows. The remaining 4-row margin keeps the modal floating
// rather than consuming the full terminal even on short screens. A 30-row
// cap prevents very tall terminals from rendering an oversized modal.
func detailOverlayInnerHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	// Inside the box we render the chrome counted by detailInnerChrome
	// (header 2 + blank + ▲ slot + ▼ slot + footer = 6) on top of at
	// least detailMinVisible body rows when possible. On shorter terminals,
	// shrink the body rows instead of rendering past the root body area.
	minInner := detailMinVisible + detailInnerChrome
	fit := rootBodyHeight(m) - styleOverlayBox.GetVerticalFrameSize()
	if fit < 0 {
		return 0
	}
	avail := fit - 4 // safety margin
	if avail < minInner {
		if fit < minInner {
			return fit
		}
		return minInner
	}
	if avail > 30 {
		avail = 30
	}
	if avail > fit {
		avail = fit
	}
	return avail
}

// detailVisibleHeight returns the number of scrollable event rows that fit
// inside the detail overlay's inner area (innerHeight minus the
// detailInnerChrome lines reserved by renderDetail).
func detailVisibleHeight(m Model) int {
	inner := detailOverlayInnerHeight(m)
	if inner <= 0 {
		return 0
	}
	v := inner - detailInnerChrome
	if v < detailMinVisible {
		if v < 0 {
			return 0
		}
		return v
	}
	return v
}

// detailScrollBounds returns the total number of events and the visible
// page size at the supplied content height. When the model has no height
// yet (pre-WindowSizeMsg) the visible window equals the total so scroll
// keys become no-ops naturally — mirroring helpScrollBounds.
func detailScrollBounds(m Model, visibleHeight int) (total, visible int) {
	total = len(m.detailEvents)
	if m.height == 0 {
		return total, total
	}
	if visibleHeight < 0 {
		visibleHeight = 0
	}
	visible = min(total, visibleHeight)
	return total, visible
}

// isTerminalAction reports whether an event action marks the end of a session
// run. Recoverable tool failures (`failure`) are intentionally excluded: they
// should stay visible in the log without drawing a false "session ended"
// separator.
func isTerminalAction(action string) bool {
	switch action {
	case "stop", "stop_failure":
		return true
	}
	return false
}

// sessionEndedSeparator renders a centered, dim "session ended" divider
// padded to width with horizontal-rule characters. The separator is
// emitted after each terminal event row so the user can see where the
// session ended in the most-recent-first event list.
func sessionEndedSeparator(width int) string {
	const label = " session ended "
	labelW := displayWidth(label)
	if width <= labelW {
		return styleDim.Render(strings.TrimSpace(label))
	}
	remaining := width - labelW
	left := remaining / 2
	right := remaining - left
	return styleDim.Render(strings.Repeat("─", left) + label + strings.Repeat("─", right))
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
	return displayPadRight(displayTruncate(s, width, "…"), width)
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
	colorName := projectIdentityColor(s, buildProfileColorMap(m), m.cfg)
	badge := accountStyle(colorName).Render("[" + acctBadge + "]")

	icon := ""
	if s.State != "" {
		icon = palette.StateIcon(s.State) + " "
	}

	line1 := fmt.Sprintf("%s %s%s — window %d", badge, icon, displayRenderTitleIcon(runningSessionDisplayTitle(s)), s.WindowID)

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
	if n := len(m.detailEvents); n > 0 && isTerminalAction(m.detailEvents[n-1].Action) {
		meta = append(meta, "session ended")
	}

	return styleHeader.Render(line1) + "\n" + styleDim.Render(strings.Join(meta, " · "))
}

// detailRow renders one event row. Suppressed events dim the arrow and append
// a marker glyph. The flexible detail column exposes tool, the ASL question
// preview, compact trace ID, and the decisive reason without disturbing the
// fixed event/action columns.
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
	details := make([]string, 0, 4)
	if ev.ToolName != "" {
		details = append(details, ev.ToolName)
	}
	// The daemon already collapsed and bounded the preview, so the shared
	// column truncation below is the only clipping applied here.
	if ev.QuestionPreview != "" {
		details = append(details, "q: "+ev.QuestionPreview)
	}
	if ev.TraceID != "" {
		traceID := ev.TraceID
		if len(traceID) > 8 {
			traceID = traceID[:8]
		}
		details = append(details, "t:"+traceID)
	}
	reason := ev.DecisionReason
	if reason == "" {
		reason = ev.SuppressionReason
	}
	if reason == "" {
		reason = ev.NotificationReason
	}
	if reason != "" {
		details = append(details, reason)
	}
	detailStr := ""
	if toolWidth > 0 {
		detailStr = displayTruncate(strings.Join(details, " · "), toolWidth, "…")
	}

	gap := strings.Repeat(" ", detailColumnGap)
	row := timeCell + gap + eventCell + gap + arrowCell + gap + actionCell + gap + detailToolStyle.Render(detailStr) + gap + markerGlyph
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
	hints := "t: timeline · ↑/↓ scroll · pgup/pgdn page · g/G top/bottom · d/esc close"
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

	total, visible := detailScrollBounds(m, detailVisibleHeight(m))

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

	rowWidth := detailRowWidth(m)
	now := time.Now()
	end := offset + visible
	if end > total {
		end = total
	}
	for _, ev := range reversed[offset:end] {
		sb.WriteString(detailRow(ev, now, rowWidth))
		sb.WriteByte('\n')
		if isTerminalAction(ev.Action) {
			sb.WriteString(sessionEndedSeparator(rowWidth))
			sb.WriteByte('\n')
		}
	}

	if end < total {
		sb.WriteString(styleDim.Render("  ▼"))
	}
	sb.WriteByte('\n')

	sb.WriteString(detailFooter(m, total, visible))
	sb.WriteByte('\n')

	return sb.String()
}

// detailBoxInnerWidth returns the styleOverlayBox `.Width(...)` value used
// for the detail overlay. The cap (96 cols) keeps the modal floating on
// very wide terminals; the m.width-4 clamp prevents the box from spilling
// off-screen on narrow ones.
func detailBoxInnerWidth(m Model) int {
	innerWidth := 96
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return innerWidth
}

// detailRowWidth returns the cell width an event row should occupy so it
// fits inside the overlay box's content area without wrapping. The 2-col
// subtraction matches styleOverlayBox's Padding(0, 1) — the border is
// already excluded from `.Width(...)` semantics in lipgloss.
func detailRowWidth(m Model) int {
	w := detailBoxInnerWidth(m) - 2
	if w < 1 {
		w = 1
	}
	return w
}

// renderDetailBox wraps renderDetail in styleOverlayBox so the per-session
// hook event log can be composited as a centered modal on top of the
// dimmed dashboard. When detailViewTimeline is set, renderTimeline is used
// instead (it returns a fully self-contained styled box).
func renderDetailBox(m Model) string {
	if m.detailViewTimeline {
		return renderTimeline(m)
	}
	return styleOverlayBox.Width(detailBoxInnerWidth(m)).Render(strings.TrimRight(renderDetail(m), "\n"))
}
