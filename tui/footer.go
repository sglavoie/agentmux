package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/layout"
)

var footerStripeBackgroundANSI = ansi.Style{}.
	BackgroundColor(ansi.HexColor(footerStripeBackgroundHex)).
	String()

func renderFooter(m Model) string {
	var lines []string
	switch m.mode {
	case ModeSearch:
		lines = nonEmptyFooterLines(
			footerLine(m.width, " · ", "filter: @profile", "!state", "word", "\\@/\\! literal"),
			footerStyledLine(m.width, "  ", renderFooterBinding("enter", "apply"), renderFooterBinding("esc", "cancel")),
		)
	case ModeConfirm:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, "   ",
			renderFooterBindingWithLabel("[y]", "", "es", styleFooter),
			renderFooterBindingWithLabel("[n]", "", "o", styleFooter),
			renderFooterBinding("[esc]", "cancel"),
		))
	case ModePicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, "  ",
				renderFooterBinding("space", "toggle"), renderFooterBinding("enter", "launch"),
				renderFooterBinding("f", "profile"), renderFooterBinding("P", "pin"),
				renderFooterBinding("s", "sessions"), renderFooterBinding("C", "profile"),
				renderFooterBinding("o", "open dirs"), renderFooterBinding("O", "open in editor"),
				renderFooterBinding("p", "preview"), renderFooterBinding("esc", "clear/exit"),
			),
			footerStyledLine(m.width, "  ", styleFooter.Render("DSL: @profile, word"), renderFooterBinding("j/k", "up/down"), renderFooterBinding("g/G", "top/bottom")),
		)
	case ModeConfigPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, "  ", renderFooterBinding("enter", "select"), renderFooterBinding("esc/q", "cancel")),
			footerStyledLine(m.width, "  ", renderFooterBinding("j/k", "up/down")),
		)
	case ModeLiveSessionPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, "  ", renderFooterBinding("enter", "focus"), renderFooterBinding("esc/q", "cancel")),
			footerStyledLine(m.width, "  ", renderFooterBinding("j/k", "up/down")),
		)
	case ModeHelp:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, "  ", renderFooterBinding("j/k", "up/down"), renderFooterBinding("g/G", "top/bottom"), renderFooterBinding("esc/?/q", "close help")))
	case ModeActions:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, "  ", renderFooterBinding("<key>", "run action"), renderFooterBinding("esc/q", "back")))
	case ModeCommandPalette:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, " · ", renderFooterBinding("↑/↓ or ctrl+j/k or ctrl+n/p", "move"), renderFooterBinding("enter", "run"), renderFooterBinding("esc", "close"), renderFooterBinding("ctrl+c", "quit")))
	case ModeDetail:
		// Inner box footer already carries the scroll position and detail
		// keymap; the outer hint area stays blank so the dimmed dashboard
		// footer doesn't surface irrelevant row-action keys.
		lines = nil
	case ModeChipPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, "  ", renderFooterBinding("enter", "select"), renderFooterBinding("esc/q", "cancel")),
			footerStyledLine(m.width, "  ", renderFooterBinding("/", "search"), renderFooterBinding("j/k", "up/down"), renderFooterBinding("g/G", "top/bottom"), renderFooterBinding("pgup/pgdn", "page")),
		)
	case ModeSessionSearch:
		lines = footerHintLines(m, sessionSearchFooterPalette, footerHintMaxLines)
	case ModeSessionView:
		lines = footerHintLines(m, sessionViewFooterPalette, footerHintMaxLines)
	case ModePlanSearch:
		lines = footerHintLines(m, planSearchFooterPalette, footerHintMaxLines)
	case ModePlanView:
		lines = footerHintLines(m, planViewFooterPalette, footerHintMaxLines)
	case ModeWorktrees:
		lines = footerHintLines(m, worktreesFooterPalette, footerHintMaxLines)
	case ModeASLRuns:
		lines = footerHintLines(m, aslRunsFooterPalette, footerHintMaxLines)
	case ModeASLResponse:
		lines = footerHintLines(m, aslResponseFooterPalette, footerHintMaxLines)
	case ModeMessageDetail:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, " · ", renderFooterBinding("j/k", "scroll"), renderFooterBinding("g/G", "top/bottom"), renderFooterBinding("esc/q/enter", "close")))
	case ModeDeleteChoice:
		lines = nonEmptyFooterLines(footerLineFromTokens(m.width, " · ",
			footerBindingToken("[i]", "index only"),
			footerCustomBindingToken("[f]", " ", "also delete file", styleError),
			footerBindingToken("[esc]", "cancel"),
		))
	default: // ModeList
		position := ""
		if m.pendingJumpDir == 0 && !m.previewEnabled {
			position = listPositionFragment(m)
		}
		lines = listModeFooterLines(m, position)
	}

	var sb strings.Builder
	if len(lines) > 0 {
		// Every root-level footer shares the same alternating background so
		// navigation hints remain visually consistent as the user moves
		// between dashboard views and transient overlays.
		lines = zebraFooterLines(lines, m.width)
		sb.WriteString(strings.Join(lines, "\n"))
	}
	return sb.String()
}

func footerLine(maxWidth int, separator string, fragments ...string) string {
	rendered := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		if fragment == "" {
			continue
		}
		rendered = append(rendered, styleFooter.Render(fragment))
	}
	return footerStyledLine(maxWidth, separator, rendered...)
}

func footerStyledLine(maxWidth int, separator string, rendered ...string) string {
	tokens := make([]footerToken, 0, len(rendered))
	for _, r := range rendered {
		if r == "" {
			continue
		}
		tokens = append(tokens, footerRenderedToken(r))
	}
	return footerLineFromTokens(maxWidth, separator, tokens...)
}

func footerLineFromTokens(maxWidth int, separator string, tokens ...footerToken) string {
	if len(tokens) == 0 {
		return ""
	}
	sep := styleFooter.Render(separator)
	sepW := layout.StyledWidth(sep)
	for i := 0; i < len(tokens)-1; i++ {
		tokens[i].sep = sep
		tokens[i].sepWidth = sepW
	}
	return wrapFooterLine("  ", "  ", tokens, maxWidth)
}

func footerRenderedToken(rendered string) footerToken {
	return footerToken{
		rendered: rendered,
		width:    layout.StyledWidth(rendered),
	}
}

func nonEmptyFooterLines(lines ...string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func listFooterLines(m Model, selectedLine string) []string {
	lines := []string{listFooterActions(m)}
	if selectedLine != "" {
		lines = append(lines, selectedLine)
	}
	return lines
}

// listModeFooterLines assembles the unstyled physical blocks for ModeList.
// position is passed in by callers so layout budgeting can measure the same
// footer without recursively asking listVisibleHeight for the position text.
func listModeFooterLines(m Model, position string) []string {
	switch {
	case m.pendingJumpDir != 0:
		// Bracket chord pending: show the chord vocabulary instead of
		// the usual nav row so the user can complete or cancel.
		arrow := "next"
		if m.pendingJumpDir == -1 {
			arrow = "prev"
		}
		return listFooterLines(m, footerStyledLine(m.width, "  ",
			styleFooter.Render(fmt.Sprintf("jump %s: ", arrow))+renderFooterBindingWithLabel("p", "", ")perm", styleFooter),
			renderFooterBindingWithLabel("e", "", ")fail", styleFooter),
			renderFooterBindingWithLabel("a", "", ")active", styleFooter),
			renderFooterBindingWithLabel("q", "", ")question", styleFooter),
			renderFooterBindingWithLabel("s", "", ")stop", styleFooter),
			renderFooterBindingWithLabel("c", "", ")compact", styleFooter),
			renderFooterBindingWithLabel("b", "", ")sub", styleFooter),
			renderFooterBindingWithLabel("]]/[[", "", ")attention", styleFooter),
			styleFooter.Render("(")+renderFooterBindingWithLabel("esc", "", " cancels)", styleFooter),
		))
	case m.previewEnabled:
		// In preview mode the only binding shown is `p: hide preview`,
		// which is rendered right-aligned in the title row.
		return nil
	default:
		return listFooterLines(m, listFooterLine2WithPosition(m, listFooterNavTokens(m), position))
	}
}

func listFooterNavTokens(m Model) []string {
	navTokens := []string{
		renderFooterBinding("j/k", "up/down"),
		renderFooterBinding("]/[", "jump-chord"),
		renderFooterBinding("}/{", "nav+focus"),
		renderFooterBinding("enter", "focus"),
		renderFooterBinding("a", "actions"),
		renderFooterBinding("/", "filter"),
		renderFooterBinding("?", "help"),
		renderFooterBinding("q", "quit"),
		renderFooterBinding("p", "preview"),
	}
	if !m.activeFilter.IsEmpty() {
		navTokens = append(navTokens, renderFooterBinding("esc", "clear filters"))
		if n := len(m.activeFilter.Components()); n > 0 {
			navTokens = append(navTokens,
				renderFooterBinding(fmt.Sprintf("1-%d", n), "remove chip"),
				renderFooterBinding("tab", "focus next"),
			)
		}
	}
	return navTokens
}

// zebraFooterLines flattens wrapped footer blocks into physical rows, then
// applies the shared root-footer zebra treatment. The first row receives the
// dark background and is padded to the viewport width; the next row retains
// the terminal's default background, and so on.
func zebraFooterLines(lines []string, maxWidth int) []string {
	rows := strings.Split(strings.Join(lines, "\n"), "\n")
	for i := range rows {
		if i%2 == 0 {
			rows[i] = renderFooterStripeRow(rows[i], maxWidth)
		}
	}
	return rows
}

// renderFooterStripeRow adds a full-width background without flattening the
// foreground styles already embedded in the row. Lipgloss resets attributes
// after each styled token, so the background sequence is re-applied after
// every reset before the remaining content and trailing padding are emitted.
func renderFooterStripeRow(row string, maxWidth int) string {
	background := footerStripeBackgroundSequence()

	var sb strings.Builder
	sb.WriteString(background)
	sb.WriteString(strings.ReplaceAll(row, ansi.ResetStyle, ansi.ResetStyle+background))
	if maxWidth > 0 {
		if padding := maxWidth - layout.StyledWidth(row); padding > 0 {
			sb.WriteString(strings.Repeat(" ", padding))
		}
	}
	sb.WriteString(ansi.ResetStyle)
	return sb.String()
}

func footerStripeBackgroundSequence() string {
	return footerStripeBackgroundANSI
}

// listFooterLine2 prefixes the navigation hints with a gloss of the cursor
// row's state icon (e.g. `⚡ active — j/k: up/down …`). The gloss text picks
// up the state's accent color so the meaning of the row icon is reinforced;
// the dash separator and trailing nav stay in the dim footer style. When the
// filtered list overflows the visible window a `start–end of total` position
// fragment is prepended so the user sees where the cursor sits.
//
// navFragments is the ordered list of navigation hints to render; each
// fragment is joined with the standard double-space separator and wrapped to
// fit m.width.
func listFooterLine2(m Model, navFragments []string) string {
	return listFooterLine2WithPosition(m, navFragments, listPositionFragment(m))
}

func listFooterLine2WithPosition(m Model, navFragments []string, position string) string {
	sep := styleFooter.Render("  ")
	sepW := layout.StyledWidth(sep)

	tokens := make([]footerToken, 0, len(navFragments)+1)
	if position != "" {
		// The position fragment sits before the nav hints with a wider
		// three-space gap so the eye chunks it as a separate group.
		gap := styleFooter.Render("   ")
		gapW := layout.StyledWidth(gap)
		posRendered := styleFooter.Render(position)
		tokens = append(tokens, footerToken{
			rendered: posRendered,
			width:    layout.StyledWidth(posRendered),
			sep:      gap,
			sepWidth: gapW,
		})
	}

	for i, rendered := range navFragments {
		tok := footerToken{
			rendered: rendered,
			width:    layout.StyledWidth(rendered),
		}
		if i < len(navFragments)-1 {
			tok.sep = sep
			tok.sepWidth = sepW
		}
		tokens = append(tokens, tok)
	}
	firstLead, contIndent := listFooterLine2Leads(m)
	return wrapFooterLine(firstLead, contIndent, tokens, m.width)
}

// listFooterLine2Leads returns the rendered prefix for the first line and the
// indent used on continuation lines. When the cursor row has a state gloss
// the first line carries the accent-colored "⚡ active —" lead; continuation
// lines fall back to the plain two-space indent so the gloss does not repeat.
func listFooterLine2Leads(m Model) (string, string) {
	indent := "  "
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		s := m.filtered[m.cursor]
		if g := StateGloss(s.State); g != "" {
			icon := palette.StateIcon(s.State)
			glossText := layout.PadIconCell(icon) + " " + g
			lead := indent + stateAccentStyle(s.State).Render(glossText) + styleFooter.Render(" — ")
			return lead, indent
		}
	}
	return indent, indent
}

// listPositionFragment returns a `start–end of total` indicator (mirroring
// detailFooter) when the filtered list overflows the visible window.
// Returns "" when everything fits or the model has not yet been resized.
func listPositionFragment(m Model) string {
	return listPositionFragmentForVisible(m, listVisibleHeight(m))
}

func listPositionFragmentForVisible(m Model, visible int) string {
	total := len(m.filtered)
	if total == 0 || m.height == 0 {
		return ""
	}
	if visible >= total {
		return ""
	}
	start := m.listOffset + 1
	end := m.listOffset + visible
	if end > total {
		end = total
	}
	if start < 1 {
		start = 1
	}
	return fmt.Sprintf("%d–%d of %d", start, end, total)
}

// listFooterBudgetLineCount measures the reflowed ModeList footer using the
// same builders as renderFooter. Position text depends on the list height,
// while list height depends on footer rows, so iterate the small calculation
// to a stable row count rather than falling back to a hard-coded budget.
func listFooterBudgetLineCount(m Model) int {
	if m.previewEnabled {
		return 0
	}

	position := ""
	lineCount := countFooterPhysicalRows(listModeFooterLines(m, position))
	maxLineCount := lineCount
	for range 4 {
		visible := listVisibleHeightForFooterLines(m, lineCount)
		nextPosition := ""
		if m.pendingJumpDir == 0 {
			nextPosition = listPositionFragmentForVisible(m, visible)
		}
		nextLineCount := countFooterPhysicalRows(listModeFooterLines(m, nextPosition))
		if nextLineCount > maxLineCount {
			maxLineCount = nextLineCount
		}
		if nextLineCount == lineCount && nextPosition == position {
			return nextLineCount
		}
		position = nextPosition
		lineCount = nextLineCount
	}
	return maxLineCount
}

func countFooterPhysicalRows(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	return len(strings.Split(strings.Join(lines, "\n"), "\n"))
}

// footerEntry describes one slot in the stable footer palette. The palette is
// rendered in declaration order regardless of the cursor row's state; only the
// styling of each entry changes (available vs unavailable) so users always see
// every action in the same sequence. `section` preserves each action's semantic
// bucket (row actions, launching, app-wide commands) inside the shared wrap.
// `priority` orders truncation when a palette cannot fit its row budget: 0 is
// never dropped, and higher numbers are dropped before lower ones.
