package layout

// Line-splicing overlay composition. Ported from the tt project's
// internal/tui/prefix_popup_overlay.go (overlayPopup + overlayLineAt). The
// approach pads each base line out to the full width, uses ansi.Cut to slice
// around the popup region so SGR state is preserved across cuts, and rejoins
// the pieces. The result is a centered modal that does not depend on the
// natural height of the base content.

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// dimStyle recedes the base view behind a popup. It is a private copy of the
// root TUI's dim style rather than a shared style dependency: compositing is
// the only thing this package knows how to do, and one faint attribute is
// cheaper to duplicate than a theme edge into a layout leaf.
var dimStyle = lipgloss.NewStyle().Faint(true)

// ComposeOverlay splices popup lines into base content at (startCol, startRow).
// base and popup are pre-rendered strings; baseWidth/baseHeight describe the
// area available for compositing. When dim is true the base is dimmed first so
// it visually recedes behind the popup.
//
// The popup is right-clipped at baseWidth when its width plus startCol would
// exceed the base area. Rows beyond baseHeight are dropped. An empty popup
// returns the normalized, possibly dimmed base.
func ComposeOverlay(base, popup string, baseWidth, baseHeight, startCol, startRow int, dim bool) string {
	if dim {
		base = dimContent(base)
	}

	baseLines := SectionLines(base)
	if baseHeight < 0 {
		baseHeight = 0
	}
	if len(baseLines) > baseHeight {
		baseLines = baseLines[:baseHeight]
	}
	for len(baseLines) < baseHeight {
		baseLines = append(baseLines, "")
	}
	if popup == "" {
		return strings.Join(baseLines, "\n")
	}

	popupLines := strings.Split(popup, "\n")
	maxPopupWidth := PopupWidth(popup)

	for i, popupLine := range popupLines {
		row := startRow + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		lineWidth := StyledWidth(popupLine)
		if lineWidth < maxPopupWidth {
			popupLine += strings.Repeat(" ", maxPopupWidth-lineWidth)
		}
		baseLines[row] = overlayLineAt(baseLines[row], popupLine, startCol, baseWidth)
	}

	return strings.Join(baseLines, "\n")
}

// CenterOverlay computes (startCol, startRow) so the popup is centered in the
// (baseWidth, baseHeight) area, then delegates to ComposeOverlay.
func CenterOverlay(base, popup string, baseWidth, baseHeight int, dim bool) string {
	popupW := PopupWidth(popup)
	popupH := 0
	if popup != "" {
		popupH = strings.Count(popup, "\n") + 1
	}
	startCol := max((baseWidth-popupW)/2, 0)
	startRow := max((baseHeight-popupH)/2, 0)
	return ComposeOverlay(base, popup, baseWidth, baseHeight, startCol, startRow, dim)
}

// TopCenterOverlay horizontally centers the popup but anchors it near the top
// of the base area (one row from the top edge). Use this when the popup's
// height can change at runtime and the user shouldn't see the modal jump
// vertically as content is added or removed.
func TopCenterOverlay(base, popup string, baseWidth, baseHeight int, dim bool) string {
	popupW := PopupWidth(popup)
	popupH := 0
	if popup != "" {
		popupH = strings.Count(popup, "\n") + 1
	}
	startCol := max((baseWidth-popupW)/2, 0)
	startRow := 1
	if baseHeight <= 1 || popupH >= baseHeight {
		startRow = 0
	}
	return ComposeOverlay(base, popup, baseWidth, baseHeight, startCol, startRow, dim)
}

// PopupWidth returns the widest rendered line in popup, accounting for ANSI
// escape sequences via StyledWidth.
func PopupWidth(popup string) int {
	width := 0
	for _, line := range strings.Split(popup, "\n") {
		if w := StyledWidth(line); w > width {
			width = w
		}
	}
	return width
}

// dimContent applies the dim style to every line of content so it visually
// recedes behind an overlay popup. ANSI sequences on the original content are
// preserved because lipgloss layers the faint attribute over them rather than
// stripping.
func dimContent(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = dimStyle.Render(line)
	}
	return strings.Join(lines, "\n")
}

// overlayLineAt places overlayStr on top of baseLine starting at column
// startCol within an area totalWidth columns wide. baseLine is right-padded to
// totalWidth when shorter; the popup region is sliced out with ansi.Cut so any
// existing ANSI escapes outside the overlay window survive.
func overlayLineAt(baseLine, overlayStr string, startCol, totalWidth int) string {
	baseW := StyledWidth(baseLine)
	if baseW < totalWidth {
		baseLine += strings.Repeat(" ", totalWidth-baseW)
	}

	overlayWidth := StyledWidth(overlayStr)

	left := ""
	if startCol > 0 {
		left = ansi.Cut(baseLine, 0, startCol)
		leftWidth := StyledWidth(left)
		if leftWidth < startCol {
			left += strings.Repeat(" ", startCol-leftWidth)
		}
	}

	rightStart := startCol + overlayWidth
	right := ""
	if rightStart < totalWidth {
		right = ansi.Cut(baseLine, rightStart, totalWidth)
	}

	// Clip overlay if it exceeds the available area.
	if startCol+overlayWidth > totalWidth {
		avail := totalWidth - startCol
		if avail <= 0 {
			return left + right
		}
		overlayStr = ansi.Cut(overlayStr, 0, avail)
	}

	return left + overlayStr + right
}
