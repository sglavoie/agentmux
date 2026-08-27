package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// actionDescIndent is the column at which wrapped description text must
// resume so continuation lines align under the first description column.
// It matches the 22 visible columns produced by `%-2s  %-18s` for the
// key+label prefix plus the 2-space separator before the description.
const actionDescIndent = 24

// actionsScrollChrome counts the inner lines renderActionsWindow reserves
// outside the scrollable action rows: title, blank, top indicator slot, bottom
// indicator slot, and hint.
const actionsScrollChrome = 5

// renderActions renders the actions overlay for m.targetSession. Available
// actions render in default text; unavailable ones render dim. Descriptions
// are word-wrapped to descWidth so continuation lines indent under the
// description column instead of dangling at the left edge.
func renderActions(m Model, descWidth int) string {
	if descWidth < 1 {
		descWidth = 1
	}

	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Actions for "+displayRenderTitleIcon(runningSessionDisplayTitle(m.targetSession))) + "\n\n")
	for _, line := range actionBodyLines(m, descWidth) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	sb.WriteString(styleFooter.Render("press ") + renderFooterBindingWithLabel("a key", " ", "to run", styleFooter) +
		styleFooter.Render(" · ") + renderFooterBinding("esc/q", "to back out") + "\n")
	return sb.String()
}

// actionBodyLines renders only the action rows, including wrapped continuation
// rows. Header, scroll indicators, and footer hints are added by renderActions
// or renderActionsWindow.
func actionBodyLines(m Model, descWidth int) []string {
	if descWidth < 1 {
		descWidth = 1
	}
	indent := strings.Repeat(" ", actionDescIndent)
	var lines []string
	for _, a := range actionsFor(m) {
		avail := a.available(m)
		keyCol := fmt.Sprintf("%-2s  %-18s", a.keyDisplay, a.label)
		descLines := wrapText(a.description, descWidth)
		if len(descLines) == 0 {
			descLines = []string{""}
		}
		style := styleDim
		keyRendered := keyCol
		if !avail {
			style = styleFooterUnavailable
			keyRendered = styleFooterUnavailable.Render(keyCol)
		}
		lines = append(lines, keyRendered+"  "+style.Render(descLines[0]))
		for _, cont := range descLines[1:] {
			lines = append(lines, indent+style.Render(cont))
		}
	}
	return lines
}

// renderActionsWindow renders a height-adaptive actions overlay. When the full
// action list fits in the root body, it preserves renderActions' original
// output. On shorter terminals, it scrolls only the action rows while keeping
// the title and dispatch hint visible.
func renderActionsWindow(m Model, descWidth int) string {
	body := actionBodyLines(m, descWidth)
	visible := actionsVisibleHeight(m, len(body))
	if visible >= len(body) {
		return renderActions(m, descWidth)
	}

	offset := m.actionsOffset
	maxOffset := len(body) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}

	end := offset + visible
	if end > len(body) {
		end = len(body)
	}

	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Actions for "+displayRenderTitleIcon(runningSessionDisplayTitle(m.targetSession))) + "\n\n")
	if offset > 0 {
		sb.WriteString(styleDim.Render("  ▲"))
	}
	sb.WriteByte('\n')
	for _, line := range body[offset:end] {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if end < len(body) {
		sb.WriteString(styleDim.Render("  ▼"))
	}
	sb.WriteByte('\n')
	sb.WriteString(styleFooter.Render("press ") + renderFooterBindingWithLabel("a key", " ", "to run", styleFooter) +
		styleFooter.Render(" · ") + renderFooterBinding("j/k", "scroll") +
		styleFooter.Render(" · ") + renderFooterBinding("esc/q", "to back out") + "\n")
	return sb.String()
}

// actionsVisibleHeight returns how many rendered action-row lines can fit.
// Full-height terminals keep the original non-scrolling layout; short terminals
// spend the available inner height on a scroll window that still leaves the
// title and hint visible.
func actionsVisibleHeight(m Model, bodyLines int) int {
	if bodyLines <= 0 {
		return 0
	}
	innerFit := rootBodyHeight(m) - styleOverlayBox.GetVerticalFrameSize()
	if innerFit <= 0 {
		return 1
	}
	fullInner := bodyLines + 4 // title + blank + body + blank + hint
	if fullInner <= innerFit {
		return bodyLines
	}
	visible := innerFit - actionsScrollChrome
	if visible < 1 {
		visible = 1
	}
	if visible > bodyLines {
		visible = bodyLines
	}
	return visible
}

func actionsScrollBounds(m Model) (total, visible int) {
	descWidth := actionsBoxDescWidth(m)
	body := actionBodyLines(m, descWidth)
	total = len(body)
	visible = actionsVisibleHeight(m, total)
	if visible > total {
		visible = total
	}
	return total, visible
}

func clampActionsOffset(m *Model) {
	if m.mode != ModeActions {
		m.actionsOffset = 0
		return
	}
	total, visible := actionsScrollBounds(*m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.actionsOffset > maxOffset {
		m.actionsOffset = maxOffset
	}
	if m.actionsOffset < 0 {
		m.actionsOffset = 0
	}
}

// wrapText greedy-wraps s into lines no wider than width. Tokens longer
// than width are hard-broken so the caller can guarantee every emitted
// line fits the available column budget.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" {
		return nil
	}
	var lines []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		lines = append(lines, cur.String())
		cur.Reset()
	}
	for _, word := range strings.Fields(s) {
		for len(word) > width {
			flush()
			// Back the cut off to a rune boundary so a multi-byte word is
			// never sliced mid-rune; a single rune wider than the byte
			// budget is emitted whole rather than garbled.
			cut := width
			for cut > 0 && !utf8.RuneStart(word[cut]) {
				cut--
			}
			if cut == 0 {
				_, cut = utf8.DecodeRuneInString(word)
			}
			lines = append(lines, word[:cut])
			word = word[cut:]
		}
		if cur.Len() == 0 {
			cur.WriteString(word)
			continue
		}
		if cur.Len()+1+len(word) > width {
			flush()
			cur.WriteString(word)
			continue
		}
		cur.WriteByte(' ')
		cur.WriteString(word)
	}
	flush()
	return lines
}

// renderActionsBox wraps renderActions in styleOverlayBox so the actions
// overlay can be composited as a centered modal on top of the dimmed
// dashboard. The box grows to fit the widest unwrapped action row and
// clamps to m.width-4 on narrow terminals to leave a 2-col margin.
func renderActionsBox(m Model) string {
	boxWidth := actionsBoxWidth(m)
	descWidth := boxWidth - styleOverlayBox.GetHorizontalFrameSize() - actionDescIndent
	if descWidth < 1 {
		descWidth = 1
	}
	return styleOverlayBox.Width(boxWidth).Render(strings.TrimRight(renderActionsWindow(m, descWidth), "\n"))
}

func actionsBoxWidth(m Model) int {
	frameWidth := styleOverlayBox.GetHorizontalFrameSize()
	content := strings.TrimRight(renderActions(m, naturalActionsDescWidth(m)), "\n")
	boxWidth := 70
	if contentWidth := popupWidth(content) + frameWidth; contentWidth > boxWidth {
		boxWidth = contentWidth
	}
	if avail := m.width - 4; avail < boxWidth {
		boxWidth = avail
	}
	if boxWidth < 1 {
		boxWidth = 1
	}
	return boxWidth
}

func actionsBoxDescWidth(m Model) int {
	descWidth := actionsBoxWidth(m) - styleOverlayBox.GetHorizontalFrameSize() - actionDescIndent
	if descWidth < 1 {
		return 1
	}
	return descWidth
}

func naturalActionsDescWidth(m Model) int {
	width := 1
	for _, a := range actionsFor(m) {
		if w := displayWidth(a.description); w > width {
			width = w
		}
	}
	return width
}
