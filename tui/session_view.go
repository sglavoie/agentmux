package tui

import (
	"fmt"
	"strings"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/transcript"
)

// sessionViewVisibleHeight returns the number of viewer rows that fit in the
// root body area. composeRootView owns the footer and sticky status row, so
// this budget subtracts only the viewer header/path/blank chrome and any
// inline read-error row.
func sessionViewVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	avail := rootBodyHeight(m) - 2 - pathRowLines(currentSessionViewPath(m)) - sessionViewErrorLines(m)
	if avail < 1 {
		return 1
	}
	return avail
}

func sessionViewErrorLines(m Model) int {
	if m.viewerLoadErr != nil {
		return 1
	}
	return 0
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
	if row := renderPathRow("session path", currentSessionViewPath(m), m.width); row != "" {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}
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

	window := flatListWindowFor(total, m.viewerScroll, height)

	if window.showTop {
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
	cursorLine := m.viewerCursor

	for i := window.start; i < window.end; i++ {
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

	if window.showBottom {
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

// scrollViewerToCursor advances m.viewerScroll the minimum amount needed to
// keep m.viewerCursor visible inside the windowed body. Mirrors
// scrollSessionSearchToCursor: the body reserves one row for ▲ when scroll
// > 0 and one row for ▼ when more rows remain past the window, so the budget
// must subtract them too — otherwise the cursor falls off the bottom of the
// rendered list.
func scrollViewerToCursor(m *Model) {
	total := len(m.viewerMessages)
	height := sessionViewVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.viewerScroll = 0
		return
	}
	if m.viewerCursor < 0 {
		m.viewerCursor = 0
	}
	if m.viewerCursor >= total {
		m.viewerCursor = total - 1
	}
	if m.viewerCursor < m.viewerScroll {
		m.viewerScroll = m.viewerCursor
	}
	for m.viewerScroll < m.viewerCursor {
		budget := height
		if m.viewerScroll > 0 {
			budget--
		}
		if m.viewerCursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if m.viewerCursor-m.viewerScroll+1 <= budget {
			break
		}
		m.viewerScroll++
	}
	if m.viewerScroll < 0 {
		m.viewerScroll = 0
	}
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
