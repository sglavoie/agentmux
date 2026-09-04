package dashboard

import (
	"time"
)

// ScrollInput is the subset of ListInput that the scroll math reads. The line
// cost of a windowed walk depends only on the projected items, the facts that
// decide each item's urgency group, and whether the renderer is drawing group
// chrome — not on the terminal size or on anything else the Model carries.
//
// The root owns the chrome budget (header, legend, chips, footer, preview
// pane) that turns a terminal height into a row budget; this type answers the
// question the root cannot: how many lines the items themselves cost.
type ScrollInput struct {
	Items         ItemList
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	// GroupHeadings mirrors ShowsGroupHeadings for the view the renderer is
	// drawing under. It is passed rather than derived so a caller that already
	// computed it for the renderer charges the same chrome the renderer will
	// actually draw — and so the Projects view, which draws no headings, does not
	// have to be named again here.
	GroupHeadings bool
}

// LineCost returns the number of rendered lines from offset through cursor
// inclusive, matching the RenderList walk. Every item costs one line whatever
// its kind and depth: a family summary is one line, an indented child is one
// line, and a collapsed family's children are absent from the projection
// entirely and so cost nothing.
//
// When GroupHeadings is true a group-label line precedes the first item and
// every subsequent item whose group differs from the previous one; subsequent
// visible group labels also get a blank separator line. The walk initializes
// prevGroup to -1 to match RenderList, so the item at offset always emits a
// label when GroupHeadings is true.
func (in ScrollInput) LineCost(offset, cursor int) int {
	if offset > cursor || offset < 0 || cursor >= in.Items.Len() {
		return 0
	}
	cost := 0
	prevGroup := -1
	for i := offset; i <= cursor; i++ {
		if in.GroupHeadings {
			g := in.Items.GroupAt(i, in.LastActivity, in.IdleThreshold)
			if g != prevGroup {
				if prevGroup != -1 {
					cost++ // blank separator before a subsequent group
				}
				cost++ // group label
				prevGroup = g
			}
		}
		cost++ // item line
	}
	return cost
}

// VisibleRange returns the half-open item window [start, end) that RenderList
// emits from the given offset inside a pane of the given height. It charges the
// same group chrome LineCost does and reserves the same ▲/▼ indicator lines the
// renderer spends, so a caller asking "which items are on screen" gets the
// renderer's own answer rather than a second estimate of it.
//
// This is the question a line budget cannot answer on its own: a height of 12
// shows twelve items in the Projects view and as few as four in a multi-group
// urgency view, because there the headings and their separators are spent out of
// the same budget. Callers that treat the height as an item count report a
// window the renderer never drew.
//
// A non-positive height means "unbounded", matching RenderList: every item is
// displayed and neither indicator is drawn.
func (in ScrollInput) VisibleRange(offset, height int) (start, end int) {
	total := in.Items.Len()
	if total <= 0 {
		return 0, 0
	}
	if height <= 0 {
		return 0, total
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	start = offset

	budget := height
	if start > 0 {
		budget-- // ▲
	}
	if budget < 0 {
		budget = 0
	}

	// Two-pass fit, mirroring RenderList: fit once without reserving the bottom
	// indicator, and if items still remain below the window, re-fit with one
	// fewer line so the ▼ slot is paid for.
	end = in.FitRows(start, budget)
	if end < total {
		if budget > 0 {
			budget--
		}
		end = in.FitRows(start, budget)
	}
	return start, end
}

// FitRows returns the exclusive end index such that rendering items
// [startIdx, end) consumes at most budget lines. The walk mirrors RenderList:
// when GroupHeadings is true, prevGroup initializes to -1 so the first item at
// startIdx always charges a group-label line, matching the renderer's "begin a
// window inside group 1 still emits the label" behavior. Subsequent visible
// group transitions also charge one blank separator line before the next group
// label.
//
// Every item costs exactly one line regardless of kind and depth: a summary is
// one line, an indented child is one line, and a collapsed family's children
// are not items at all and so cost nothing. That is the whole reason the
// projection folds before the walk runs rather than during it.
func (in ScrollInput) FitRows(startIdx, budget int) int {
	total := in.Items.Len()
	if budget <= 0 || startIdx >= total {
		return startIdx
	}
	used := 0
	prevGroup := -1
	end := startIdx
	for i := startIdx; i < total; i++ {
		cost := 1
		var g int
		if in.GroupHeadings {
			g = in.Items.GroupAt(i, in.LastActivity, in.IdleThreshold)
			if g != prevGroup {
				cost++
				if prevGroup != -1 {
					cost++
				}
			}
		}
		if used+cost > budget {
			break
		}
		used += cost
		if in.GroupHeadings {
			prevGroup = g
		}
		end = i + 1
	}
	return end
}

// CursorFits reports whether RenderList can emit the cursor item from the given
// offset inside a pane of the given height. The pane height is not all row
// budget: RenderList spends one line on ▲ when offset > 0 and one line on ▼
// when items remain below the cursor. Those indicator lines must be reserved
// during scroll calculations, otherwise the caller can select an item that the
// renderer drops while making space for the scroll markers.
func (in ScrollInput) CursorFits(offset, cursor, height int) bool {
	if offset < 0 || offset > cursor || cursor >= in.Items.Len() {
		return false
	}
	budget := height
	if offset > 0 {
		budget--
	}
	if cursor < in.Items.Len()-1 {
		budget--
	}
	if budget < 0 {
		return false
	}
	return in.LineCost(offset, cursor) <= budget
}
