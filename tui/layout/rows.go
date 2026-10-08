// The pieces a selectable list is drawn from, stated without reference to any
// model: a word wrapper that measures in terminal cells, a one-line row with a
// right-aligned shortcut, a labelled rule that separates one band of rows from
// the next, a detail area that is always the same height, and a cell that
// keeps the second of two texts visible.
//
// They live here rather than beside the surface that first needed them because
// two surfaces need them and neither may import the other: the command palette
// in tui/commands and the per-row session menu that tui/sessionactions declares.
// The primitives themselves hold no state and know nothing about actions,
// commands, or sessions — a caller hands them text and a column budget.
//
// Every one of them measures with Width, so a label carrying an emoji, a CJK
// name, or a combining mark is laid out by the cells it paints rather than by
// the bytes it occupies. None of them accept ANSI-styled input: a caller styles
// the cells this file returns, which is what lets one row paint its label and
// its shortcut differently without this file knowing either style.

package layout

import "strings"

// DetailClipMarker is appended to the last line of a detail block whose content
// did not fit. It is exported because a caller asserting that a reason was shown
// whole has to be able to say what a clipped one looks like.
const DetailClipMarker = "…"

// WrapWords greedy-wraps s into lines no wider than width display cells,
// breaking at whitespace. Runs of whitespace collapse to a single space, so the
// result is a paragraph rather than a transcription.
//
// A word wider than the whole budget is hard-broken through WrapHard rather than
// left to overflow, which is what lets a caller treat every returned line as
// fitting: a long path or a URL cannot push a row past the column budget. The
// one exception is a SINGLE grapheme wider than the budget — a CJK ideograph or
// an emoji at width 1 — which is emitted whole at its own width, because a
// grapheme has no smaller piece to break into and dropping it would be a
// different string. That case is only reachable in a viewport too narrow to
// render anything, and one cell of overflow there is the least wrong answer.
//
// An empty or whitespace-only string yields no lines at all, so a caller can
// tell "nothing to say" from "one blank line".
func WrapWords(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}

	var lines []string
	current := ""
	for _, word := range fields {
		if Width(word) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			pieces := WrapHard(word, width)
			if len(pieces) == 0 {
				continue
			}
			lines = append(lines, pieces[:len(pieces)-1]...)
			// The tail of a hard-broken word is short enough to accept more
			// words after it, so it re-enters the greedy loop instead of being
			// flushed as its own line.
			current = pieces[len(pieces)-1]
			continue
		}
		switch {
		case current == "":
			current = word
		case Width(current)+1+Width(word) > width:
			lines = append(lines, current)
			current = word
		default:
			current += " " + word
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// ShortcutRow lays out one selectable row: label on the left, shortcut hard
// against the right edge. The two cells are returned separately because a row
// paints them differently — an accented key beside neutral label text — and
// concatenating them yields exactly width cells.
//
// The label gives way first. A shortcut is a reference value an operator scans
// a column of, so it is never truncated: when the row is too narrow to hold both
// with a separating cell, the shortcut is dropped entirely and the label takes
// the whole width, because half a key is a different key.
func ShortcutRow(label, shortcut string, width int) (labelCell, shortcutCell string) {
	if width < 1 {
		return "", ""
	}
	shortcutWidth := Width(shortcut)
	if shortcut == "" || shortcutWidth+2 > width {
		return PadRight(Truncate(label, width, "…"), width), ""
	}
	labelBudget := width - shortcutWidth
	return PadRight(Truncate(label, labelBudget-1, "…"), labelBudget), shortcut
}

// RuleRow returns a labelled horizontal rule exactly width cells wide: the label
// sits at the left with a single cell of air on either side, and box-drawing
// dashes run out to the right edge.
//
// It is what separates one band of rows from the next in one line rather than
// two. A band header plus a blank line reads the same and costs twice as much
// vertical room, which on a 24-row terminal is the difference between a list
// that scrolls and one that does not.
//
// An empty label yields a plain rule. A label too wide for the budget is
// truncated rather than allowed to push the row over.
func RuleRow(label string, width int) string {
	if width < 1 {
		return ""
	}
	const dash = "─"
	if strings.TrimSpace(label) == "" {
		return strings.Repeat(dash, width)
	}
	// One leading dash, a space, the label, a space: four cells of chrome. A
	// budget too small for any label at all falls back to a plain rule.
	if width < 6 {
		return strings.Repeat(dash, width)
	}
	label = Truncate(label, width-4, "…")
	head := dash + " " + label + " "
	tail := width - Width(head)
	if tail < 0 {
		tail = 0
	}
	return head + strings.Repeat(dash, tail)
}

// DetailBlock returns exactly height lines of explanation, each at most width
// cells wide. Paragraphs are wrapped in order; an empty paragraph becomes a
// blank separator line rather than disappearing, so a caller can space two
// sentences apart without knowing how either one wrapped.
//
// The fixed height is the whole point. A detail area sized to its current
// content moves every time the selection moves, so the row under the cursor
// walks away from the cursor; returning the same number of lines for every
// selection is what keeps the list still. Content that does not fit is clipped
// with DetailClipMarker on the last visible line, so a truncated explanation
// says that it is truncated instead of ending mid-sentence.
func DetailBlock(paragraphs []string, width, height int) []string {
	if height < 1 {
		return nil
	}
	if width < 1 {
		width = 1
	}

	var wrapped []string
	for _, paragraph := range paragraphs {
		lines := WrapWords(paragraph, width)
		if len(lines) == 0 {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, lines...)
	}

	block := make([]string, height)
	for i := range block {
		if i < len(wrapped) {
			block[i] = wrapped[i]
		}
	}
	if len(wrapped) > height {
		last := height - 1
		block[last] = Truncate(block[last], max(width-Width(DetailClipMarker), 0), "") + DetailClipMarker
	}
	return block
}

// leadShareNumerator and leadShareDenominator cap the lead of a FitLeadAndTail
// cell at two fifths of the budget while a tail follows it.
const (
	leadShareNumerator   = 2
	leadShareDenominator = 5
	// minTailCells is the least room worth giving a tail: below twice this,
	// a cell shows its lead alone rather than two unreadable stubs.
	minTailCells = 12
)

// LeadTailSeparator joins the two texts of a FitLeadAndTail cell. It is one
// constant so every list that pairs a summary with what followed it — Past
// Sessions and `amx session list` — reads the same way.
const LeadTailSeparator = " → "

// FitLeadAndTail fits "lead → tail" into budget cells. While a tail follows,
// the lead may take at most two fifths of the budget, so a long lead cannot
// push the tail off the cell; the tail then takes what is left and is cut with
// an ellipsis. With no tail, or a budget too small to show both, the lead
// alone fills the budget. A list row uses it when what tells two rows apart is
// the second half of the cell rather than the first.
func FitLeadAndTail(lead, tail string, budget int) string {
	if tail == "" || budget < minTailCells*2 {
		return Truncate(lead, budget, "…")
	}
	leadBudget := min(Width(lead), max(budget*leadShareNumerator/leadShareDenominator, minTailCells))
	return Truncate(Truncate(lead, leadBudget, "…")+LeadTailSeparator+tail, budget, "…")
}
