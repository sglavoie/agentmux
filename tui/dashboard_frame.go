// This file is the dashboard's canonical frame: the single pass that turns the
// model into the dashboard body's text and the mouse regions produced beside
// it, plus the single statement of the vertical budget that body is laid out
// inside.
//
// Everything that draws the dashboard goes through here — the live View, the
// string render the scenario harness captures, and the dimmed backdrop an
// overlay is composited over. That is the point of the file. Those three used
// to be three separate compositions of the same seven children, and only one of
// them carried hit regions, so the frame an operator clicked on and the frame a
// committed scenario asserted were related by convention rather than by
// construction. A chip that wrapped, a banner that appeared, or a rail that was
// responsively suppressed had to be taught to all three.

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// dashboardFrame is the dashboard body: its text and the hit regions the very
// pass that emitted that text produced. The two travel together because a
// region reconstructed afterwards from a finished ANSI string is a second
// opinion about where a row is, and the two opinions drift at exactly the
// widths — wide glyphs, wrapped chips, group headings, joined panes — where
// being wrong is what the operator notices.
type dashboardFrame struct {
	text   string
	hitMap mouseHitMap
}

// renderDashboardFrame emits the canonical ModeList body: header, deprecation
// banner, legend, chips, spacer, then the list (with the project rail and the
// preview pane when the geometry admits them), then the compact detail strip.
func renderDashboardFrame(m Model) dashboardFrame {
	var sb strings.Builder
	var hitMap mouseHitMap
	y := 0

	header := renderHeaderWithHits(m)
	sb.WriteString(header.text)
	for _, hit := range header.hits {
		hit.rect = translateHitRect(hit.rect, 0, y)
		hitMap.clicks = append(hitMap.clicks, hit)
	}
	y += renderedRows(header.text)

	for _, block := range []string{renderDeprecationBanner(m), renderLegend(m)} {
		sb.WriteString(block)
		y += renderedRows(block)
	}

	chips := dashboard.RenderChipsWithHits(m.activeFilter, dashboard.ProfileColorMap(m.cfg), m.focusedChipIdx, m.width)
	sb.WriteString(chips.Text)
	for _, chip := range chips.Hits {
		hitMap.clicks = append(hitMap.clicks, mouseHitRegion{
			kind: mouseHitChip, rect: translateHitRect(chip.Rect, 0, y),
			filterRaw: m.activeFilter.Raw, chipIndex: chip.Index, chipLabel: chip.Label,
		})
	}
	y += renderedRows(chips.Text)

	spacer := renderInlineFilter(m)
	sb.WriteString(spacer)
	y += renderedRows(spacer)

	panes := renderSplitFrame(m)
	sb.WriteString(panes.text)
	for _, hit := range panes.hitMap.clicks {
		hit.rect = translateHitRect(hit.rect, 0, y)
		hitMap.clicks = append(hitMap.clicks, hit)
	}
	if panes.hitMap.hasList {
		hitMap.listPane = translateHitRect(panes.hitMap.listPane, 0, y)
		hitMap.hasList = true
	}
	if panes.hitMap.hasRail {
		hitMap.railPane = translateHitRect(panes.hitMap.railPane, 0, y)
		hitMap.hasRail = true
	}

	sb.WriteString(renderDetailStripBlock(m))
	return dashboardFrame{text: sb.String(), hitMap: hitMap}
}

// renderDashboard is the text half of the canonical frame. Overlay backdrops
// and the plain string render read it, so a backdrop is the dashboard rather
// than a second drawing that resembles it.
func renderDashboard(m Model) string {
	return renderDashboardFrame(m).text
}

// renderSplitFrame lays out the body row: the project rail, the session list,
// and the preview pane, joined according to the resolved geometry and falling
// back to the plain list when the split does not fit. Hit regions are
// translated into the joined result here, where the column offsets are known,
// rather than recovered from it.
func renderSplitFrame(m Model) dashboardFrame {
	g := dashboardGeometry(m)
	list := dashboard.RenderListWithHits(dashboardListInput(m, g.listWidth, g.listHeight))
	result := dashboardFrame{text: list.Text}
	listX := 0

	var rail dashboard.RailRender
	if g.rail {
		rail = dashboard.RenderRailWithHits(dashboard.RailInput{
			Rows: dashboardRailRows(m), Cursor: m.railCursor, Offset: m.railOffset,
			Width: g.railWidth, Height: g.railHeight, AppliedProject: m.activeFilter.Project, Focused: m.railFocused,
		})
		divider := strings.TrimRight(strings.Repeat(styleDim.Render("│")+"\n", g.listHeight), "\n")
		result.text = lipgloss.JoinHorizontal(lipgloss.Top, rail.Text, divider, list.Text)
		listX = g.railWidth + 1
		result.hitMap.railPane = dashboard.HitRect{Width: g.railWidth, Height: g.railHeight}
		result.hitMap.hasRail = true
		for _, hit := range rail.Hits {
			result.hitMap.clicks = append(result.hitMap.clicks, mouseHitRegion{
				kind: mouseHitRailRow, rect: hit.Rect, project: hit.Project, railLabel: hit.Label,
			})
		}
	}

	for _, hit := range list.Hits {
		row := translateHitRect(hit.Rect, listX, 0)
		if hit.HasChevron {
			result.hitMap.clicks = append(result.hitMap.clicks, mouseHitRegion{
				kind:            mouseHitFamilyChevron,
				rect:            dashboard.HitRect{X: listX + hit.ChevronX, Y: hit.Rect.Y, Width: 1, Height: 1},
				identity:        hit.Identity,
				familyCollapsed: hit.Collapsed,
			})
			result.hitMap.clicks = append(result.hitMap.clicks, mouseHitRegion{kind: mouseHitFamily, rect: row, identity: hit.Identity})
		} else {
			result.hitMap.clicks = append(result.hitMap.clicks, mouseHitRegion{kind: mouseHitSession, rect: row, identity: hit.Identity})
		}
	}
	result.hitMap.listPane = dashboard.HitRect{X: listX, Width: g.listWidth, Height: g.listHeight}
	result.hitMap.hasList = true

	if !g.split {
		if g.rail {
			result.text += "\n"
		}
		return result
	}
	if g.vertical {
		divider := styleDim.Render(strings.Repeat("─", m.width))
		result.text = lipgloss.JoinVertical(lipgloss.Left, result.text, divider,
			renderPreviewPane(m, g.previewWidth, g.previewHeight)) + "\n"
		return result
	}
	divider := strings.TrimRight(strings.Repeat(styleDim.Render("│")+"\n", g.listHeight), "\n")
	result.text = lipgloss.JoinHorizontal(lipgloss.Top, result.text, divider,
		renderPreviewPane(m, g.previewWidth, g.previewHeight)) + "\n"
	return result
}

// renderedRows counts the physical rows a rendered block occupies. A block that
// ends without a trailing newline still owns the row it wrote, which is why
// this is not a plain newline count.
func renderedRows(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// dashboardPaddingLines is the blank row the root composition leaves between
// the bottom of the dashboard body and the footer. It is a term in the budget
// rather than a bare 1 in three subtractions because it is the one term with no
// renderer to measure it against.
const dashboardPaddingLines = 1

// dashboardChrome is the dashboard's vertical budget: the rows spent above the
// list, below it, and at the bottom of the screen. There is one statement of
// that budget and every consumer reads it — the geometry the panes are laid out
// from, the chrome total the scroll arithmetic charges, and the footer
// fixpoint's measurement of a candidate footer. They used to be three
// independent spellings of one subtraction, which is a shape that stays correct
// only for as long as nobody adds a term to it.
type dashboardChrome struct {
	// above is header, deprecation banner, legend, chips, and the spacer.
	above int
	// below is the compact band's selected-item detail strip, or nothing.
	below int
	// footer is the reflowed footer's row count.
	footer int
}

// dashboardChromeWithFooter states the budget against an explicit footer row
// count. This is the seam that keeps the geometry and the footer measurement
// from closing a loop: the ModeList footer carries a position indicator derived
// from the list height, and the list height is derived from the footer's rows,
// so the footer fixpoint measures each candidate against a budget it supplies
// the footer term for rather than against one that would ask
// footerBudgetLines for the number being computed.
func dashboardChromeWithFooter(m Model, footerLines int) dashboardChrome {
	return dashboardChrome{
		above:  chromeAboveListLines(m),
		below:  chromeBelowListLines(m),
		footer: footerLines,
	}
}

// dashboardChromeFor states the budget at the model's settled footer height.
// Callers that are not themselves computing the footer use this one.
func dashboardChromeFor(m Model) dashboardChrome {
	return dashboardChromeWithFooter(m, footerBudgetLines(m))
}

// total is every row the chrome spends around the list.
func (c dashboardChrome) total() int {
	return c.above + c.below + dashboardPaddingLines + c.footer + statusBarLines
}

// paneRows is the complement: the body rows left for the list and the preview
// at termHeight. The result may be zero or negative on a degenerate terminal;
// flooring it to something usable is the caller's judgement, because the list
// and the split floor it differently.
func (c dashboardChrome) paneRows(termHeight int) int {
	return termHeight - c.total()
}
