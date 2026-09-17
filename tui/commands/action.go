package commands

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/layout"
)

// Action describes one entry in the per-row actions overlay. KeyDisplay is what
// the user sees in the overlay (e.g. "⏎"); KeyMatch is the literal msg.String()
// value the handler compares against (e.g. "enter").
//
// ID is the same stable identity the palette files the action under, so an
// overlay entry and a palette row can be shown to be the same action rather
// than two things that happen to be worded alike. It is display-irrelevant:
// dispatch is by KeyMatch, as it has to be for a key the operator presses.
//
// Like Command, the type parameter is the root program model: Available reads
// live model state (last activity, idle threshold, configured project) and Run
// returns the mutated model plus its effect, both as closures the root owns.
type Action[M any] struct {
	ID          ID
	KeyDisplay  string
	KeyMatch    string
	Label       string
	Description string
	Available   func(M) bool
	Run         func(M) (M, tea.Cmd)
}

// ActionsInput is the projection the actions overlay renders from. Title is the
// already-resolved session display title; BodyHeight is the root's own body
// measurement, charged before the value reaches this package.
type ActionsInput[M any] struct {
	Model      M
	Actions    []Action[M]
	Title      string
	Offset     int
	Width      int
	BodyHeight int
}

// actionDescIndent is the column at which wrapped description text must resume
// so continuation lines align under the first description column. It matches
// the 22 visible columns produced by `%-2s  %-18s` for the key+label prefix
// plus the 2-space separator before the description.
const actionDescIndent = 24

// actionsScrollChrome counts the inner lines renderActionsWindow reserves
// outside the scrollable action rows: title, blank, top indicator slot, bottom
// indicator slot, and hint.
const actionsScrollChrome = 5

// RenderActions renders the actions overlay for the input's target row.
// Available actions render in default text; unavailable ones render dim.
// Descriptions are word-wrapped to descWidth so continuation lines indent under
// the description column instead of dangling at the left edge.
func RenderActions[M any](in ActionsInput[M], descWidth int) string {
	if descWidth < 1 {
		descWidth = 1
	}

	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Actions for "+layout.RenderTitleIcon(in.Title)) + "\n\n")
	for _, line := range actionBodyLines(in, descWidth) {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	sb.WriteString(styleFooter.Render("press ") + renderFooterBindingWithLabel("a key", " ", "to run", styleFooter) +
		styleFooter.Render(" · ") + renderFooterBinding("esc/q", "to back out") + "\n")
	return sb.String()
}

// actionBodyLines renders only the action rows, including wrapped continuation
// rows. Header, scroll indicators, and footer hints are added by RenderActions
// or renderActionsWindow.
func actionBodyLines[M any](in ActionsInput[M], descWidth int) []string {
	if descWidth < 1 {
		descWidth = 1
	}
	indent := strings.Repeat(" ", actionDescIndent)
	var lines []string
	for _, a := range in.Actions {
		avail := a.Available(in.Model)
		keyCol := fmt.Sprintf("%-2s  %-18s", a.KeyDisplay, a.Label)
		descLines := wrapText(a.Description, descWidth)
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
// action list fits in the root body, it preserves RenderActions' original
// output. On shorter terminals, it scrolls only the action rows while keeping
// the title and dispatch hint visible.
func renderActionsWindow[M any](in ActionsInput[M], descWidth int) string {
	body := actionBodyLines(in, descWidth)
	visible := in.visibleActionRows(len(body))
	if visible >= len(body) {
		return RenderActions(in, descWidth)
	}

	offset := in.Offset
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
	sb.WriteString(styleHeader.Render("Actions for "+layout.RenderTitleIcon(in.Title)) + "\n\n")
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

// visibleActionRows returns how many rendered action-row lines can fit.
// Full-height terminals keep the original non-scrolling layout; short terminals
// spend the available inner height on a scroll window that still leaves the
// title and hint visible.
func (in ActionsInput[M]) visibleActionRows(bodyLines int) int {
	if bodyLines <= 0 {
		return 0
	}
	innerFit := in.BodyHeight - styleOverlayBox.GetVerticalFrameSize()
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

// ScrollBounds returns the total action-row count and how many of them fit.
func (in ActionsInput[M]) ScrollBounds() (total, visible int) {
	body := actionBodyLines(in, in.boxDescWidth())
	total = len(body)
	visible = in.visibleActionRows(total)
	if visible > total {
		visible = total
	}
	return total, visible
}

// ClampOffset returns the input's offset confined to the scrollable range.
func (in ActionsInput[M]) ClampOffset() int {
	total, visible := in.ScrollBounds()
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	return clamp(in.Offset, 0, maxOffset)
}

// wrapText greedy-wraps s into lines no wider than width. Tokens longer than
// width are hard-broken so the caller can guarantee every emitted line fits the
// available column budget.
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

// RenderActionsBox wraps renderActionsWindow in the overlay box so the actions
// overlay can be composited as a centered modal on top of the dimmed
// dashboard. The box grows to fit the widest unwrapped action row and clamps to
// width-4 on narrow terminals to leave a 2-col margin.
func RenderActionsBox[M any](in ActionsInput[M]) string {
	boxWidth := in.boxWidth()
	descWidth := boxWidth - styleOverlayBox.GetHorizontalFrameSize() - actionDescIndent
	if descWidth < 1 {
		descWidth = 1
	}
	return styleOverlayBox.Width(boxWidth).Render(strings.TrimRight(renderActionsWindow(in, descWidth), "\n"))
}

func (in ActionsInput[M]) boxWidth() int {
	frameWidth := styleOverlayBox.GetHorizontalFrameSize()
	content := strings.TrimRight(RenderActions(in, in.naturalDescWidth()), "\n")
	boxWidth := 70
	if contentWidth := layout.PopupWidth(content) + frameWidth; contentWidth > boxWidth {
		boxWidth = contentWidth
	}
	if avail := in.Width - 4; avail < boxWidth {
		boxWidth = avail
	}
	if boxWidth < 1 {
		boxWidth = 1
	}
	return boxWidth
}

func (in ActionsInput[M]) boxDescWidth() int {
	descWidth := in.boxWidth() - styleOverlayBox.GetHorizontalFrameSize() - actionDescIndent
	if descWidth < 1 {
		return 1
	}
	return descWidth
}

func (in ActionsInput[M]) naturalDescWidth() int {
	width := 1
	for _, a := range in.Actions {
		if w := layout.Width(a.Description); w > width {
			width = w
		}
	}
	return width
}
