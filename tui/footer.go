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
		if m.pendingJumpDir == 0 && !m.inspectorFullScreenActive() {
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
	case compact:
		required, optional := compactListFooterTokens(m)
		return []string{compactFooterRow(m, "  ", required, optional)}
	case m.pendingJumpDir != 0:
		// Bracket chord pending: the chord vocabulary replaces the palette
		// outright rather than joining it. Half of these letters mean something
		// else in the normal footer, so showing both rows at once would print two
		// contradictory meanings for `p`, `e`, `a`, `q`, `s`, and `c` on adjacent
		// lines while the very keypress that resolves them is pending.
		return prioritizedFooterLines(listJumpChordTokens(m), footerHintIndent, m.width, footerHintMaxLines)
	default:
		return prioritizedFooterLines(listFooterTokens(m, position), footerHintIndent, m.width, footerHintMaxLines)
	}
}

// listJumpChordTokens is the wide spelling of the bracket chord: each target
// named in full, capped at the same two rows the palette gets.
//
// The heading rides on the first chord rather than standing as its own token,
// because "jump next:" is the sentence the letters complete and a separator
// between them would read as a third target. Only the heading and the escape
// hatch are essential: a chord the operator cannot cancel is a trap, while a
// target they cannot see is a key they will not press.
func listJumpChordTokens(m Model) []prioritizedFooterToken {
	arrow := "next"
	if m.pendingJumpDir == -1 {
		arrow = "prev"
	}
	target := func(key, tail string) prioritizedFooterToken {
		return prioritizedFooterToken{
			token:    footerCustomBindingToken(key, "", tail, styleFooter),
			priority: footerPriorityPrimary,
		}
	}
	heading := styleFooter.Render(fmt.Sprintf("jump %s: ", arrow)) +
		renderFooterBindingWithLabel("p", "", ")perm", styleFooter)
	return []prioritizedFooterToken{
		{token: footerRenderedToken(heading), priority: footerPriorityEssential},
		target("e", ")fail"),
		target("a", ")active"),
		target("q", ")question"),
		target("s", ")stop"),
		target("c", ")compact"),
		target("b", ")sub"),
		target("]]/[[", ")attention"),
		{
			token:    footerCustomBindingToken("esc", " ", "cancels", styleFooter),
			priority: footerPriorityEssential,
		},
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

// listPositionFragment returns a `start–end of total` indicator (mirroring
// detailFooter) when the filtered list overflows the visible window.
// Returns "" when everything fits or the model has not yet been resized.
func listPositionFragment(m Model) string {
	return listPositionFragmentForVisible(m, listVisibleHeight(m))
}

// listPositionFragmentForVisible reports the position for an explicit list-pane
// height. The height is a LINE budget, not an item count, and the two are the
// same number only in a view that draws no chrome between its rows: in a
// multi-group urgency list the group labels and their separators are spent out
// of the same budget, and the ▼ marker costs one more. Comparing the height to
// the item count therefore concluded that every item fit while the renderer was
// drawing an overflow marker under three of seven of them.
//
// So the range and the overflow decision are both taken from the window the
// renderer actually emits. Suppression follows from that window rather than from
// a separate test: the fragment is dropped only when the first item is the top
// of the list and the last item is the bottom of it.
func listPositionFragmentForVisible(m Model, height int) string {
	// Counted in items, not sessions: the window is in item indices, so the
	// denominator has to be the same unit or the indicator drifts the moment a
	// line stands for more than one session.
	total := m.listItemCount()
	if total == 0 || m.height == 0 {
		return ""
	}
	start, end := dashboardScrollInput(&m, listShowsGroupHeadings(&m)).VisibleRange(m.listOffset, height)
	if start <= 0 && end >= total {
		return ""
	}
	if end <= start {
		// A budget too small for even one item still has a cursor parked on one,
		// and "4–3 of 7" is not a range. Report the single item the window is
		// pinned to rather than an empty one.
		end = start + 1
	}
	return fmt.Sprintf("%d–%d of %d", start+1, end, total)
}

// listFooterBudgetLineCount measures the reflowed ModeList footer using the
// same builders as renderFooter. Position text depends on the list height,
// while list height depends on footer rows, so iterate the small calculation
// to a stable row count rather than falling back to a hard-coded budget.
func listFooterBudgetLineCount(m Model) int {
	// The inspector no longer suppresses the footer, so there is no early return
	// here and there must not be one: this count is a term in the body-row floor
	// the inspector's own fit is judged against, and a footer that asked whether
	// the inspector fits would be asking itself.
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
	return !m.railFocused && m.hasSelectedSession()
}

// listFooterCandidate is one thing the regular and spacious ModeList footer may
// draw. Most candidates name a key, but two do not: the position fragment and
// the cursor row's state gloss describe the list rather than binding anything,
// and they compete for the same two rows against the same ladder as the keys.
// Declaring all of them in one ordered list is what makes truncation a single
// decision instead of two blocks racing each other for the space left over.
//
// The list stays authoritative when the budget omits an entry. Generated help is
// built from these same declarations, so the `? more` marker the drop appends
// always points at a keymap that is complete.
type listFooterCandidate struct {
	// footerEntry carries the canonical key, label, section, availability, and
	// priority band. A metadata candidate leaves the key empty, has no
	// availability, and never reaches generated help.
	footerEntry
	// keyFor and labelFor respell a binding for the current model without moving
	// it: enter focuses a session and folds a project summary. Generated help
	// documents the canonical spelling, which is the one true in every state.
	keyFor   func(Model) string
	labelFor func(Model) string
	// textFor renders a metadata candidate's own styled text, returning "" when
	// there is nothing to say. Metadata is never dimmed — it is either a fact
	// about the list or absent.
	textFor func(m Model, position string) string
	// shownFn omits a binding that does not exist in the current state at all:
	// the chip controls name no key until a filter is active. Generated help
	// still documents them, because the overlay is where a user who has never
	// filtered finds out the state exists.
	shownFn func(Model) bool
}

func listBinding(key, label, section string, priority int, avail func(Model) bool) listFooterCandidate {
	return listFooterCandidate{footerEntry: footerEntry{key, label, section, avail, priority}}
}

// listCommandBinding is a footer entry whose key, label, and availability all
// come from the dashboard binding table rather than being restated here. Only
// the section and the priority band are the footer's own business: they say
// where the entry is drawn and what it costs, which is a layout decision the
// binding has no opinion about.
//
// The availability is the command's own — the same question the palette asks —
// so a key rendered live is a key that will do something and a dim one will not.
// The exception is a binding the key runs unchecked: that key always acts, and
// the command reports its own refusal to the operator, so dimming it in the
// footer would contradict what pressing it does.
func listCommandBinding(bindings commands.BindingSet, id commands.ID, section string, priority int) listFooterCandidate {
	binding, ok := bindings.Of(id)
	if !ok {
		// Unreachable while the table is the only source of dashboard keys; a
		// footer entry for a command nothing binds would draw an empty key.
		return listBinding("", "", section, priority, footerNever)
	}
	avail := dashboardCommandAvailable(id)
	if binding.Gate == commands.GateUnchecked {
		avail = footerAlways
	}
	return listBinding(binding.Key(), binding.Hint, section, priority, avail)
}

// availableWhen overrides the availability a command-backed entry derives, for
// the two footer keys that mean more on the dashboard than the command they
// dispatch. It exists so the override has to be written down beside a reason
// rather than achieved by quietly declaring the entry as a literal.
func (c listFooterCandidate) availableWhen(fn func(Model) bool) listFooterCandidate {
	c.availFn = fn
	return c
}

func listMetadata(priority int, text func(m Model, position string) string) listFooterCandidate {
	return listFooterCandidate{footerEntry: footerEntry{priority: priority}, textFor: text}
}

func (c listFooterCandidate) respelled(keyFor, labelFor func(Model) string) listFooterCandidate {
	c.keyFor, c.labelFor = keyFor, labelFor
	return c
}

func (c listFooterCandidate) shownWhen(fn func(Model) bool) listFooterCandidate {
	c.shownFn = fn
	return c
}

// listFooterPalette is the complete, fixed-order ModeList footer palette. Order
// is invariant: a candidate never moves between states, and the only thing the
// cursor row changes is whether a key renders available or dim.
//
// The priority band is what the two-row budget spends: essentials survive any
// width, and everything below is dropped from the bottom of the declaration up,
// so the row is always a prefix of this list plus `? more`. Nothing is lost by
// dropping — every candidate here is documented in the generated Dashboard help
// section, and most are also reachable from the command palette.
// It is a function rather than a package variable because the command-backed
// entries read the catalog, and the catalog's preview gate measures this very
// footer. As a variable that would be an initialization cycle; as a function it
// is just the ordinary recursion the budget already resolves.
func listFooterPalette() []listFooterCandidate {
	// Read once and threaded through: the table is rebuilt per call, and asking
	// it for each of twenty entries would rebuild it twenty times per measured
	// frame.
	bindings := dashboardBindings()
	return []listFooterCandidate{
		// Essential: the six commands without which the dashboard cannot be driven
		// at all — move the cursor, act on what it holds, reach every other command,
		// and find out what those are. Dropping any of them would leave a row that
		// cannot lead anywhere, so none of them ever drop.
		listBinding("j/k", "up/down", footerSectionApp, footerPriorityEssential, footerAlways),
		// enter is not a row action even though it usually acts on one: on a project
		// summary it folds and on the rail it applies a scope, neither of which is
		// FocusSession. Its key and label come from the binding, but its
		// availability cannot, because the two other things it does are available
		// exactly when focusing is not.
		listCommandBinding(bindings, commands.FocusSession, footerSectionApp, footerPriorityEssential).
			availableWhen(footerAlways).
			respelled(listEnterKey, listEnterLabel),
		listBinding("a", "actions", footerSectionRow, footerPriorityEssential, hasRow),
		listBinding(":", "commands", footerSectionApp, footerPriorityEssential, footerAlways),
		listCommandBinding(bindings, commands.FilterSessions, footerSectionApp, footerPriorityEssential),
		listCommandBinding(bindings, commands.ShowKeyboardHelp, footerSectionApp, footerPriorityEssential),

		// Primary: where the cursor sits, what is filtering the list, and the rest of
		// the ways to move or leave. The position fragment leads the band — and so
		// outlives every key in it — because a list that is hiding rows and will not
		// say so is the one thing no keypress recovers.
		listMetadata(footerPriorityPrimary, listPositionText),
		// The command is hidden with no filter active and the entry is withheld in
		// exactly that state, so deriving the availability too would only ever
		// produce a dim key nothing draws.
		listCommandBinding(bindings, commands.ClearFilters, footerSectionApp, footerPriorityPrimary).
			availableWhen(footerAlways).
			shownWhen(listFilterActive),
		listBinding("1-9", "remove chip", footerSectionApp, footerPriorityPrimary, footerAlways).
			shownWhen(listHasChips).respelled(listChipRangeKey, nil),
		// One key, two meanings, and only ever one of them shown. With an
		// inspector on screen tab hands the keyboard between the panes; with none
		// it keeps its older job of walking the chip row. Printing both would put
		// two contradictory glosses for the same key on the same line.
		listBinding("tab", "switch pane", footerSectionApp, footerPriorityPrimary, footerAlways).
			shownWhen(listInspectorShown),
		listBinding("tab", "focus next", footerSectionApp, footerPriorityPrimary, footerAlways).
			shownWhen(listChipFocusAvailable),
		listBinding("]/[", "jump chord", footerSectionApp, footerPriorityPrimary, footerAlways),
		listBinding("}/{", "jump + focus", footerSectionApp, footerPriorityPrimary, footerAlways),
		listCommandBinding(bindings, commands.ToggleInspector, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.ToggleRail, footerSectionApp, footerPriorityPrimary),
		listBinding("←/→", "switch pane", footerSectionApp, footerPriorityPrimary, listRailVisible).
			shownWhen(listUrgencyView),
		// A view action, not a row action: switching the arrangement is available
		// with the cursor on a summary, on a session, and on an empty list alike.
		listCommandBinding(bindings, commands.ToggleListView, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.OpenLauncher, footerSectionLaunch, footerPriorityPrimary),
		listCommandBinding(bindings, commands.Quit, footerSectionApp, footerPriorityPrimary),

		// Secondary: the gloss that says what the cursor row's icon means, then the
		// row actions reached often enough to be worth a key hint.
		listMetadata(footerPrioritySecondary, listGlossText),
		listCommandBinding(bindings, commands.PeekScrollback, footerSectionRow, footerPrioritySecondary),
		listCommandBinding(bindings, commands.ShowTimeline, footerSectionRow, footerPrioritySecondary),
		listCommandBinding(bindings, commands.ClearStatusIcon, footerSectionRow, footerPrioritySecondary),
		listCommandBinding(bindings, commands.RestartSession, footerSectionRow, footerPrioritySecondary),

		// Extra: the tail the narrow footer sheds first. Every one of these is also
		// in the actions overlay or the command palette, both of which the essential
		// band keeps one keypress away.
		listCommandBinding(bindings, commands.OverrideProfile, footerSectionRow, footerPriorityExtra),
		listCommandBinding(bindings, commands.LaunchAnother, footerSectionRow, footerPriorityExtra),
		listCommandBinding(bindings, commands.SearchProject, footerSectionRow, footerPriorityExtra),
		listCommandBinding(bindings, commands.CloseSession, footerSectionRow, footerPriorityExtra),
		listCommandBinding(bindings, commands.OpenDirectoryID, footerSectionRow, footerPriorityExtra),
		listCommandBinding(bindings, commands.OpenEditorID, footerSectionRow, footerPriorityExtra),
		listCommandBinding(bindings, commands.CycleProfileFilter, footerSectionLaunch, footerPriorityExtra),
		listCommandBinding(bindings, commands.SearchAllSessions, footerSectionApp, footerPriorityExtra),
		listCommandBinding(bindings, commands.SearchPlans, footerSectionApp, footerPriorityExtra),
		listCommandBinding(bindings, commands.ResortTabs, footerSectionApp, footerPriorityExtra),
	}
}

func listEnterKey(m Model) string {
	if m.railFocused {
		return "enter"
	}
	if m.summarySelected() {
		return "enter/←/→"
	}
	return "enter"
}

func listEnterLabel(m Model) string {
	if m.railFocused {
		return "apply scope"
	}
	if m.summarySelected() {
		return "fold/unfold"
	}
	return "focus"
}

func listFilterActive(m Model) bool { return !m.activeFilter.IsEmpty() }

func listHasChips(m Model) bool { return len(m.activeFilter.Components()) > 0 }

// listChipFocusAvailable is the narrower half of tab's split meaning: chip
// cycling is only what the key does when there is no second pane to hand the
// keyboard to.
func listChipFocusAvailable(m Model) bool { return listHasChips(m) && !listInspectorShown(m) }

func listUrgencyView(m Model) bool { return m.listView == dashboard.ViewUrgency }

func listRailVisible(m Model) bool {
	return listUrgencyView(m) && dashboardRailWidth(m) > 0
}

// listChipRangeKey names the digits that actually remove something. With a
// single chip the range collapses to "1": "1-1" is a range with one member,
// which reads as a typo rather than as a fact about the filter.
func listChipRangeKey(m Model) string {
	if n := len(m.activeFilter.Components()); n > 1 {
		return fmt.Sprintf("1-%d", n)
	}
	return "1"
}

// listPositionText renders the `start–end of total` fragment the caller already
// measured. It takes the position rather than deriving one because the layout
// budget and the renderer must measure the same footer, and deriving it here
// would ask listVisibleHeight for a height that depends on this very answer.
func listPositionText(_ Model, position string) string {
	if position == "" {
		return ""
	}
	return styleFooter.Render(position)
}

// listGlossText renders the cursor row's state icon and its meaning in the
// state's accent color, so the icon painted in the list is explained in the
// words the legend uses. Rows with no gloss — idle and unknown states — say
// nothing rather than saying "idle", which is what their bare icon already says.
func listGlossText(m Model, _ string) string {
	if m.railFocused {
		return ""
	}
	s, ok := m.selectedSession()
	if !ok {
		return ""
	}
	g := dashboard.StateGloss(s.State)
	if g == "" {
		return ""
	}
	return stateAccentStyle(s.State).Render(layout.PadIconCell(palette.StateIcon(s.State)) + " " + g)
}

// listFooterTokens renders the palette into prioritized tokens for the current
// model, in declaration order. Every candidate is rendered exactly once: as with
// footerHintTokens, availFn is pure over the Model snapshot, so re-running it per
// drop pass could only produce the same answer.
func listFooterTokens(m Model, position string) []prioritizedFooterToken {
	palette := listFooterPalette()
	tokens := make([]prioritizedFooterToken, 0, len(palette))
	for _, c := range palette {
		tok, ok := c.render(m, position)
		if !ok {
			continue
		}
		tokens = append(tokens, prioritizedFooterToken{token: tok, priority: c.priority})
	}
	return tokens
}

// render returns the token this candidate draws for m, or ok=false when it has
// nothing to draw at all. An unavailable binding still draws: keeping the key
// visible in the muted style is what lets the footer's order stay fixed while
// the cursor moves.
func (c listFooterCandidate) render(m Model, position string) (footerToken, bool) {
	if c.textFor != nil {
		text := c.textFor(m, position)
		if text == "" {
			return footerToken{}, false
		}
		return footerRenderedToken(text), true
	}
	if c.shownFn != nil && !c.shownFn(m) {
		return footerToken{}, false
	}
	key, label := c.key, c.label
	if c.keyFor != nil {
		key = c.keyFor(m)
	}
	if c.labelFor != nil {
		label = c.labelFor(m)
	}
	if !c.availFn(m) {
		return footerUnavailableBindingToken(key, label), true
	}
	return footerBindingToken(key, label), true
}

// listFooterBindings projects the palette onto the keybindings it declares,
// dropping the metadata candidates that name no key. It is what generated help
// documents and what parity tests compare against: the footer may omit a
// binding, but this projection never does.
func listFooterBindings() []footerEntry {
	palette := listFooterPalette()
	out := make([]footerEntry, 0, len(palette))
	for _, c := range palette {
		if c.key == "" {
			continue
		}
		out = append(out, c.footerEntry)
	}
	return out
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
