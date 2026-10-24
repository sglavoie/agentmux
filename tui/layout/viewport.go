package layout

import "strings"

// SectionLines splits a rendered view section into the terminal rows it
// occupies. A section that renders to nothing occupies no rows, and a single
// trailing newline is chrome rather than an extra row, so it is trimmed before
// splitting.
func SectionLines(section string) []string {
	section = strings.TrimRight(section, "\n")
	if section == "" {
		return nil
	}
	return strings.Split(section, "\n")
}

// ClampLine hard-clamps a single composed (styled) line to width so headers,
// hints, and query bars cannot overflow narrow terminals. A non-positive width
// (pre-resize) is a no-op.
func ClampLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	return StyledTruncate(line, width, "…")
}

// QueryLine is prompt followed by a query being typed or shown, fit to width
// by dropping the start of the query rather than its end: the newest
// characters, and the cursor after them, are what the operator is looking at.
// A non-positive width (pre-resize) is a no-op.
func QueryLine(prompt, query string, width int) string {
	if width <= 0 {
		return prompt + query
	}
	return ClampLine(prompt+TruncateLeft(query, width-StyledWidth(prompt), "…"), width)
}

// ListWindow is the slice of a flat list that fits a body of a given height,
// plus whether scroll indicators are needed above and below it. The indicators
// consume a row each, which is why the window is narrower than the raw height
// whenever the list overflows.
type ListWindow struct {
	Start      int
	End        int
	ShowTop    bool
	ShowBottom bool
}

// MaxListOffset is the furthest a list of total rows can usefully be scrolled
// inside a body height rows tall: the offset whose window ends on the last row.
// Scrolled any further, the window would leave rows hidden above it while the
// body had room for them below. A list that fits has nowhere to scroll, and
// once scrolled the window spends a row on its ▲ marker, which is why the
// answer is one past total-height; a one-row body draws no marker. A
// non-positive height is unbounded.
func MaxListOffset(total, height int) int {
	if total <= 0 || height <= 0 || total <= height {
		return 0
	}
	if height == 1 {
		return total - 1
	}
	return total - height + 1
}

// ListWindowFor computes the visible ListWindow for a flat list of total rows
// scrolled to offset inside a body height rows tall. A non-positive height
// means "unbounded" and suppresses the scroll indicators. An offset past
// MaxListOffset is drawn from MaxListOffset, so a list that shrank under its
// offset (a filter, a refresh) fills the body instead of hiding rows above.
func ListWindowFor(total, offset, height int) ListWindow {
	window := ListWindow{Start: 0, End: total}
	if total <= 0 {
		window.End = 0
		return window
	}
	if height <= 0 {
		return window
	}

	if offset < 0 {
		offset = 0
	}
	if maxOffset := MaxListOffset(total, height); offset > maxOffset {
		offset = maxOffset
	}
	window.Start = offset

	if height == 1 {
		window.End = window.Start + 1
		if window.End > total {
			window.End = total
		}
		return window
	}

	budget := height
	if window.Start > 0 {
		window.ShowTop = true
		budget--
	}
	if budget < 0 {
		budget = 0
	}
	window.End = window.Start + budget
	if window.End > total {
		window.End = total
	}
	if window.End < total {
		window.ShowBottom = true
		if budget > 1 {
			budget--
		} else {
			window.ShowBottom = false
		}
		window.End = window.Start + budget
		if window.End > total {
			window.End = total
		}
	}
	return window
}

// FallbackWidth is the column budget views wrap to before the terminal has
// reported its size. Bubble Tea delivers the first WindowSizeMsg after the
// initial render, so every body renderer needs a width that produces readable
// output rather than a zero-width collapse.
const FallbackWidth = 120

// WrapWidth returns the column budget a body should wrap to, substituting
// FallbackWidth when the terminal width is not yet known.
func WrapWidth(width int) int {
	if width <= 0 {
		return FallbackWidth
	}
	return width
}
