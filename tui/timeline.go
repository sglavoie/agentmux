package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/daemon"
)

type timelinePhase struct {
	kind         string // "preamble" | "active" | "subagent" | "compact" | "gap" | "separator"
	startAt      time.Time
	endAt        time.Time // zero = ongoing
	events       []daemon.SessionEvent
	termEvent    *daemon.SessionEvent // event that closed this phase (nil = ongoing)
	contextLimit bool                 // phase terminated by context-limit probe
}

// phaseDuration returns a human-readable duration string, or "ongoing" if endAt is zero.
func (p timelinePhase) phaseDuration() string {
	if p.endAt.IsZero() {
		return "ongoing"
	}
	d := p.endAt.Sub(p.startAt)
	if d < 0 {
		d = 0
	}
	switch {
	case d < 60*time.Second:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh%02dm", h, m)
	}
}

// toolSummary collapses PreToolUse ToolName values into "bash ×3  write_file ×2" (top 4, freq desc).
func (p timelinePhase) toolSummary() string {
	counts := map[string]int{}
	for _, ev := range p.events {
		if ev.Event == "PreToolUse" && ev.ToolName != "" {
			counts[ev.ToolName]++
		}
	}
	if len(counts) == 0 {
		return ""
	}

	type kv struct {
		name  string
		count int
	}
	pairs := make([]kv, 0, len(counts))
	for name, count := range counts {
		pairs = append(pairs, kv{name, count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	if len(pairs) > 4 {
		pairs = pairs[:4]
	}

	result := ""
	for i, kv := range pairs {
		if i > 0 {
			result += "  "
		}
		result += fmt.Sprintf("%s ×%d", kv.name, kv.count)
	}
	return result
}

// groupIntoPhases walks events (oldest-first) and groups them into timeline phases.
func groupIntoPhases(events []daemon.SessionEvent) []timelinePhase {
	if len(events) == 0 {
		return []timelinePhase{{kind: "preamble"}}
	}

	var phases []timelinePhase
	var cur *timelinePhase
	var lastEnd time.Time // end time of the last appended phase

	maybeGap := func(at time.Time) {
		if !lastEnd.IsZero() && at.Sub(lastEnd) >= 60*time.Second {
			phases = append(phases, timelinePhase{kind: "gap", startAt: lastEnd, endAt: at})
			lastEnd = time.Time{}
		}
	}

	closeCur := func(at time.Time) {
		if cur != nil {
			cur.endAt = at
			phases = append(phases, *cur)
			lastEnd = at
			cur = nil
		}
	}

	ensureCur := func(kind string, at time.Time) {
		if cur == nil {
			maybeGap(at)
			cur = &timelinePhase{kind: kind, startAt: at}
		}
	}

	for i := range events {
		ev := events[i]
		at := time.Unix(ev.At, 0)

		if ev.ResetFlow {
			closeCur(at)
			phases = append(phases, timelinePhase{kind: "separator", startAt: at, endAt: at})
			lastEnd = time.Time{} // separator fills the visual gap; reset so no gap follows it
		}

		switch ev.Action {
		case "active", "subagent", "compact":
			if cur == nil || cur.kind != ev.Action {
				closeCur(at)
				maybeGap(at)
				cur = &timelinePhase{kind: ev.Action, startAt: at}
			}
		case "stop", "stop_failure", "failure", "permission", "elicitation":
			ensureCur("preamble", at)
			evCopy := ev
			cur.termEvent = &evCopy
			cur.contextLimit = ev.ContextLimit
			cur.events = append(cur.events, ev)
			closeCur(at)
			continue
		case "idle", "clear", "user_clear":
			ensureCur("preamble", at)
			cur.events = append(cur.events, ev)
			closeCur(at)
			continue
		default:
			ensureCur("preamble", at)
		}

		if cur != nil {
			cur.events = append(cur.events, ev)
		}
	}

	if cur != nil {
		phases = append(phases, *cur)
	}

	if len(phases) == 0 {
		return []timelinePhase{{kind: "preamble"}}
	}

	return phases
}

// phaseIcon returns the display icon for a phase kind.
func phaseIcon(kind string) string {
	switch kind {
	case "active":
		return "⚡"
	case "subagent":
		return "🤖"
	case "compact":
		return "🌀"
	default:
		return "·"
	}
}

// phaseKindLabel returns the display label for a phase kind.
func phaseKindLabel(kind string) string {
	switch kind {
	case "active":
		return "Active"
	case "subagent":
		return "Subagent"
	case "compact":
		return "Compact"
	default:
		return kind
	}
}

// timelinePhaseExpanded reports whether phase i should render expanded.
// Explicit fold entries take precedence; the default expands phases with ≤5
// events or the last content phase.
func timelinePhaseExpanded(idx int, phase timelinePhase, fold map[int]bool, lastContentIdx int) bool {
	if fold != nil {
		if v, ok := fold[idx]; ok {
			return v
		}
	}
	return len(phase.events) <= 5 || idx == lastContentIdx
}

// phaseTermLine renders the closing "└─" line for a phase, styled by the
// terminal action type.
func phaseTermLine(phase timelinePhase) string {
	if phase.termEvent == nil {
		return styleTimelineActive.Render("  └─ ⏩ ongoing")
	}
	switch phase.termEvent.Action {
	case "stop":
		return styleTimelineStop.Render("  └─ ⏹ Stop")
	case "stop_failure":
		if phase.contextLimit {
			return styleTimelineError.Render("  └─ ❗ Error · context limit")
		}
		return styleTimelineError.Render("  └─ ❗ Error")
	case "failure":
		return styleTimelineError.Render("  └─ ⚠ Failure")
	case "permission":
		return styleTimelineCompact.Render("  └─ 🔑 Permission pending")
	case "elicitation":
		return styleTimelineGap.Render("  └─ ❓ Elicitation")
	case "idle", "clear", "user_clear":
		return styleTimelineGap.Render("  └─ ⏸ Idle")
	default:
		return styleTimelineGap.Render("  └─ " + phase.termEvent.Action)
	}
}

// renderPhaseBlock renders the 2–N lines for a single active/subagent/compact phase.
func renderPhaseBlock(phase timelinePhase, expanded bool, showSuppressed bool, showTimestamps bool) []string {
	var lines []string

	foldStr := "▸"
	if expanded {
		foldStr = "▾"
	}

	startStr := phase.startAt.Format("15:04:05")
	endStr := "ongoing"
	if !phase.endAt.IsZero() {
		endStr = phase.endAt.Format("15:04:05")
	}
	dur := phase.phaseDuration()

	kindPart := foldStr + " " + phaseIcon(phase.kind) + " " + phaseKindLabel(phase.kind)
	var styledKind string
	switch phase.kind {
	case "active", "subagent":
		styledKind = styleTimelineActive.Render(kindPart)
	case "compact":
		styledKind = styleTimelineCompact.Render(kindPart)
	default:
		styledKind = kindPart
	}

	meta := fmt.Sprintf("   %s → %s   %s   %d events", startStr, endStr, dur, len(phase.events))
	headerLine := styledKind + meta
	if expanded {
		headerLine += "   " + styleTimelineExpanded.Render("[expanded]")
	}
	lines = append(lines, headerLine)

	if expanded {
		suppressedCount := 0
		for _, ev := range phase.events {
			if ev.Suppressed {
				suppressedCount++
				if !showSuppressed {
					continue
				}
			}
			row := "  " + styleTimelineTree.Render("│") + "  "
			if showTimestamps {
				row += time.Unix(ev.At, 0).Format("15:04:05") + "  "
			}
			row += padRight(ev.Event, 20)
			if ev.ToolName != "" {
				row += "  " + styleTimelineTool.Render(ev.ToolName)
			}
			if ev.Suppressed {
				row += "  ⊘"
			}
			lines = append(lines, row)
		}
		if suppressedCount > 0 && !showSuppressed {
			lines = append(lines, styleTimelineGap.Render(fmt.Sprintf("  │  …  %d more hidden (s: show)", suppressedCount)))
		}
	} else {
		summary := phase.toolSummary()
		if summary != "" {
			lines = append(lines, "  "+styleTimelineTree.Render("│")+"  "+styleTimelineTool.Render(summary))
		}
	}

	lines = append(lines, phaseTermLine(phase))
	return lines
}

// renderTimelineLines produces the full line slice for the timeline view.
// width is the inner render width (box width minus borders/padding).
// fold nil means use default fold logic (collapse phases with >5 events).
// showSuppressed: if false, suppressed events are omitted from expanded phases.
// showTimestamps: if true, event rows include HH:MM:SS prefix.
func renderTimelineLines(
	phases []timelinePhase,
	width int,
	fold map[int]bool,
	showSuppressed bool,
	showTimestamps bool,
) []string {
	var lines []string

	// Determine last non-gap/non-separator index for default fold logic.
	lastContentIdx := -1
	for i, p := range phases {
		if p.kind != "gap" && p.kind != "separator" {
			lastContentIdx = i
		}
	}

	for i, phase := range phases {
		switch phase.kind {
		case "preamble":
			if len(phase.events) == 0 {
				continue
			}
			first := time.Unix(phase.events[0].At, 0)
			timeStr := first.Format("15:04")
			if len(phase.events) == 1 {
				lines = append(lines, fmt.Sprintf("  %s  %s", timeStr, phase.events[0].Event))
			} else {
				last := phase.events[len(phase.events)-1]
				lines = append(lines, fmt.Sprintf("  %s  %s ─── %s", timeStr, phase.events[0].Event, last.Event))
			}

		case "gap":
			dur := phase.phaseDuration()
			suffix := " " + dur + " gap"
			prefix := "  ·  Idle "
			dashW := width - runewidth.StringWidth(prefix) - runewidth.StringWidth(suffix)
			if dashW < 0 {
				dashW = 0
			}
			lines = append(lines, styleTimelineGap.Render(prefix+strings.Repeat("─", dashW)+suffix))

		case "separator":
			ts := ""
			if !phase.startAt.IsZero() {
				ts = phase.startAt.Format("15:04:05")
			}
			const sepLabel = " /compact "
			prefix := "  ────"
			suffix := "──── " + ts
			dashW := width - runewidth.StringWidth(prefix) - runewidth.StringWidth(sepLabel) - runewidth.StringWidth(suffix)
			if dashW < 0 {
				dashW = 0
			}
			lines = append(lines, styleTimelineSep.Render(prefix+sepLabel+strings.Repeat("─", dashW)+suffix))

		default: // "active", "subagent", "compact"
			expanded := timelinePhaseExpanded(i, phase, fold, lastContentIdx)
			lines = append(lines, renderPhaseBlock(phase, expanded, showSuppressed, showTimestamps)...)
		}
	}
	return lines
}

// renderTimeline applies scroll offset into the pre-rendered lines and wraps
// everything in the overlay box. Called from renderDetailBox when detailViewTimeline=true.
func renderTimeline(m Model) string {
	phases := groupIntoPhases(m.detailEvents)
	innerWidth := detailBoxInnerWidth(m)
	// Padding(0,1) in styleOverlayBox subtracts 2 cols from the render width.
	renderWidth := innerWidth - 2
	if renderWidth < 1 {
		renderWidth = 1
	}

	lines := renderTimelineLines(phases, renderWidth, m.detailTimelineFold, m.detailShowSuppressed, m.detailShowTimestamps)

	innerH := detailOverlayInnerHeight(m)
	visibleH := innerH - detailInnerChrome
	if visibleH < detailMinVisible {
		visibleH = detailMinVisible
	}

	total := len(lines)
	offset := m.detailTimelineScroll
	if offset+visibleH > total {
		offset = total - visibleH
	}
	if offset < 0 {
		offset = 0
	}

	var sb strings.Builder
	sb.WriteString(detailHeader(m))
	sb.WriteString("\n\n")

	if offset > 0 {
		sb.WriteString(styleDim.Render("  ▲"))
	}
	sb.WriteByte('\n')

	end := offset + visibleH
	if end > total {
		end = total
	}
	for _, line := range lines[offset:end] {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	if end < total {
		sb.WriteString(styleDim.Render("  ▼"))
	}
	sb.WriteByte('\n')

	hints := "t: flat list · ↑/↓ scroll · enter: fold · z/Z collapse/expand · s: suppressed · T: timestamps · esc close"
	sb.WriteString(styleDim.Render(hints))
	sb.WriteByte('\n')

	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(sb.String(), "\n"))
}

// phaseHeaderLinePositions returns the starting line index (0-based) of each
// phase in the pre-rendered output from renderTimelineLines. The slice has
// one entry per phase in phases. Used by [ / ] navigation to jump between
// phase headers and by enter to identify which phase is at the scroll position.
func phaseHeaderLinePositions(phases []timelinePhase, fold map[int]bool, showSuppressed bool) []int {
	lastContentIdx := -1
	for i, p := range phases {
		if p.kind != "gap" && p.kind != "separator" {
			lastContentIdx = i
		}
	}

	positions := make([]int, len(phases))
	line := 0
	for i, phase := range phases {
		positions[i] = line
		switch phase.kind {
		case "preamble":
			if len(phase.events) > 0 {
				line++
			}
		case "gap", "separator":
			line++
		default:
			expanded := timelinePhaseExpanded(i, phase, fold, lastContentIdx)
			line += len(renderPhaseBlock(phase, expanded, showSuppressed, false))
		}
	}
	return positions
}

// isPhaseExpanded reports the current expanded state for a phase, consulting
// the fold map before falling back to the default (≤5 events → expanded).
func isPhaseExpanded(phase timelinePhase, idx int, fold map[int]bool) bool {
	if fold != nil {
		if v, ok := fold[idx]; ok {
			return v
		}
	}
	return len(phase.events) <= 5
}

// phaseAtScroll returns the index of the phase whose header is at or just
// above the supplied scroll offset. Returns -1 when positions is empty.
func phaseAtScroll(positions []int, scroll int) int {
	result := -1
	for i, pos := range positions {
		if pos <= scroll {
			result = i
		}
	}
	return result
}

// clampTimelineScroll pins m.detailTimelineScroll to [0, max(0, totalLines-visibleH)].
// Must be called after any change that affects the timeline line count
// (fold toggle, suppressed toggle).
func clampTimelineScroll(m *Model, phases []timelinePhase) {
	innerWidth := detailBoxInnerWidth(*m)
	renderWidth := innerWidth - 2
	if renderWidth < 1 {
		renderWidth = 1
	}
	lines := renderTimelineLines(phases, renderWidth, m.detailTimelineFold, m.detailShowSuppressed, m.detailShowTimestamps)
	innerH := detailOverlayInnerHeight(*m)
	visibleH := innerH - detailInnerChrome
	if visibleH < detailMinVisible {
		visibleH = detailMinVisible
	}
	maxOffset := len(lines) - visibleH
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.detailTimelineScroll > maxOffset {
		m.detailTimelineScroll = maxOffset
	}
	if m.detailTimelineScroll < 0 {
		m.detailTimelineScroll = 0
	}
}
