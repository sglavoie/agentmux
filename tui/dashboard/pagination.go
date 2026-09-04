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
