package history

import (
	"github.com/sglavoie/agentmux/tui/layout"
)

// detailMinVisible is the floor on the visible body height. Even on very
// short terminals we keep at least this many event rows visible so the pane
// remains useful.
const detailMinVisible = 3

// detailInnerChrome counts the lines Render reserves inside the overlay box
// outside the scrollable event body: header line 1 (1) + header line 2 (1) +
// blank after header (1) + scroll-up indicator slot (1) + scroll-down
// indicator slot (1) + pane footer (1) = 6. The two indicator slots are
// reserved unconditionally so the layout does not jitter as the user scrolls.
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

// ClampScroll returns in.Scroll pinned to the [0, maxOffset] range at the
// input's current dimensions. The root calls this from the WindowSizeMsg
// handler so a resize that shrinks the viewport doesn't leave a stale offset
// above maxOffset — without the clamp the next j/k keypress would have to chew
// through the dead rows before the visible window reacts.
func (in DetailInput) ClampScroll() int {
	total, visible := in.ScrollBounds(in.VisibleHeight())
	maxOffset := total - visible
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

// overlayInnerHeight returns the inner content height of the detail overlay
// box (everything inside the rounded border). The box uses Padding(0, 1) so
// vertical padding is zero; the only chrome it owns is the 2 border rows. The
// remaining 4-row margin keeps the modal floating rather than consuming the
// full terminal even on short screens. A 30-row cap prevents very tall
// terminals from rendering an oversized modal.
func (in DetailInput) overlayInnerHeight() int {
	if in.Height <= 0 {
		return 0
	}
	// Inside the box we render the chrome counted by detailInnerChrome
	// (header 2 + blank + ▲ slot + ▼ slot + footer = 6) on top of at
	// least detailMinVisible body rows when possible. On shorter terminals,
	// shrink the body rows instead of rendering past the root body area.
	minInner := detailMinVisible + detailInnerChrome
	fit := in.BodyHeight - styleOverlayBox.GetVerticalFrameSize()
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

// VisibleHeight returns the number of scrollable event rows that fit inside
// the detail overlay's inner area (overlayInnerHeight minus the
// detailInnerChrome lines reserved by Render).
func (in DetailInput) VisibleHeight() int {
	inner := in.overlayInnerHeight()
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

// ScrollBounds returns the total number of events and the visible page size at
// the supplied content height. When the input has no height yet
// (pre-WindowSizeMsg) the visible window equals the total so scroll keys
// become no-ops naturally — mirroring helpScrollBounds.
func (in DetailInput) ScrollBounds(visibleHeight int) (total, visible int) {
	total = len(in.Events)
	if in.ViewMetadata {
		total = len(in.metadataLines())
	}
	if in.Height == 0 {
		return total, total
	}
	if visibleHeight < 0 {
		visibleHeight = 0
	}
	visible = min(total, visibleHeight)
	return total, visible
}

// boxInnerWidth returns the styleOverlayBox `.Width(...)` value used for the
// detail overlay. The cap (96 cols) keeps the modal floating on very wide
// terminals; the Width-4 clamp prevents the box from spilling off-screen on
// narrow ones.
func (in DetailInput) boxInnerWidth() int {
	innerWidth := 96
	if avail := in.Width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return innerWidth
}

// detailRowWidth returns the cell width an event row should occupy so it fits
// inside the overlay box's content area without wrapping. The 2-col
// subtraction matches styleOverlayBox's Padding(0, 1) — the border is already
// excluded from `.Width(...)` semantics in lipgloss.
func (in DetailInput) detailRowWidth() int {
	w := in.boxInnerWidth() - 2
	if w < 1 {
		w = 1
	}
	return w
}

// padRight pads s to exactly width visual cells, truncating with `…` if it
// is too long. Used for fixed-width detail-row columns where downstream
// layout depends on stable widths.
func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return layout.PadRight(layout.Truncate(s, width, "…"), width)
}
