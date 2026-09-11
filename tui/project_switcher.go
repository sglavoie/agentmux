package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// projectSwitcherBoxWidth is the width styleOverlayBox is rendered at: the
// content plus its two padding columns, with the border outside that. Fifty
// columns is the chip picker's figure and the switcher matches it so the two
// overlays read as one family; a terminal too narrow for it gets whatever is
// left after the border.
func projectSwitcherBoxWidth(m Model) int {
	width := 50
	if avail := m.width - 4; avail < width {
		width = avail
	}
	if width < 1 {
		width = 1
	}
	return width
}

// projectSwitcherContentWidth is the width the leaf lays a scope row out at.
//
// It subtracts the box's own frame — the border column and the padding column on
// each side — from the width the box is rendered at, because styleOverlayBox
// re-wraps anything wider than that. A row laid out at the box width instead
// would fold every scope onto a second line, which is exactly what "the switcher
// fits in the body" stops being true about.
func projectSwitcherContentWidth(m Model) int {
	width := projectSwitcherBoxWidth(m) - styleOverlayBox.GetHorizontalFrameSize()
	if width < 1 {
		width = 1
	}
	return width
}

// projectSwitcherVisibleHeight is the rows the scope list may spend, including
// its ▲/▼ markers.
//
// The chrome it subtracts is the same set the chip picker charges — two border
// rows, the heading, and a two-row safety margin — plus the search row once the
// operator has started one. The cap keeps the dimmed dashboard visible around
// the overlay on a tall terminal, which is what makes the switcher read as a
// detour from the list rather than as a screen of its own.
func projectSwitcherVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	// The border rows the box will add, the heading, and a one-row margin so the
	// overlay reads as a detour rather than a full screen — plus the search row
	// once the operator has started one.
	chrome := styleOverlayBox.GetVerticalFrameSize() + 2
	if projectSwitcherSearchVisible(m) {
		chrome++
	}
	avail := rootBodyHeight(m) - chrome
	if avail < 1 {
		return 1
	}
	if avail > 12 {
		return 12
	}
	return avail
}

func projectSwitcherSearchVisible(m Model) bool {
	return m.projectSwitcherSearch || m.projectSwitcherQuery != ""
}

// projectSwitcherInput projects the model onto the leaf's rendering input.
// Every field is re-derived here rather than read from stored state, which is
// what lets a refresh that landed under the open overlay show up in the tallies
// on the very next frame.
func projectSwitcherInput(m Model) dashboard.ProjectSwitcherInput {
	return dashboard.ProjectSwitcherInput{
		Scopes:    projectSwitcherScopes(m),
		Cursor:    projectSwitcherCursor(m),
		Offset:    m.projectSwitcherOffset,
		Width:     projectSwitcherContentWidth(m),
		Height:    projectSwitcherVisibleHeight(m),
		Query:     m.projectSwitcherQuery,
		Searching: m.projectSwitcherSearch,
		Applied:   m.activeFilter.Project,
	}
}

// renderProjectSwitcherBox wraps the leaf's content in styleOverlayBox so the
// switcher can be composited over the dimmed dashboard.
func renderProjectSwitcherBox(m Model) string {
	content := dashboard.RenderProjectSwitcher(projectSwitcherInput(m)).Text
	return styleOverlayBox.Width(projectSwitcherBoxWidth(m)).Render(strings.TrimRight(content, "\n"))
}

// projectSwitcherBoxInset is where the leaf's content begins inside the rendered
// box: one column and one row of border, plus the one padding column
// styleOverlayBox adds on the left. There is no vertical padding.
const (
	projectSwitcherBoxInsetX = 2
	projectSwitcherBoxInsetY = 1
)

// renderProjectSwitcherFrame composes the switcher over the dimmed dashboard and
// publishes the scope rows as click targets in screen coordinates.
//
// The popup's placement is computed through layout.CenterOverlayOrigin rather
// than restated here, and the composition is the same CenterOverlay call every
// other centered modal makes. That is the whole reason the switcher can have
// mouse targets at all: the rectangle a row is drawn in and the rectangle a click
// is tested against are two readings of one placement, not two placements that
// happen to agree.
func renderProjectSwitcherFrame(m Model, chrome rootChrome) (string, mouseHitMap) {
	base := renderDashboard(m)
	popup := renderProjectSwitcherBox(m)
	bodyHeight := chrome.bodyHeight()
	text := layout.CenterOverlay(base, popup, m.width, bodyHeight, true)

	originX, originY := layout.CenterOverlayOrigin(popup, m.width, bodyHeight)
	originX += projectSwitcherBoxInsetX
	originY += projectSwitcherBoxInsetY

	var hitMap mouseHitMap
	for _, hit := range dashboard.RenderProjectSwitcher(projectSwitcherInput(m)).Hits {
		hitMap.clicks = append(hitMap.clicks, mouseHitRegion{
			kind:    mouseHitProjectScope,
			rect:    translateHitRect(hit.Rect, originX, originY),
			project: hit.Project,
			label:   hit.Label,
		})
	}
	return text, hitMap
}
