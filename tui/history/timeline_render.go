package history

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/layout"
)

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
			dashW := width - layout.Width(prefix) - layout.Width(suffix)
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
			dashW := width - layout.Width(prefix) - layout.Width(sepLabel) - layout.Width(suffix)
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

// RenderTimeline applies scroll offset into the pre-rendered lines and wraps
// everything in the overlay box. Called from RenderBox when ViewTimeline=true.
func (in DetailInput) RenderTimeline() string {
	innerWidth := in.boxInnerWidth()
	lines := in.timelineLines()
	visibleH := in.TimelineVisibleHeight()

	total := len(lines)
	offset := in.TimelineScroll
	if offset+visibleH > total {
		offset = total - visibleH
	}
	if offset < 0 {
		offset = 0
	}

	var sb strings.Builder
	sb.WriteString(in.Header())
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

	hints := chrome.JoinBindings(" · ",
		chrome.Binding("t", "flat list"),
		chrome.Binding("↑/↓", "scroll"),
		chrome.Binding("enter", "fold"),
		chrome.Binding("[/]", "phase"),
		chrome.Binding("z/Z", "collapse/expand"),
		chrome.Binding("s", "suppressed"),
		chrome.Binding("T", "timestamps"),
		chrome.Binding("?", "help"),
		chrome.Binding("esc/q/d", "close"),
	)
	sb.WriteString(hints)
	sb.WriteByte('\n')

	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(sb.String(), "\n"))
}
