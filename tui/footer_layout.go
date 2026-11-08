package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

func footerLine(maxWidth int, separator string, fragments ...string) string {
	rendered := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		if fragment == "" {
			continue
		}
		rendered = append(rendered, styleFooter.Render(fragment))
	}
	return footerStyledLine(maxWidth, separator, rendered...)
}

func footerStyledLine(maxWidth int, separator string, rendered ...string) string {
	tokens := make([]footerToken, 0, len(rendered))
	for _, r := range rendered {
		if r == "" {
			continue
		}
		tokens = append(tokens, footerRenderedToken(r))
	}
	return footerLineFromTokens(maxWidth, separator, tokens...)
}

func footerLineFromTokens(maxWidth int, separator string, tokens ...footerToken) string {
	if len(tokens) == 0 {
		return ""
	}
	sep := styleFooter.Render(separator)
	sepW := layout.StyledWidth(sep)
	for i := 0; i < len(tokens)-1; i++ {
		tokens[i].sep = sep
		tokens[i].sepWidth = sepW
	}
	return wrapFooterLine("  ", "  ", tokens, maxWidth)
}

func footerRenderedToken(rendered string) footerToken {
	return footerToken{
		rendered: rendered,
		width:    layout.StyledWidth(rendered),
	}
}

func nonEmptyFooterLines(lines ...string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// listModeFooterLines assembles the unstyled physical blocks for ModeList.
// position is passed in by callers so layout budgeting can measure the same
// footer without recursively asking listVisibleHeight for the position text.
//
// The dashboard spends exactly one physical row on its footer at every width.
// The palette reflowed to two rows and the compact band spent one on a
// separately declared shortlist; both are gone in favour of a single
// width-budgeted row generated from the one palette, because a footer that
// grows a row is a session the list stops showing, and two hand-maintained
// spellings of "the essentials" drift apart.
// It is also always the SAME row. The bracket chord used to replace the palette
// outright while it was armed, because half its follow-up letters meant
// something else in the ordinary footer; the chord is retired along with the
// modal state that made a second spelling necessary, so there is one row and one
// vocabulary.
func listModeFooterLines(m Model, position string) []string {
	return []string{listFooterRow(m, listFooterTokens(m, position))}
}

// listFooterRow renders the dashboard's single footer row.
//
// Three spellings are tried in order, and the row never mixes them. The
// labelled palette is dropped from the bottom of the priority ladder until it
// fits. The visible help key leads to commands omitted for space. When only
// the essentials remain and they still do not fit with the marker, the marker
// goes: `? help` is on the row already, so losing `? more` costs a hint that
// something dropped, not the way to reach what dropped. When even the labelled
// essentials alone overflow, the same survivors are respelled as bare keys —
// all of them or none, so the operator who learned the row does not have to
// re-read it to find out which half changed wording.
func listFooterRow(m Model, tokens []prioritizedFooterToken) string {
	kept := make([]prioritizedFooterToken, len(tokens))
	copy(kept, tokens)

	fits := func(line string) bool { return m.width <= 0 || layout.StyledWidth(line) <= m.width }
	truncated := false
	for {
		line := footerSingleRow(kept, footerHintIndent, truncated, false)
		if fits(line) {
			return line
		}
		drop := prioritizedFooterDropIndex(kept)
		if drop < 0 {
			break
		}
		kept = append(kept[:drop], kept[drop+1:]...)
		truncated = true
	}
	if line := footerSingleRow(kept, footerHintIndent, false, false); fits(line) {
		return line
	}
	if line := footerSingleRow(kept, footerHintIndent, false, true); fits(line) {
		return line
	}
	// Narrower than the bare-key spelling of the essential band. The band is not
	// uniform at this width: four of its six controls are how an operator moves,
	// acts, reaches every other command, and finds out what those are, and a row
	// that kept any of the other two instead would name a capability while
	// dropping the way to the rest of them. They go last, in declaration order,
	// so the row degrades toward the four rather than toward its own left edge.
	for {
		drop := -1
		for i := len(kept) - 1; i >= 0; i-- {
			if !kept[i].irreducible {
				drop = i
				break
			}
		}
		if drop < 0 {
			break
		}
		kept = append(kept[:drop], kept[drop+1:]...)
		if line := footerSingleRow(kept, footerHintIndent, false, true); fits(line) {
			return line
		}
	}
	// Nothing left to give up. A clamped row still carries the leading keys.
	return layout.StyledTruncate(footerSingleRow(kept, footerHintIndent, false, true), m.width, "…")
}

// footerSingleRow joins tokens onto one physical line, separators included and
// no wrapping anywhere. short selects the bare-key spelling of every token that
// has one; the more marker is suppressed in that spelling because the row's own
// `?` is already the pointer at generated help.
func footerSingleRow(tokens []prioritizedFooterToken, indent string, truncated, short bool) string {
	sep := styleFooter.Render(footerHintSeparator)
	parts := make([]string, 0, len(tokens)+1)
	for _, t := range tokens {
		rendered := t.token.rendered
		if short && t.short.rendered != "" {
			rendered = t.short.rendered
		}
		if rendered == "" {
			continue
		}
		parts = append(parts, rendered)
	}
	if truncated && !short && !strings.Contains(ansi.Strip(strings.Join(parts, " ")), "? help") {
		parts = append(parts, chrome.Binding("?", "more"))
	}
	return indent + strings.Join(parts, sep)
}

// listFooterShortKey is the bare-key spelling of one key. Only a key whose own
// name is wider than the glyph it prints gets respelled; everything else is
// already one or three cells and reads the same either way.
func listFooterShortKey(key string) string {
	if strings.HasPrefix(key, "enter") {
		return "↵"
	}
	return key
}

// footerShortToken renders a bare key in the spelling the fallback row uses,
// keeping an unavailable binding muted exactly as the labelled row does.
func footerShortToken(key string, available bool) footerToken {
	style := styleFooterKey
	if !available {
		style = styleFooterUnavailableKey
	}
	return footerRenderedToken(style.Render(listFooterShortKey(key)))
}

// zebraFooterLines flattens wrapped footer blocks into physical rows, then
// applies the shared root-footer zebra treatment. The first row receives the
// dark background and is padded to the viewport width; the next row retains
// the terminal's default background, and so on.
func zebraFooterLines(lines []string, maxWidth int) []string {
	rows := strings.Split(strings.Join(lines, "\n"), "\n")
	for i := range rows {
		if i%2 == 0 {
			rows[i] = renderFooterStripeRow(rows[i], maxWidth)
		}
	}
	return rows
}

// renderFooterStripeRow adds a full-width background without flattening the
// foreground styles already embedded in the row. Lipgloss resets attributes
// after each styled token, so the background sequence is re-applied after
// every reset before the remaining content and trailing padding are emitted.
func renderFooterStripeRow(row string, maxWidth int) string {
	background := footerStripeBackgroundSequence()

	var sb strings.Builder
	sb.WriteString(background)
	sb.WriteString(strings.ReplaceAll(row, ansi.ResetStyle, ansi.ResetStyle+background))
	if maxWidth > 0 {
		if padding := maxWidth - layout.StyledWidth(row); padding > 0 {
			sb.WriteString(strings.Repeat(" ", padding))
		}
	}
	sb.WriteString(ansi.ResetStyle)
	return sb.String()
}

func footerStripeBackgroundSequence() string {
	return ansi.Style{}.BackgroundColor(theme.Color(theme.Footer)).String()
}

// listPositionFragment returns a `start–end of total` indicator (mirroring
// detailFooter) when the filtered list overflows the visible window.
// Returns "" when everything fits or the model has not yet been resized.
func listPositionFragment(m Model) string {
	return listPositionFragmentForVisible(m, listVisibleHeight(m))
}

// listPositionFragmentForVisible reports the position for an explicit list-pane
// height. The height is a LINE budget, not an item count, and the two are the
// same number only in a view that draws no chrome between its rows: in a
// multi-group urgency list the group labels and their separators are spent out
// of the same budget, and the ▼ marker costs one more. Comparing the height to
// the item count therefore concluded that every item fit while the renderer was
// drawing an overflow marker under three of seven of them.
//
// So the range and the overflow decision are both taken from the window the
// renderer actually emits. Suppression follows from that window rather than from
// a separate test: the fragment is dropped only when the first item is the top
// of the list and the last item is the bottom of it.
func listPositionFragmentForVisible(m Model, height int) string {
	// Counted in items, not sessions: the window is in item indices, so the
	// denominator has to be the same unit or the indicator drifts the moment a
	// line stands for more than one session.
	total := m.listItemCount()
	if total == 0 || m.height == 0 {
		return ""
	}
	start, end := dashboardScrollInput(&m, listShowsGroupHeadings(&m)).VisibleRange(m.listOffset, height)
	if start <= 0 && end >= total {
		return ""
	}
	if end <= start {
		// A budget too small for even one item still has a cursor parked on one,
		// and "4–3 of 7" is not a range. Report the single item the window is
		// pinned to rather than an empty one.
		end = start + 1
	}
	return fmt.Sprintf("%d–%d of %d", start+1, end, total)
}

// listFooterBudgetLineCount measures the reflowed ModeList footer using the
// same builders as renderFooter. Position text depends on the list height,
// while list height depends on footer rows, so iterate the small calculation
// to a stable row count rather than falling back to a hard-coded budget.
//
// The dashboard footer is one row at every width today, so the iteration
// converges on the first pass. It is kept rather than replaced by the constant
// because listModeFooterLines is the authority on how many rows it draws, and a
// hard-coded 1 here would be a second claim about that free to drift from it.
func listFooterBudgetLineCount(m Model) int {
	// The inspector no longer suppresses the footer, so there is no early return
	// here and there must not be one: this count is a term in the body-row floor
	// the inspector's own fit is judged against, and a footer that asked whether
	// the inspector fits would be asking itself.
	position := ""
	lineCount := countFooterPhysicalRows(listModeFooterLines(m, position))
	maxLineCount := lineCount
	for range 4 {
		visible := listVisibleHeightForFooterLines(m, lineCount)
		nextPosition := listPositionFragmentForVisible(m, visible)
		nextLineCount := countFooterPhysicalRows(listModeFooterLines(m, nextPosition))
		if nextLineCount > maxLineCount {
			maxLineCount = nextLineCount
		}
		if nextLineCount == lineCount && nextPosition == position {
			return nextLineCount
		}
		position = nextPosition
		lineCount = nextLineCount
	}
	return maxLineCount
}

func countFooterPhysicalRows(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	return len(strings.Split(strings.Join(lines, "\n"), "\n"))
}

// footerEntry describes one slot in the stable footer palette. The palette is
// rendered in declaration order regardless of the cursor row's state; only the
// styling of each entry changes (available vs unavailable) so users always see
// every action in the same sequence. `section` preserves each action's semantic
// bucket (row actions, launching, app-wide commands) inside the shared wrap.
// `priority` orders truncation when a palette cannot fit its row budget: 0 is
// never dropped, and higher numbers are dropped before lower ones.

// footerToken pairs a styled rendered string with its visible width and an
// optional separator that follows it within a section. Pre-measuring with
// layout.StyledWidth lets us account for ANSI styling correctly.
type footerToken struct {
	rendered string
	width    int
	sep      string
	sepWidth int
}

// wrapFooterLine renders tokens onto one or more lines, breaking when the
// next token's render width would exceed maxWidth. firstLineLead is the lead
// rendered before the first token of the first line; continuationIndent is
// the left margin applied to every subsequent line. The trailing separator
// of the last token on each line is dropped so wrap points stay clean.
//
// When maxWidth <= 0 (uninitialized width before the first WindowSizeMsg),
// the helper emits a single unwrapped line — matching pre-wrap behavior.
// A token whose width alone exceeds maxWidth is still placed on its own
// line; we never split mid-token, mirroring renderChips.
func wrapFooterLine(firstLineLead, continuationIndent string, tokens []footerToken, maxWidth int) string {
	if len(tokens) == 0 {
		return firstLineLead
	}
	if maxWidth <= 0 {
		var sb strings.Builder
		sb.WriteString(firstLineLead)
		for i, t := range tokens {
			sb.WriteString(t.rendered)
			if i < len(tokens)-1 {
				sb.WriteString(t.sep)
			}
		}
		return sb.String()
	}

	leadW := layout.StyledWidth(firstLineLead)
	contW := layout.StyledWidth(continuationIndent)

	var lines []string
	var cur strings.Builder
	cur.WriteString(firstLineLead)

	lineW := leadW
	lineHasToken := false
	pendingSep := ""
	pendingSepW := 0
	for _, t := range tokens {
		// Cost to extend the current line with this token: the prior
		// token's separator (if any) plus the token itself.
		cost := t.width
		if lineHasToken {
			cost += pendingSepW
		}
		if lineHasToken && lineW+cost > maxWidth {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(continuationIndent)
			lineW = contW
			lineHasToken = false
			cost = t.width
		}
		if lineHasToken {
			cur.WriteString(pendingSep)
		}
		cur.WriteString(t.rendered)
		lineW += cost
		lineHasToken = true
		pendingSep = t.sep
		pendingSepW = t.sepWidth
	}
	lines = append(lines, cur.String())

	// Final per-line hard clamp: a lead that alone exceeds maxWidth (the
	// ModeList state gloss, #1) or a single token wider than the terminal
	// (the legend entry #3 or the picker DSL hint #4) is never split by the
	// wrap loop above, so truncate every emitted display line to maxWidth.
	// Mirrors the per-row/per-line safety clamp in list_rows.go and picker.go.
	for i, line := range lines {
		lines[i] = layout.StyledTruncate(line, maxWidth, "…")
	}
	return strings.Join(lines, "\n")
}
