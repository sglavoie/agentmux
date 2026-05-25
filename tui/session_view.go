package tui

import (
	"fmt"
	"strings"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/transcript"
)

// sessionViewVisibleHeight returns the number of viewer rows that fit in the
// current viewport. The viewer's chrome is leaner than the search view:
// header (1) + blank (1) + trailing newline (1) + two-line footer (2) = 5.
func sessionViewVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	avail := m.height - 5
	if avail < 1 {
		return 1
	}
	return avail
}

// renderSessionView renders the full ModeSessionView layout: a one-line
// header summarising the transcript and a scrollable list of role-coded
// message lines below.
func renderSessionView(m Model) string {
	var sb strings.Builder

	header := "Transcript"
	if m.viewerSession != nil {
		project := m.viewerSession.Project
		if project == "" {
			project = "(unknown)"
		}
		tag := m.viewerSession.Profile + "/" + m.viewerSession.Harness
		if m.viewerSession.Profile == "" {
			tag = m.viewerSession.Harness
		}
		header = fmt.Sprintf("Transcript: %s  [%s]", project, tag)
	}
	if m.viewerLoading {
		header += "  " + styleDim.Render("(loading…)")
	} else if len(m.viewerMatchLines) > 0 {
		header += "  " + styleSearch.Render(fmt.Sprintf("match %d/%d", m.viewerMatchIdx+1, len(m.viewerMatchLines)))
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteByte('\n')
	sb.WriteByte('\n')

	if err := m.viewerLoadErr; err != nil {
		sb.WriteString(styleError.Render("read error: "+err.Error()) + "\n")
	}

	height := sessionViewVisibleHeight(m)
	sb.WriteString(renderSessionViewBody(m, height))
	return sb.String()
}

// renderSessionViewBody renders the windowed message list. height is the row
// budget; the same windowing contract as renderSessionSearchList is used so
// scroll indicators stay consistent across the two modes.
func renderSessionViewBody(m Model, height int) string {
	var sb strings.Builder

	total := len(m.viewerMessages)
	if total == 0 {
		if m.viewerLoading {
			sb.WriteString(styleDim.Render("  (loading…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no messages)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	startIdx := 0
	endIdx := total
	showTop := false
	showBottom := false

	if height > 0 {
		offset := m.viewerScroll
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
		endIdx = startIdx + budget
		if endIdx > total {
			endIdx = total
		}
		if endIdx < total {
			showBottom = true
			if budget > 0 {
				budget--
			}
			endIdx = startIdx + budget
			if endIdx > total {
				endIdx = total
			}
		}
	}

	if showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	maxWidth := m.width
	if maxWidth <= 0 {
		maxWidth = 120
	}

	isMatchLine := make(map[int]bool, len(m.viewerMatchLines))
	for _, idx := range m.viewerMatchLines {
		isMatchLine[idx] = true
	}
	cursorLine := -1
	if len(m.viewerMatchLines) > 0 && m.viewerMatchIdx >= 0 && m.viewerMatchIdx < len(m.viewerMatchLines) {
		cursorLine = m.viewerMatchLines[m.viewerMatchIdx]
	}

	for i := startIdx; i < endIdx; i++ {
		line := formatViewerLine(m.viewerMessages[i], maxWidth)
		switch {
		case i == cursorLine:
			sb.WriteString(styleSelected.Render(line))
		case isMatchLine[i]:
			sb.WriteString(styleSearch.Render(line))
		default:
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
}

// formatViewerLine renders one Message as a single, role-coded line. The
// message body is collapsed onto one line (interior whitespace squashed)
// and truncated to the viewport width — a v1 trade-off that keeps the
// match-line bookkeeping simple (one line per message).
func formatViewerLine(msg transcript.Message, maxWidth int) string {
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	if role == "" {
		role = "?"
	}
	prefix := "  "
	switch role {
	case "user":
		prefix = "> "
	case "assistant":
		prefix = "  "
	case "tool":
		prefix = "· "
	case "system":
		prefix = "# "
	}

	body := collapseWhitespace(msg.Text)
	roleTag := fmt.Sprintf("[%s] ", role)
	full := prefix + roleTag + body

	full = runewidth.Truncate(full, maxRunes(maxWidth), "…")

	switch role {
	case "assistant":
		return styleStateBolt.Render(full)
	case "tool":
		return styleDim.Render(full)
	case "system":
		return styleDim.Render(full)
	default:
		return full
	}
}

// collapseWhitespace replaces every run of whitespace with a single space.
// One-line-per-message is a v1 simplification — multi-paragraph messages
// would otherwise blow up the line count and break n/N match nav.
func collapseWhitespace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		sb.WriteRune(r)
	}
	return strings.TrimSpace(sb.String())
}

// computeViewerMatches scans msgs for a case-insensitive substring of query
// and returns the indices of matching messages. Returns nil when query is
// empty so n/N have no effect.
func computeViewerMatches(msgs []transcript.Message, query string) []int {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	needle := strings.ToLower(q)
	var matches []int
	for i, msg := range msgs {
		if strings.Contains(strings.ToLower(msg.Text), needle) {
			matches = append(matches, i)
		}
	}
	return matches
}

// clampViewerScroll pins m.viewerScroll to [0, max(0, total - visible)] at
// the model's current dimensions. Mirrors clampDetailScroll: protects
// against a stale offset surviving a resize that shrinks the viewport.
func clampViewerScroll(m *Model) {
	total := len(m.viewerMessages)
	visible := sessionViewVisibleHeight(*m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.viewerScroll > maxOffset {
		m.viewerScroll = maxOffset
	}
	if m.viewerScroll < 0 {
		m.viewerScroll = 0
	}
}
