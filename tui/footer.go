// This file is the root footer: the hint line under every mode. It holds the
// entry vocabulary a palette is written in, the binding lookup that resolves an
// entry's key display, and the wrapping that fits the chosen palette into the
// terminal's width without ever splitting a binding across lines.

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
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
			footerLine(m.width, " · ", "filter: @profile", "!state (!attention)", "word", "\\@/\\! literal"),
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
	// The compact band spends one physical row on the footer and no more. Two
	// full blocks reflow to five or six rows at those widths, which is a third
	// of a short terminal spent restating keys that are all in the command
	// palette — and every row it takes is a session the list does not show.
	compact := dashboard.BandFor(m.width) == dashboard.BandCompact
	switch {
	case m.pendingJumpDir != 0 && compact:
		// The chord's vocabulary is the whole point of the row while a chord is
		// pending, so it is what the single row carries; the action blocks stay
		// dropped.
		return []string{compactFooterRow(m, "  ", compactJumpChordTokens(m), nil)}
	case compact && m.previewEnabled:
		return nil
	case compact:
		required, optional := compactListFooterTokens(m)
		return []string{compactFooterRow(m, "  ", required, optional)}
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

// compactFooterToken is one binding in the two spellings the compact row may
// draw it in: the labelled form it prefers, and the bare key it falls back to
// when the labelled forms together do not fit the terminal.
//
// Both spellings name the same key, which is what makes the fallback a change
// of wording rather than a change of vocabulary — the operator who learned the
// row at one width has not been shown a different row at another.
type compactFooterToken struct{ long, short string }

func compactBinding(key, label string) compactFooterToken {
	return compactBindingKeys(key, label, key)
}

// compactBindingKeys is the variant for a key whose own name is too long to
// survive the fallback. "enter" spelled out is five of the nineteen cells the
// narrowest supported dashboard has; ↵ is one, and it is the glyph the key is
// printed with on the keyboard the operator is looking at.
func compactBindingKeys(key, label, shortKey string) compactFooterToken {
	return compactFooterToken{
		long:  renderFooterBinding(key, label),
		short: styleFooterKey.Render(shortKey),
	}
}

// compactListFooterTokens splits the compact footer's palette into what the row
// keeps at any width and what it adds when there is room.
//
// The four required tokens are the ones without which the dashboard cannot be
// operated at all: move the cursor, act on the row, reach every other command,
// and find out what those are. Everything else — including every row action in
// listFooterPalette — is reachable through the palette token that is always
// present, which is what makes dropping it defensible rather than merely
// necessary.
func compactListFooterTokens(m Model) (required, optional []compactFooterToken) {
	focus := compactBindingKeys("enter", "focus", "↵")
	if m.summarySelected() {
		// Same substitution the wide footer makes: a summary has no tab to focus,
		// so enter names the fold it actually performs.
		focus = compactBindingKeys("enter", "fold", "↵")
	}
	required = []compactFooterToken{
		compactBinding("j/k", "move"),
		focus,
		compactBinding(":", "cmds"),
		compactBinding("?", "help"),
	}
	optional = []compactFooterToken{
		compactBinding("v", "view"),
		compactBinding("q", "quit"),
	}
	return required, optional
}

// compactJumpChordTokens is the chord vocabulary, abbreviated to the letters
// themselves. The wide footer spells each target out; at compact widths the
// spelling is what does not fit, and the letters are what the chord needs.
//
// Unlike the list row it has no required subset: a chord is eight targets, and
// a terminal too narrow for eight letters has nothing to thin down to. It is
// the one compact row that may still clamp.
func compactJumpChordTokens(m Model) []compactFooterToken {
	arrow := "next"
	if m.pendingJumpDir == -1 {
		arrow = "prev"
	}
	chord := func(key, tail string) compactFooterToken {
		return compactFooterToken{
			long:  renderFooterBindingWithLabel(key, "", tail, styleFooter),
			short: styleFooterKey.Render(key),
		}
	}
	label := styleFooter.Render("jump " + arrow + ":")
	return []compactFooterToken{
		{long: label, short: label},
		chord("p", ")erm"),
		chord("e", ")rr"),
		chord("a", ")ctive"),
		chord("q", ")uestion"),
		chord("s", ")top"),
		chord("c", ")ompact"),
		chord("b", ")sub"),
		{
			long:  renderFooterBindingWithLabel("esc", " ", "cancels", styleFooter),
			short: styleFooterKey.Render("esc"),
		},
	}
}

// compactFooterRow renders exactly one physical row: every required token, then
// each optional token that fits whole, then a hard clamp.
//
// The required tokens are never dropped and never half-drawn. When their
// labelled spellings do not fit, the whole row falls back to bare keys rather
// than eliding the tail — a row ending in "…" tells the operator there is
// something they cannot see, whereas "j/k · enter · : · ?" tells them the four
// keys, which is the thing the row exists to say. The fallback is all-or-
// nothing so the row never mixes spellings and implies that the tokens still
// spelled out are the more important ones.
//
// Optional tokens are appended in the same spelling and stop at the first one
// that does not fit rather than skipping to a narrower later one, so the row is
// always a prefix of the declared palette and does not reorder itself as the
// terminal is dragged.
func compactFooterRow(m Model, indent string, required, optional []compactFooterToken) string {
	sep := styleFooter.Render(" · ")
	sepW := layout.StyledWidth(sep)

	spell := func(t compactFooterToken) string { return t.long }
	if m.width > 0 && compactFooterRowWidth(indent, sepW, required, spell) > m.width {
		spell = func(t compactFooterToken) string { return t.short }
	}

	parts := make([]string, 0, len(required))
	for _, t := range required {
		parts = append(parts, spell(t))
	}
	line := indent + strings.Join(parts, sep)
	width := layout.StyledWidth(line)
	for _, t := range optional {
		cost := sepW + layout.StyledWidth(spell(t))
		if m.width > 0 && width+cost > m.width {
			break
		}
		line += sep + spell(t)
		width += cost
	}
	if m.width > 0 {
		line = layout.StyledTruncate(line, m.width, "…")
	}
	return line
}

func compactFooterRowWidth(indent string, sepW int, tokens []compactFooterToken,
	spell func(compactFooterToken) string) int {
	w := layout.Width(indent)
	for i, t := range tokens {
		if i > 0 {
			w += sepW
		}
		w += layout.StyledWidth(spell(t))
	}
	return w
}

func listFooterNavTokens(m Model) []string {
	// A family summary has no tab to focus and no session to act on, so the two
	// row-targeted tokens in this row change with it: enter becomes the fold it
	// actually performs, and the actions overlay renders in the unavailable
	// style, exactly as the row-section entries below already do.
	focusToken := renderFooterBinding("enter", "focus")
	actionsToken := renderFooterBinding("a", "actions")
	if m.summarySelected() {
		focusToken = renderFooterBinding("enter/←/→", "fold/unfold")
		actionsToken = renderUnavailableFooterBinding("a", "actions")
	}
	navTokens := []string{
		renderFooterBinding("j/k", "up/down"),
		renderFooterBinding("]/[", "jump-chord"),
		renderFooterBinding("}/{", "nav+focus"),
		focusToken,
		actionsToken,
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
	if s, ok := m.selectedSession(); ok {
		if g := dashboard.StateGloss(s.State); g != "" {
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
	// Counted in items, not sessions: start and end are derived from
	// m.listOffset, which is an item index, so the denominator has to be the
	// same unit or the indicator drifts the moment a line stands for more than
	// one session.
	total := m.listItemCount()
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

// renderFooterBinding renders an available key separately from its dim
// description. Keeping the two segments independent lets keys carry the
// shared accent without changing the description's established styling.
func renderFooterBinding(key, description string) string {
	return renderFooterBindingWithLabel(key, " ", description, styleFooter)
}

// renderUnavailableFooterBinding keeps an unavailable key visible in the
// muted palette while its description retains the same dim style used by
// available bindings.
func renderUnavailableFooterBinding(key, description string) string {
	return styleFooterUnavailableKey.Render(key) + " " + styleFooter.Render(description)
}

// renderFooterBindingWithLabel is the custom-label variant used when a
// description has semantic styling (for example, a destructive red label) or
// a compact prompt needs no gap between the key and label (for example [y]es).
func renderFooterBindingWithLabel(key, gap, label string, labelStyle lipgloss.Style) string {
	return styleFooterKey.Render(key) + gap + labelStyle.Render(label)
}

func footerBindingToken(key, description string) footerToken {
	return footerRenderedToken(renderFooterBinding(key, description))
}

func footerUnavailableBindingToken(key, description string) footerToken {
	return footerRenderedToken(renderUnavailableFooterBinding(key, description))
}

func footerCustomBindingToken(key, gap, label string, labelStyle lipgloss.Style) footerToken {
	return footerRenderedToken(renderFooterBindingWithLabel(key, gap, label, labelStyle))
}

type footerEntry struct {
	key      string
	label    string
	section  string
	availFn  func(Model) bool
	priority int
}

// Section identifiers for footer command rows.
const (
	footerSectionRow    = "row"
	footerSectionLaunch = "launch"
	footerSectionApp    = "app"
)

// hasRow reports whether a row is selected; any row-targeted action requires a
// cursor sitting on a session. A cursor parked on an item that is not a session
// leaf reports false, so every row-section footer entry below renders in the
// unavailable style — which is the same thing the key handlers do when they
// fail to resolve a target.
func hasRow(m Model) bool {
	return m.hasSelectedSession()
}

// listFooterPalette is the fixed-order set of keys shown in the ModeList footer
// command rows. Order is invariant — entries never move between states. Each
// entry's availability function decides whether to render with the available
// or unavailable style.
// Every entry carries priority 0: the ModeList footer is budgeted by
// listFooterBudgetLineCount rather than truncated, so no entry is ever dropped.
var listFooterPalette = []footerEntry{
	{"e", "peek", footerSectionRow, func(m Model) bool {
		s, ok := m.selectedSession()
		return ok && s.State == "bolt"
	}, 0},
	{"T", "timeline", footerSectionRow, hasRow, 0},
	{"r", "restart", footerSectionRow, hasRow, 0},
	{"c", "profile", footerSectionRow, hasRow, 0},
	{"C", "launch", footerSectionRow, hasRow, 0},
	{"s", "project sessions", footerSectionRow, hasRow, 0},
	{"x", "clear", footerSectionRow, func(m Model) bool {
		s, ok := m.selectedSession()
		if !ok {
			return false
		}
		return dashboard.StateGroup(s, m.lastActivity, m.idleThreshold) == 0
	}, 0},
	{"X", "close", footerSectionRow, hasRow, 0},
	{"o", "open dir", footerSectionRow, func(m Model) bool {
		s, ok := m.selectedSession()
		if !ok {
			return false
		}
		_, found := commands.FindConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject())
		return found
	}, 0},
	{"O", "open in editor", footerSectionRow, func(m Model) bool {
		s, ok := m.selectedSession()
		if !ok {
			return false
		}
		_, found := commands.FindConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject())
		return found
	}, 0},
	{"f", "profile", footerSectionLaunch, func(Model) bool { return true }, 0},
	{"l", "launch", footerSectionLaunch, func(Model) bool { return true }, 0},
	// A view action, not a row action: switching the arrangement is available
	// with the cursor on a summary, on a session, and on an empty list alike.
	{"v", "group view", footerSectionApp, func(Model) bool { return true }, 0},
	{":", "command palette", footerSectionApp, func(Model) bool { return true }, 0},
	{"S", "sessions", footerSectionApp, func(Model) bool { return true }, 0},
	{"P", "plans", footerSectionApp, func(Model) bool { return true }, 0},
	{"R", "resort", footerSectionApp, func(Model) bool { return true }, 0},
}

func listFooterActions(m Model) string {
	return listFooterSectionsLine(m, []string{
		footerSectionRow,
		footerSectionLaunch,
		footerSectionApp,
	})
}

func listFooterSectionsLine(m Model, sections []string) string {
	const indent = "  "
	innerSep := styleFooter.Render(" · ")
	innerSepW := layout.StyledWidth(innerSep)

	var tokens []footerToken
	for _, sec := range sections {
		entries := footerEntriesForSection(sec)
		if len(entries) == 0 {
			continue
		}
		for i, e := range entries {
			var tok footerToken
			if e.availFn(m) {
				tok = footerBindingToken(e.key, e.label)
			} else {
				tok = footerUnavailableBindingToken(e.key, e.label)
			}
			if i < len(entries)-1 || hasLaterEntries(sections, sec) {
				tok.sep = innerSep
				tok.sepWidth = innerSepW
			}
			tokens = append(tokens, tok)
		}
	}
	return wrapFooterLine(indent, indent, tokens, m.width)
}

func hasLaterEntries(sections []string, current string) bool {
	foundCurrent := false
	for _, sec := range sections {
		if !foundCurrent {
			if sec == current {
				foundCurrent = true
			}
			continue
		}
		if len(footerEntriesForSection(sec)) > 0 {
			return true
		}
	}
	return false
}

// footerToken pairs a styled rendered string with its visible width and an
// optional separator that follows it within a section. Pre-measuring with
// layout.StyledWidth lets us account for ANSI styling correctly.
type footerToken struct {
	rendered string
	width    int
	sep      string
	sepWidth int
}

// wrapFooterLine renders tokens onto one or more lines, breaking when the
// next token's render width would exceed maxWidth. firstLineLead is the lead
// rendered before the first token of the first line; continuationIndent is
// the left margin applied to every subsequent line. The trailing separator
// of the last token on each line is dropped so wrap points stay clean.
//
// When maxWidth <= 0 (uninitialized width before the first WindowSizeMsg),
// the helper emits a single unwrapped line — matching pre-wrap behavior.
// A token whose width alone exceeds maxWidth is still placed on its own
// line; we never split mid-token, mirroring renderChips.
func wrapFooterLine(firstLineLead, continuationIndent string, tokens []footerToken, maxWidth int) string {
	if len(tokens) == 0 {
		return firstLineLead
	}
	if maxWidth <= 0 {
		var sb strings.Builder
		sb.WriteString(firstLineLead)
		for i, t := range tokens {
			sb.WriteString(t.rendered)
			if i < len(tokens)-1 {
				sb.WriteString(t.sep)
			}
		}
		return sb.String()
	}

	leadW := layout.StyledWidth(firstLineLead)
	contW := layout.StyledWidth(continuationIndent)

	var lines []string
	var cur strings.Builder
	cur.WriteString(firstLineLead)

	lineW := leadW
	lineHasToken := false
	pendingSep := ""
	pendingSepW := 0
	for _, t := range tokens {
		// Cost to extend the current line with this token: the prior
		// token's separator (if any) plus the token itself.
		cost := t.width
		if lineHasToken {
			cost += pendingSepW
		}
		if lineHasToken && lineW+cost > maxWidth {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(continuationIndent)
			lineW = contW
			lineHasToken = false
			cost = t.width
		}
		if lineHasToken {
			cur.WriteString(pendingSep)
		}
		cur.WriteString(t.rendered)
		lineW += cost
		lineHasToken = true
		pendingSep = t.sep
		pendingSepW = t.sepWidth
	}
	lines = append(lines, cur.String())

	// Final per-line hard clamp: a lead that alone exceeds maxWidth (the
	// ModeList state gloss, #1) or a single token wider than the terminal
	// (the legend entry #3 or the picker DSL hint #4) is never split by the
	// wrap loop above, so truncate every emitted display line to maxWidth.
	// Mirrors the per-row/per-line safety clamp in list_rows.go and picker.go.
	for i, line := range lines {
		lines[i] = layout.StyledTruncate(line, maxWidth, "…")
	}
	return strings.Join(lines, "\n")
}

// footerEntriesForSection returns the palette entries whose section matches
// the given identifier, preserving declaration order.
func footerEntriesForSection(section string) []footerEntry {
	var out []footerEntry
	for _, e := range listFooterPalette {
		if e.section == section {
			out = append(out, e)
		}
	}
	return out
}
