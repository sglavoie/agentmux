package history

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/searchquery"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ViewerInput carries everything the transcript viewer and its full-message
// overlay need. The two share one input because the overlay is a sub-view of
// the viewer: it reads the same message slice and the same viewport.
type ViewerInput struct {
	Messages []transcript.Message
	// Session is the transcript's metadata, nil before the load lands.
	Session *transcript.SessionMeta

	Loading bool
	LoadErr error

	MatchLines []int
	MatchIdx   int

	Cursor int
	Scroll int

	Width      int
	Height     int
	BodyHeight int

	// Path is the transcript's file path, rendered as a dim row under the
	// header. Empty collapses the row.
	Path string

	// Full-message overlay state. MessageIdx is negative when closed.
	MessageIdx    int
	MessageScroll int
}

// VisibleHeight returns the number of viewer rows that fit in the root body
// area. The root owns the footer and sticky status row, so this budget
// subtracts only the viewer header/path/blank chrome and any inline read-error
// row.
func (in ViewerInput) VisibleHeight() int {
	if in.Height <= 0 {
		return 0
	}
	avail := in.BodyHeight - 2 - pathRowLines(in.Path) - in.errorLines()
	if avail < 1 {
		return 1
	}
	return avail
}

func (in ViewerInput) errorLines() int {
	if in.LoadErr != nil {
		return 1
	}
	return 0
}

// Render renders the full transcript viewer layout: a one-line header
// summarising the transcript and a scrollable list of role-coded message lines
// below.
func (in ViewerInput) Render() string {
	var sb strings.Builder

	header := "Transcript"
	if in.Session != nil {
		project := in.Session.Project
		if project == "" {
			project = "(unknown)"
		}
		header = fmt.Sprintf("Transcript: %s  %s", layout.RenderTitleIcon(project), sessionProfileTag(*in.Session))
	}
	if in.Loading {
		header += "  " + styleDim.Render("(loading…)")
	} else if len(in.MatchLines) > 0 {
		header += "  " + styleSearch.Render(fmt.Sprintf("match %d/%d", in.MatchIdx+1, len(in.MatchLines)))
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteByte('\n')
	if row := renderPathRow("session path", in.Path, in.Width); row != "" {
		sb.WriteString(row)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')

	if err := in.LoadErr; err != nil {
		sb.WriteString(styleError.Render("read error: "+err.Error()) + "\n")
	}

	sb.WriteString(in.renderBody(in.VisibleHeight()))
	return sb.String()
}

// renderBody renders the windowed message list. height is the row budget; the
// same windowing contract as the Past Sessions list is used so scroll
// indicators stay consistent across the two modes.
func (in ViewerInput) renderBody(height int) string {
	var sb strings.Builder

	total := len(in.Messages)
	if total == 0 {
		if in.Loading {
			sb.WriteString(styleDim.Render("  (loading…)"))
		} else {
			sb.WriteString(styleDim.Render("  (no messages)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	window := layout.ListWindowFor(total, in.Scroll, height)

	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	maxWidth := in.Width
	if maxWidth <= 0 {
		maxWidth = 120
	}

	isMatchLine := make(map[int]bool, len(in.MatchLines))
	for _, idx := range in.MatchLines {
		isMatchLine[idx] = true
	}
	cursorLine := in.Cursor

	for i := window.Start; i < window.End; i++ {
		line := formatViewerLine(in.Messages[i], maxWidth)
		switch {
		case i == cursorLine:
			sb.WriteString(renderSelectedRow(line))
		case isMatchLine[i]:
			sb.WriteString(styleSearch.Render(line))
		default:
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if window.ShowBottom {
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

	body := CollapseWhitespace(msg.Text)
	roleTag := fmt.Sprintf("[%s] ", role)
	full := prefix + roleTag + body

	full = layout.Truncate(full, maxRunes(maxWidth), "…")

	switch role {
	case "assistant":
		return styleAssistant.Render(full)
	case "tool":
		return styleDim.Render(full)
	case "system":
		return styleDim.Render(full)
	default:
		return full
	}
}

// CollapseWhitespace replaces every run of whitespace with a single space.
// One-line-per-message is a v1 simplification — multi-paragraph messages
// would otherwise blow up the line count and break n/N match nav.
//
// It is exported for the root's viewer-copy regression, which asserts that
// yanking a message copies the backing text rather than this collapsed
// rendering; that boundary is a root behaviour, so the assertion stays there.
func CollapseWhitespace(s string) string {
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

// ComputeMatches scans msgs for a case-insensitive substring of query and
// returns the indices of matching messages. Returns nil when query is empty so
// n/N have no effect.
func ComputeMatches(msgs []transcript.Message, query string) []int {
	return ComputeMatchesForSource(msgs, query, "any")
}

// ComputeMatchesForSource limits match navigation while leaving all messages visible.
func ComputeMatchesForSource(msgs []transcript.Message, query, source string) []int {
	terms := searchquery.Parse(query)
	if terms.Empty() {
		return nil
	}
	var matches []int
	for i, msg := range msgs {
		if terms.ContainsAny(strings.ToLower(msg.SearchTextForSource(source))) {
			matches = append(matches, i)
		}
	}
	return matches
}

// ScrollToCursor advances the scroll offset the minimum amount needed to keep
// the cursor visible inside the windowed body, and returns the settled
// (cursor, scroll) pair. The body reserves one row for ▲ when scroll > 0 and
// one row for ▼ when more rows remain past the window, so the budget must
// subtract them too — otherwise the cursor falls off the bottom of the
// rendered list.
func (in ViewerInput) ScrollToCursor() (cursor, scroll int) {
	total := len(in.Messages)
	height := in.VisibleHeight()
	cursor, scroll = in.Cursor, in.Scroll
	if height <= 0 || total == 0 {
		return cursor, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	if cursor < scroll {
		scroll = cursor
	}
	for scroll < cursor {
		budget := height
		if scroll > 0 {
			budget--
		}
		if cursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if cursor-scroll+1 <= budget {
			break
		}
		scroll++
	}
	if scroll < 0 {
		scroll = 0
	}
	return cursor, scroll
}

// ClampScroll pins the scroll offset to [0, max(0, total - visible)] at the
// input's current dimensions. Mirrors DetailInput.ClampScroll: it protects
// against a stale offset surviving a resize that shrinks the viewport.
func (in ViewerInput) ClampScroll() int {
	maxOffset := len(in.Messages) - in.VisibleHeight()
	if maxOffset < 0 {
		maxOffset = 0
	}
	scroll := in.Scroll
	if scroll > maxOffset {
		scroll = maxOffset
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}
