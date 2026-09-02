package history

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// sessionEndedSeparator renders a centered, dim "session ended" divider
// padded to width with horizontal-rule characters. The separator is
// emitted after each terminal event row so the user can see where the
// session ended in the most-recent-first event list.
func sessionEndedSeparator(width int) string {
	const label = " session ended "
	labelW := layout.Width(label)
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

// Header renders the two-line pane header: badge + state icon + title +
// window id on line 1, and a dim metadata line on line 2 carrying the current
// state, threshold age (when known), and event count.
func (in DetailInput) Header() string {
	s, ok := in.detailSession()
	if !ok {
		// Fallback: the session disappeared between query dispatch and
		// render (closed mid-flight). Keep the pane usable instead of
		// blanking the header.
		header := fmt.Sprintf("session detail — window %d", in.WindowID)
		meta := fmt.Sprintf("session not found · %d events", len(in.Events))
		return styleHeader.Render(header) + "\n" + styleDim.Render(meta)
	}

	acctBadge := " "
	if r, sz := utf8.DecodeRuneInString(s.Account); sz > 0 && r != utf8.RuneError {
		acctBadge = strings.ToUpper(string(r))
	}
	colorName := dashboard.ProjectIdentityColor(s, dashboard.ProfileColorMap(in.Cfg), in.Cfg)
	badge := accountStyle(colorName).Render("[" + acctBadge + "]")

	icon := ""
	if s.State != "" {
		icon = palette.StateIcon(s.State) + " "
	}

	line1 := fmt.Sprintf("%s %s%s — window %d", badge, icon, layout.RenderTitleIcon(dashboard.SessionDisplayTitle(s)), s.WindowID)

	state := s.State
	if state == "" {
		state = "idle"
	}
	meta := []string{fmt.Sprintf("state=%s", state)}
	if ws, found := in.Watchdog[s.WindowID]; found && ws.LastAction != "" {
		meta = append(meta, fmt.Sprintf("age=%s", dashboard.FormatAge(ws, time.Now())))
	}
	meta = append(meta, fmt.Sprintf("%d events", len(in.Events)))
	if n := len(in.Events); n > 0 && isTerminalAction(in.Events[n-1].Action) {
		meta = append(meta, "session ended")
	}

	return styleHeader.Render(line1) + "\n" + styleDim.Render(strings.Join(meta, " · "))
}

// detailRow renders one event row. Suppressed events dim the arrow and append
// a marker glyph. The flexible detail column exposes tool, the ASL question
// preview, compact trace ID, and the decisive reason without disturbing the
// fixed event/action columns.
func detailRow(ev protocol.SessionEvent, now time.Time, width int) string {
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
		detailStr = layout.Truncate(strings.Join(details, " · "), toolWidth, "…")
	}

	gap := strings.Repeat(" ", detailColumnGap)
	row := timeCell + gap + eventCell + gap + arrowCell + gap + actionCell + gap + detailToolStyle.Render(detailStr) + gap + markerGlyph
	return detailRowStyle.Render(row)
}

// Footer renders the bottom hint strip: scroll position, total event count,
// and the key hints. Mirrors the dimmed footer used by the rest of the
// dashboard.
func (in DetailInput) Footer(total, visible int) string {
	pos := ""
	switch {
	case total == 0:
		pos = "0 events"
	case visible >= total:
		pos = fmt.Sprintf("%d events", total)
	default:
		start := in.Scroll + 1
		end := in.Scroll + visible
		if end > total {
			end = total
		}
		pos = fmt.Sprintf("%d–%d of %d", start, end, total)
	}
	hints := joinFooterBindings(" · ",
		renderFooterBinding("t", "timeline"),
		renderFooterBinding("↑/↓", "scroll"),
		renderFooterBinding("pgup/pgdn", "page"),
		renderFooterBinding("g/G", "top/bottom"),
		renderFooterBinding("d/esc", "close"),
	)
	return styleFooter.Render(pos+"   ") + hints
}

// Render renders the per-session hook event detail pane. The layout mirrors
// renderHelp: header + blank, optional ▲/▼ indicators bracketing the
// scrollable body, footer. On short or pre-resize terminals the indicator
// slots collapse but the rest of the chrome still renders so the user has the
// keymap and the event count.
func (in DetailInput) Render() string {
	var sb strings.Builder
	sb.WriteString(in.Header())
	sb.WriteString("\n\n")

	// Error short-circuits the body. Loading and empty likewise — the
	// indicator slots stay empty so the pane shape is stable.
	if in.Err != nil {
		sb.WriteString(styleError.Render(fmt.Sprintf("error: %v", in.Err)))
		sb.WriteByte('\n')
		sb.WriteString(styleDim.Render("try `amx daemon status` to inspect the daemon"))
		sb.WriteString("\n\n\n")
		sb.WriteString(in.Footer(0, 0))
		sb.WriteByte('\n')
		return sb.String()
	}
	if in.Loading && len(in.Events) == 0 {
		sb.WriteString("\n")
		sb.WriteString(styleDim.Render("Loading…"))
		sb.WriteString("\n\n\n")
		sb.WriteString(in.Footer(0, 0))
		sb.WriteByte('\n')
		return sb.String()
	}
	if len(in.Events) == 0 {
		sb.WriteString("\n")
		sb.WriteString(styleDim.Render("No events recorded for this session yet."))
		sb.WriteString("\n\n\n")
		sb.WriteString(in.Footer(0, 0))
		sb.WriteByte('\n')
		return sb.String()
	}

	total, visible := in.ScrollBounds(in.VisibleHeight())

	// Most-recent-first: the daemon serves events oldest-first, so we walk
	// the slice in reverse for display. Scroll is then a count of rows
	// skipped from the top of the reversed view.
	reversed := make([]protocol.SessionEvent, total)
	for i, ev := range in.Events {
		reversed[total-1-i] = ev
	}

	offset := in.Scroll
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

	rowWidth := in.detailRowWidth()
	now := in.now()
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

	sb.WriteString(in.Footer(total, visible))
	sb.WriteByte('\n')

	return sb.String()
}

// RenderBox wraps Render in styleOverlayBox so the per-session hook event log
// can be composited as a centered modal on top of the dimmed dashboard. When
// ViewTimeline is set, RenderTimeline is used instead (it returns a fully
// self-contained styled box).
func (in DetailInput) RenderBox() string {
	if in.ViewTimeline {
		return in.RenderTimeline()
	}
	return styleOverlayBox.Width(in.boxInnerWidth()).Render(strings.TrimRight(in.Render(), "\n"))
}
