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
// construction. A chip that wrapped, a banner that appeared, or an inspector
// that was responsively suppressed had to be taught to all three.

package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
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

// renderDashboardFrame emits the canonical ModeList body: header, chips,
// spacer, then the list (with the inspector pane when the geometry admits it).
// The deprecation banner and the icon legend that used to sit between the
// header and the chips are gone — the banner to the command palette, the legend
// to the help overlay — which is what makes the block above the list a fixed
// two or three rows rather than a reflowing one.
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

	chips := dashboard.RenderChipsWithHits(m.activeFilter, dashboard.ProfileColorMap(m.cfg), m.width)
	sb.WriteString(chips.Text)
	for _, chip := range chips.Hits {
		hitMap.clicks = append(hitMap.clicks, mouseHitRegion{
			kind: mouseHitChip, rect: translateHitRect(chip.Rect, 0, y),
			filterRaw: m.activeFilter.Raw, chipIndex: chip.Index, chipLabel: chip.Label,
		})
	}
	y += renderedRows(chips.Text)

	spacer := renderHeaderSpacer(m)
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
	if panes.hitMap.hasInspector {
		hitMap.inspectorPane = translateHitRect(panes.hitMap.inspectorPane, 0, y)
		hitMap.hasInspector = true
	}

	return dashboardFrame{text: sb.String(), hitMap: hitMap}
}

// renderDashboard is the text half of the canonical frame. Overlay backdrops
// and the plain string render read it, so a backdrop is the dashboard rather
// than a second drawing that resembles it.
func renderDashboard(m Model) string {
	return renderDashboardFrame(m).text
}

// renderSplitFrame lays out the body row: the session list and the inspector
// pane, joined according to the resolved geometry and falling back to the plain
// list when the split does not fit. Hit regions are translated into the joined
// result here, where the column offsets are known, rather than recovered from
// it.
//
// The full-screen inspector returns before a single list region is produced.
// That is the mouse contract, not an optimization: no queue column is on screen,
// so nothing on it may be clickable, and a click that landed on the inspector
// must not select the row that would have been under it.
func renderSplitFrame(m Model) dashboardFrame {
	g := dashboardGeometry(m)
	if g.fullScreen {
		return dashboardFrame{
			text: renderPreviewPane(m, g.previewWidth, g.previewHeight) + "\n",
			hitMap: mouseHitMap{
				inspectorPane: dashboard.HitRect{Width: g.previewWidth, Height: g.previewHeight},
				hasInspector:  true,
			},
		}
	}
	list := dashboard.RenderListWithHits(dashboardListInput(m, g.listWidth, g.listHeight))
	result := dashboardFrame{text: list.Text}
	blocks := []paneBlock{{text: list.Text, width: g.listWidth}}
	// The queue is the leftmost pane, and nothing is drawn to its left any more.
	// The offset stays spelled out because every list rectangle below is stated
	// against it, and the joined result is where those rectangles are measured.
	const listX = 0

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
		return result
	}
	inspectorX := listX + g.listWidth + 1
	blocks = append(blocks, paneDivider(g.listHeight),
		paneBlock{text: renderPreviewPane(m, g.previewWidth, g.previewHeight), width: g.previewWidth})
	result.text = joinPanes(g.listHeight, blocks...) + "\n"
	result.hitMap.inspectorPane = dashboard.HitRect{X: inspectorX, Width: g.previewWidth, Height: g.previewHeight}
	result.hitMap.hasInspector = true
	return result
}

// paneBlock is a rendered pane and the column width the geometry allotted it.
// The width travels with the text because it is what the block was laid out to
// occupy, not what its content happened to fill: a run of short queue rows ends
// the block early, and the hit map published in the same pass still says the
// queue is g.listWidth wide and the inspector starts one column after it.
type paneBlock struct {
	text  string
	width int
}

// paneDivider is the one-column rule drawn between two panes, full body height.
func paneDivider(height int) paneBlock {
	if height <= 0 {
		return paneBlock{width: 1}
	}
	return paneBlock{text: strings.TrimRight(strings.Repeat(styleDim.Render("│")+"\n", height), "\n"), width: 1}
}

// joinPanes stacks pane blocks side by side at the widths they were allotted,
// padding each to its width and to the full body height as it goes.
//
// It is a hand-rolled join rather than lipgloss.JoinHorizontal because the
// column a pane starts at is a claim the hit map makes, so exactly one ruler may
// decide it. lipgloss measures with go-runewidth and layout measures pictographic
// clusters as two cells, and the two disagree on glyphs whose East Asian width is
// neutral — 🗣 among them. Padding with one ruler and joining with the other put
// a session titled 🗣commscoach one column right of every other row, which widened
// the whole frame past the terminal.
//
// A short block is padded to height because the divider beside it is full
// height; without that the rows below the queue's last entry would close early
// and the rule would have nothing to sit beside.
func joinPanes(height int, blocks ...paneBlock) string {
	if height <= 0 {
		return ""
	}
	split := make([][]string, len(blocks))
	for i, block := range blocks {
		split[i] = strings.Split(block.text, "\n")
	}
	rows := make([]string, height)
	var sb strings.Builder
	for y := range rows {
		sb.Reset()
		for i, lines := range split {
			line := ""
			if y < len(lines) {
				line = lines[y]
			}
			sb.WriteString(line)
			if gap := blocks[i].width - layout.StyledWidth(line); gap > 0 {
				sb.WriteString(strings.Repeat(" ", gap))
			}
		}
		rows[y] = sb.String()
	}
	return strings.Join(rows, "\n")
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
// There is no term below the list any more. The detail strip that used to hold
// those two rows was the compact band's compensation for a session row that
// could not carry its own metadata; the inspector says all of it, at every
// width, beside the row rather than beneath the list — so the strip had become
// two rows spent restating what the pane next to it already said.
type dashboardChrome struct {
	// above is the header, the chips when a filter is active, and the spacer.
	above int
	// footer is the reflowed footer's row count. The reserved feedback row
	// above it is statusBarLines, which total() charges separately.
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
	return c.above + dashboardPaddingLines + c.footer + statusBarLines
}

// paneRows is the complement: the body rows left for the list and the inspector
// at termHeight. The result may be zero or negative on a degenerate terminal;
// flooring it to something usable is the caller's judgement, because the list
// and the split floor it differently.
func (c dashboardChrome) paneRows(termHeight int) int {
	return termHeight - c.total()
}
