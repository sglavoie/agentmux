// The keymap, written out.
//
// The footer has one row and eleven entries competing for it, so what it can say
// about a binding is its key and one word. This is where the rest goes: every
// binding the grid answers, including the ones the footer drops under width
// pressure and the one it never shows at all, each with the sentence that says
// what bounds it.
//
// It is GENERATED from the binding table rather than written beside it. A help
// screen maintained by hand is the classic place a keymap rots — a key gets
// rebound and the help goes on naming the old one, or a binding is added and
// nothing mentions it — and the whole reason bindings.go exists is that the
// dispatcher, the footer, and this file are three readings of one declaration.
// TestHelpCoversEveryBinding is what holds the generation honest.
//
// What is here is the DRAWING only, which is why a geometry struct goes in and a
// string comes out: the overlay's state, and the keys that move it, are in
// help_keys.go, the same split action_keys.go and actions.go already make.

package overview

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

const (
	// helpNaturalWidth is the overlay's preferred outer width, clamped below so
	// a narrow terminal reflows rather than clips.
	helpNaturalWidth = 76
	// helpKeyColumn is the width of the key column. It is a constant rather than
	// a measurement of the table because the column is a column: an operator
	// scans it, and a width that moved when a binding was renamed would move
	// every sentence on the screen with it.
	helpKeyColumn = 13
	// helpKeyGap is the air between the key column and its sentence.
	helpKeyGap = 2
	// helpTitle names the surface the keys belong to.
	helpTitle = "Overview keys"
)

var styleHelpBox = theme.Popup()

// helpFrame is everything one render of the overlay depends on: the terminal
// width it is clamped against, the body height the chrome left it, and how far
// the operator has scrolled.
//
// It is a struct of three integers rather than the model because the keymap is a
// property of the program and not of what is running under it. A test renders
// every target size by naming three numbers.
type helpFrame struct {
	width  int
	height int
	offset int
}

// helpBoxWidth is the overlay's outer width: its preferred width, always leaving
// two terminal cells on either side. It is the same clamp the destructive
// confirmation uses, so the two overlays line up on a narrow terminal.
func helpBoxWidth(terminalWidth int) int {
	width := helpNaturalWidth
	if terminalWidth > 0 {
		width = min(width, terminalWidth-4)
	}
	return max(width, 1)
}

// helpContentWidth is the interior the rules, the key column, and the wrapped
// sentences are laid out against.
func helpContentWidth(terminalWidth int) int {
	return max(helpBoxWidth(terminalWidth)-styleHelpBox.GetHorizontalFrameSize(), 1)
}

// helpChrome is which of the two fixed rows a short terminal can still afford.
//
// The header goes first and the hint last, which inverts what "least useful"
// would suggest. The header repeats the name of the surface the operator is
// standing on; the hint is the only statement of how to scroll this list and how
// to get out of it, and an overlay that can be opened but not obviously left is
// the failure this affordance would otherwise be most likely to have.
func helpChrome(bodyHeight int) (header, hint bool, visible int) {
	fit := max(bodyHeight-styleHelpBox.GetVerticalFrameSize(), 1)
	header, hint = true, true
	visible = fit - 2
	if visible < 1 {
		header = false
		visible = fit - 1
	}
	if visible < 1 {
		hint = false
		visible = fit
	}
	return header, hint, max(visible, 1)
}

// helpVisible is how many content rows are on screen at once.
func helpVisible(bodyHeight int) int {
	_, _, visible := helpChrome(bodyHeight)
	return visible
}

// helpLines renders the keymap into rows of the given width.
//
// It is a package function of the width alone — no model, no clock, no session
// list — because the keymap does not depend on what is running. A test asserts on
// it directly at every target width.
func helpLines(width int) []string {
	if width < 1 {
		return nil
	}
	keyWidth := min(helpKeyColumn, max(width-helpKeyGap-1, 1))
	textWidth := max(width-keyWidth-helpKeyGap, 1)
	gap := strings.Repeat(" ", helpKeyGap)
	indent := strings.Repeat(" ", keyWidth+helpKeyGap)

	lines := make([]string, 0, 32)
	for _, section := range helpSectionOrder {
		bindings := helpBindingsIn(section)
		if len(bindings) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, styleDim.Render(layout.RuleRow(section, width)))
		for _, b := range bindings {
			key := styleFooterKey.Render(layout.PadRight(
				layout.Truncate(b.footerKeysText(), keyWidth, "…"), keyWidth))
			wrapped := layout.WrapWords(b.help, textWidth)
			if len(wrapped) == 0 {
				wrapped = []string{""}
			}
			for i, text := range wrapped {
				if i == 0 {
					lines = append(lines, key+gap+text)
					continue
				}
				lines = append(lines, indent+text)
			}
		}
	}
	return lines
}

// helpBindingsIn is the table's entries for one section, in declaration order.
func helpBindingsIn(section string) []gridBinding {
	out := make([]gridBinding, 0, len(gridBindings()))
	for _, b := range gridBindings() {
		if b.section == section {
			out = append(out, b)
		}
	}
	return out
}

// helpBody is the overlay's interior.
func helpBody(f helpFrame) string {
	width := helpContentWidth(f.width)
	header, hint, visible := helpChrome(f.height)
	content := helpLines(width)
	offset := clamp(f.offset, 0, max(len(content)-visible, 0))
	end := min(offset+visible, len(content))

	lines := make([]string, 0, visible+2)
	if header {
		lines = append(lines, styleTitle.Render(layout.Truncate(helpTitle, width, "…")))
	}
	for _, line := range content[offset:end] {
		lines = append(lines, layout.PadRight(line, width))
	}
	for len(lines) < visible+boolRow(header) {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if hint {
		lines = append(lines, helpHintLine(width, offset > 0, end < len(content)))
	}
	return strings.Join(lines, "\n")
}

// boolRow is 1 for a chrome row that is drawn and 0 for one that was shed.
func boolRow(present bool) int {
	if present {
		return 1
	}
	return 0
}

// helpHintLine is the overlay's own footer: the scroll markers, and the only
// place that says how to leave.
//
// It is the single help affordance the base footer's `? help` points at, and the
// base footer says nothing about scrolling or dismissal — the two rows explain
// different things rather than the same thing twice.
func helpHintLine(width int, above, below bool) string {
	if width < 1 {
		return ""
	}
	left, right := "  ", "  "
	if above {
		left = "▲ "
	}
	if below {
		right = " ▼"
	}
	middleWidth := max(width-layout.Width(left)-layout.Width(right), 0)
	middle := strings.Join([]string{
		chrome.Keys("scroll", chrome.Key{Text: "j/k", Available: true}),
		chrome.Keys("page", chrome.Key{Text: "ctrl+f/b", Available: true}),
		chrome.Keys("ends", chrome.Key{Text: "g/G", Available: true}),
		chrome.Keys("close", chrome.Key{Text: "esc", Available: true}, chrome.Key{Text: "q", Available: true}, chrome.Key{Text: "?", Available: true}),
	}, styleDim.Render(footerSeparator))
	middle = layout.StyledTruncate(middle, middleWidth, "…")
	if padding := middleWidth - layout.StyledWidth(middle); padding > 0 {
		middle += strings.Repeat(" ", padding)
	}
	return styleDim.Render(left) + middle + styleDim.Render(right)
}

// helpBox is the bounded modal the grid is composited under.
func helpBox(f helpFrame) string {
	return styleHelpBox.Width(helpBoxWidth(f.width)).Render(helpBody(f))
}
