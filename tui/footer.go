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

	"github.com/sglavoie/agentmux/tui/chrome"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

func renderFooter(m Model) string {
	var lines []string
	switch m.mode {
	case ModeSearch:
		lines = nonEmptyFooterLines(
			footerLine(m.width, " · ", "filter: @profile", "#project", "%harness", "!state (!attention)", "word", "\\@ \\# \\% \\! literal"),
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "apply"), chrome.Binding("esc", "cancel")),
		)
	case ModeConfirm:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, "   ",
			chrome.BindingWithLabel("[y]", "", "es", styleFooter),
			chrome.BindingWithLabel("[n]", "", "o", styleFooter),
			chrome.Binding("[esc]", "cancel"),
		))
	case ModePicker:
		if m.pickerDirMode {
			lines = nonEmptyFooterLines(
				footerStyledLine(m.width, " · ",
					chrome.Binding("enter", "apply"), chrome.Binding("tab", "complete"),
					chrome.Binding("ctrl+u", "clear"), chrome.Binding("esc", "cancel"),
				),
				footerLine(m.width, " · ", "launch directory", "relative paths start at the project's directory", "empty restores it"),
			)
			break
		}
		if m.pickerSearchMode {
			lines = nonEmptyFooterLines(
				footerStyledLine(m.width, " · ", chrome.Binding("enter", "apply"), chrome.Binding("esc", "cancel")),
				footerStyledLine(m.width, " · ", styleFooter.Render("DSL: @profile, word")),
			)
			break
		}
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ",
				chrome.Binding("space", "toggle"), chrome.Binding("enter", "launch"),
				chrome.Binding("ctrl+e", "settings"),
				chrome.Binding("f", "profile"), chrome.Binding("P", "pin"),
				chrome.Binding("s", "sessions"), chrome.Binding("C", "override"),
				chrome.Binding("d", "directory"),
				chrome.Binding("o", "open dirs"), chrome.Binding("O", "open in editor"),
				chrome.Binding("p", "preview"), chrome.Binding("esc", "clear/back"),
				chrome.Binding("?", "help"),
			),
			footerStyledLine(m.width, " · ", styleFooter.Render("DSL: @profile, word"), chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom")),
		)
	case ModeConfigPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "select"), chrome.Binding("esc/q", "cancel")),
			footerStyledLine(m.width, " · ", chrome.Binding("j/k", "up/down")),
		)
	case ModeLiveSessionPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "focus"), chrome.Binding("esc/q", "cancel")),
			footerStyledLine(m.width, " · ", chrome.Binding("j/k", "up/down")),
		)
	case ModeHelp:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, " · ", chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("esc/?/q", "close")))
	case ModeActions:
		// The menu carries its own hint line — select, run, the letters, and how
		// to close — so this row said the same three things a second time, in a
		// second wording ("back" for the same key the menu called "close"). It is
		// blank for the reason ModeDetail's is: the modal is the thing being read,
		// and the outer chrome repeating it is not a second reminder but a second
		// source of truth. The menu's hint is now the only statement of its keys,
		// which is why the shed ladder gives up the header before it.
		lines = nil
	case ModeCommandPalette:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, " · ", chrome.Binding("↑/↓ or ctrl+j/k or ctrl+n/p", "up/down"), chrome.Binding("enter", "run"), chrome.Binding("esc", "close"), chrome.Binding("ctrl+c", "quit")))
	case ModeDetail:
		// Inner box footer already carries the scroll position and detail
		// keymap; the outer hint area stays blank so the dimmed dashboard
		// footer doesn't surface irrelevant row-action keys.
		lines = nil
	case ModeChipPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "select"), chrome.Binding("esc/q", "cancel")),
			footerStyledLine(m.width, " · ", chrome.Binding("/", "search"), chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("ctrl+f/b", "page")),
		)
	case ModeProjectSwitcher:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "apply scope"), chrome.Binding("esc/q/p", "cancel")),
			footerStyledLine(m.width, " · ", chrome.Binding("/", "search"), chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("ctrl+f/b", "page")),
		)
	case ModeFeedback:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("tab", "next notice"), chrome.Binding("j/k", "scroll"), chrome.Binding("enter", "inspect"), chrome.Binding("u", "recover")),
			footerStyledLine(m.width, " · ", chrome.Binding("y/Y", "copy/all"), chrome.Binding("d/D", "dismiss/all"), chrome.Binding("h", "health"), chrome.Binding("esc/q", "close")),
		)
	case ModeHealth:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("ctrl+f/b", "page")),
			footerStyledLine(m.width, " · ", healthDismissFooterBinding(m), chrome.Binding("esc/q", "close")),
		)
	case ModeSessionSearch:
		if m.sessionSearchMode {
			lines = footerHintLines(m, queryTypingFooterPalette, footerHintMaxLines)
			break
		}
		lines = footerHintLines(m, sessionSearchFooter(m), footerHintMaxLines)
	case ModeSessionView:
		lines = footerHintLines(m, sessionViewFooterPalette, footerHintMaxLines)
	case ModePlanSearch:
		if m.planSearchMode {
			lines = footerHintLines(m, queryTypingFooterPalette, footerHintMaxLines)
			break
		}
		lines = footerHintLines(m, planSearchFooter(m), footerHintMaxLines)
	case ModePlanView:
		lines = footerHintLines(m, planViewFooterPalette, footerHintMaxLines)
	case ModeWorktrees:
		if m.worktreeFilterMode {
			lines = footerHintLines(m, queryTypingFooterPalette, footerHintMaxLines)
			break
		}
		lines = footerHintLines(m, worktreesFooter(m), footerHintMaxLines)
	case ModeASLRuns:
		lines = footerHintLines(m, aslRunsFooterPalette, footerHintMaxLines)
	case ModeASLResponse:
		lines = footerHintLines(m, aslResponseFooterPalette, footerHintMaxLines)
	case ModeSend:
		lines = footerHintLines(m, sendFooterPalette, footerHintMaxLines)
	case ModePlanRun:
		lines = footerHintLines(m, planRunFooterPalette, footerHintMaxLines)
	case ModeMessageDetail:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, " · ", chrome.Binding("j/k", "scroll"), chrome.Binding("ctrl+d/u", "half"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("y", "copy"), chrome.Binding("esc/q/enter", "close")))
	case ModeDeleteChoice:
		lines = nonEmptyFooterLines(footerLineFromTokens(m.width, " · ",
			footerBindingToken("[i]", "index only"),
			footerCustomBindingToken("[f]", " ", "also delete file", styleError),
			footerBindingToken("[esc]", "cancel"),
		))
	default: // ModeList
		position := ""
		if !m.inspectorFullScreenActive() {
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
//
// The dashboard spends exactly one physical row on its footer at every width.
// The palette reflowed to two rows and the compact band spent one on a
// separately declared shortlist; both are gone in favour of a single
// width-budgeted row generated from the one palette, because a footer that
// grows a row is a session the list stops showing, and two hand-maintained
// spellings of "the essentials" drift apart.
// It is also always the SAME row. The bracket chord used to replace the palette
// outright while it was armed, because half its follow-up letters meant
// something else in the ordinary footer; the chord is retired along with the
// modal state that made a second spelling necessary, so there is one row and one
// vocabulary.
func listModeFooterLines(m Model, position string) []string {
	return []string{listFooterRow(m, listFooterTokens(m, position))}
}

// listFooterRow renders the dashboard's single footer row.
//
// Three spellings are tried in order, and the row never mixes them. The
// labelled palette is dropped from the bottom of the priority ladder until it
// fits. The visible help key leads to commands omitted for space. When only
// the essentials remain and they still do not fit with the marker, the marker
// goes: `? help` is on the row already, so losing `? more` costs a hint that
// something dropped, not the way to reach what dropped. When even the labelled
// essentials alone overflow, the same survivors are respelled as bare keys —
// all of them or none, so the operator who learned the row does not have to
// re-read it to find out which half changed wording.
func listFooterRow(m Model, tokens []prioritizedFooterToken) string {
	kept := make([]prioritizedFooterToken, len(tokens))
	copy(kept, tokens)

	fits := func(line string) bool { return m.width <= 0 || layout.StyledWidth(line) <= m.width }
	truncated := false
	for {
		line := footerSingleRow(kept, footerHintIndent, truncated, false)
		if fits(line) {
			return line
		}
		drop := prioritizedFooterDropIndex(kept)
		if drop < 0 {
			break
		}
		kept = append(kept[:drop], kept[drop+1:]...)
		truncated = true
	}
	if line := footerSingleRow(kept, footerHintIndent, false, false); fits(line) {
		return line
	}
	if line := footerSingleRow(kept, footerHintIndent, false, true); fits(line) {
		return line
	}
	// Narrower than the bare-key spelling of the essential band. The band is not
	// uniform at this width: four of its six controls are how an operator moves,
	// acts, reaches every other command, and finds out what those are, and a row
	// that kept any of the other two instead would name a capability while
	// dropping the way to the rest of them. They go last, in declaration order,
	// so the row degrades toward the four rather than toward its own left edge.
	for {
		drop := -1
		for i := len(kept) - 1; i >= 0; i-- {
			if !kept[i].irreducible {
				drop = i
				break
			}
		}
		if drop < 0 {
			break
		}
		kept = append(kept[:drop], kept[drop+1:]...)
		if line := footerSingleRow(kept, footerHintIndent, false, true); fits(line) {
			return line
		}
	}
	// Nothing left to give up. A clamped row still carries the leading keys.
	return layout.StyledTruncate(footerSingleRow(kept, footerHintIndent, false, true), m.width, "…")
}

// footerSingleRow joins tokens onto one physical line, separators included and
// no wrapping anywhere. short selects the bare-key spelling of every token that
// has one; the more marker is suppressed in that spelling because the row's own
// `?` is already the pointer at generated help.
func footerSingleRow(tokens []prioritizedFooterToken, indent string, truncated, short bool) string {
	sep := styleFooter.Render(footerHintSeparator)
	parts := make([]string, 0, len(tokens)+1)
	for _, t := range tokens {
		rendered := t.token.rendered
		if short && t.short.rendered != "" {
			rendered = t.short.rendered
		}
		if rendered == "" {
			continue
		}
		parts = append(parts, rendered)
	}
	if truncated && !short && !strings.Contains(ansi.Strip(strings.Join(parts, " ")), "? help") {
		parts = append(parts, chrome.Binding("?", "more"))
	}
	return indent + strings.Join(parts, sep)
}

// listFooterShortKey is the bare-key spelling of one key. Only a key whose own
// name is wider than the glyph it prints gets respelled; everything else is
// already one or three cells and reads the same either way.
func listFooterShortKey(key string) string {
	if strings.HasPrefix(key, "enter") {
		return "↵"
	}
	return key
}

// footerShortToken renders a bare key in the spelling the fallback row uses,
// keeping an unavailable binding muted exactly as the labelled row does.
func footerShortToken(key string, available bool) footerToken {
	style := styleFooterKey
	if !available {
		style = styleFooterUnavailableKey
	}
	return footerRenderedToken(style.Render(listFooterShortKey(key)))
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
	return ansi.Style{}.BackgroundColor(theme.Color(theme.Footer)).String()
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
//
// The dashboard footer is one row at every width today, so the iteration
// converges on the first pass. It is kept rather than replaced by the constant
// because listModeFooterLines is the authority on how many rows it draws, and a
// hard-coded 1 here would be a second claim about that free to drift from it.
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
		nextPosition := listPositionFragmentForVisible(m, visible)
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

func footerBindingToken(key, description string) footerToken {
	return footerRenderedToken(chrome.Binding(key, description))
}

func footerUnavailableBindingToken(key, description string) footerToken {
	return footerRenderedToken(chrome.UnavailableBinding(key, description))
}

func footerCustomBindingToken(key, gap, label string, labelStyle lipgloss.Style) footerToken {
	return footerRenderedToken(chrome.BindingWithLabel(key, gap, label, labelStyle))
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
	// irreducibleFlag survives the last drop pass, below the essential band. See
	// prioritizedFooterToken.irreducible and listFooterRow.
	irreducibleFlag bool
}

// irreducible marks a candidate the narrowest row keeps. It is only meaningful
// on an essential-band entry: everything above that band is already gone by the
// time the pass that reads it runs.
func (c listFooterCandidate) irreducible() listFooterCandidate {
	c.irreducibleFlag = true
	return c
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

// listFooterPalette keeps navigation in a stable order and reserves space for
// the selected session’s available answer or send action.
//
// The single row spends its budget on essentials before secondary hints.
// Omitted commands remain available through the palette and Dashboard help.
// Every binding here is documented in the generated Dashboard help
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
		// Essential: navigation, the selected session’s next action, and the commands
		// without which the dashboard cannot be driven
		// at all — move the cursor, act on what it holds, reach every other command,
		// and find out what those are. Dropping any of them would leave a row that
		// cannot lead anywhere, so none of them ever drop.
		listBinding("j/k", "up/down", footerSectionApp, footerPriorityEssential, footerAlways).irreducible(),
		// enter is not a row action even though it usually acts on one: on a project
		// summary it folds, which is not FocusSession. Its key and label come from
		// the binding, but its availability cannot, because the other thing it does
		// is available exactly when focusing is not.
		listCommandBinding(bindings, commands.FocusSession, footerSectionApp, footerPriorityEssential).
			availableWhen(footerAlways).
			respelled(nil, listEnterLabel).
			irreducible(),
		listCommandBinding(bindings, commands.SendToSession, footerSectionRow, footerPriorityEssential).
			shownWhen(dashboardCommandAvailable(commands.SendToSession)).
			respelled(nil, listSendLabel),
		listMetadata(footerPriorityEssential, func(m Model, _ string) string {
			row, ok := m.selectedSession()
			if commands.SendAvailability(commands.Context{Target: row, HasTarget: ok}).Reason == commands.ReasonSendASLQuestion {
				return chrome.Binding(": runs ↵", "answer run")
			}
			return ""
		}),
		listBinding("a", "actions", footerSectionRow, footerPriorityEssential, hasRow),
		listBinding(":", "commands", footerSectionApp, footerPriorityEssential, footerAlways).irreducible(),
		listCommandBinding(bindings, commands.FilterSessions, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.ShowKeyboardHelp, footerSectionApp, footerPriorityEssential).irreducible(),

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
		// Needs-you navigation is the only motion key here that can have nothing
		// to move to, so it is the only one that ever dims: on a dashboard where
		// nothing is waiting, `n` says so rather than looking like a key that
		// broke.
		listBinding("n/N", "needs you", footerSectionApp, footerPriorityPrimary, listNeedsYouReachable),
		// Tab names something only while there is a second pane to hand the
		// keyboard to. With one pane on screen the key answers nothing, and a
		// footer entry for it would be advertising a move that cannot happen.
		// A full-screen inspector is one pane as surely as a queue-only dashboard
		// is, so the entry is withheld there too.
		listBinding("tab", "switch pane", footerSectionApp, footerPriorityPrimary, footerAlways).
			shownWhen(listBothPanesShown),
		// The inspector leads the keys in this band because it is the only one
		// whose surface is already on screen: it is on before the first keypress,
		// so the row that names how to turn it off is the only thing on a
		// first-run dashboard that does. Quit follows for the same reason in
		// reverse — a row that cannot say how to leave is the one omission a
		// newcomer cannot recover from by pressing more keys.
		listCommandBinding(bindings, commands.ToggleInspector, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.Quit, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.SwitchProject, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.OpenLauncher, footerSectionLaunch, footerPriorityPrimary),
		listCommandBinding(bindings, commands.SearchAllSessions, footerSectionApp, footerPriorityPrimary),
		listCommandBinding(bindings, commands.SearchProject, footerSectionRow, footerPriorityPrimary),
		// Recovery is live only for the seconds after a confirmed close, so the
		// entry is shown only then rather than drawn dim the rest of the time;
		// it is still declared here, which is what puts `u` in the generated
		// Dashboard help.
		listCommandBinding(bindings, commands.UndoClose, footerSectionApp, footerPrioritySecondary).
			shownWhen(dashboardCommandAvailable(commands.UndoClose)),
	}
}

// listEnterLabel is the one respelling the footer still carries. Enter focuses a
// session on every line but a family summary, where it folds instead; the key is
// the same either way, so only the gloss changes.
//
// It used to respell the KEY as well — `enter/←/→` on a summary — because the
// arrows folded and unfolded separately. They are retired, so there is one key
// and one thing to say about it.
func listEnterLabel(m Model) string {
	if m.summarySelected() {
		return "fold/unfold"
	}
	return "open"
}

func listSendLabel(m Model) string {
	row, _ := m.selectedSession()
	if row.State == "key" || row.State == "question" {
		return "answer"
	}
	return "send prompt"
}

func listFilterActive(m Model) bool { return !m.activeFilter.IsEmpty() }

// listNeedsYouReachable reports whether `n`/`N` has anywhere to go. It asks the
// navigator itself rather than counting rows in the Needs you group, so the
// footer cannot say "nothing is waiting" about a row the keystroke would in fact
// land on — including one folded inside a family, which has no line of its own
// to count.
func listNeedsYouReachable(m Model) bool {
	_, ok := dashboard.FindNextNeedsYou(m.items, m.cursor, +1, m.lastActivity, m.idleThreshold)
	return ok
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

// listFooterTokens renders the palette into prioritized tokens for the current
// model, in declaration order. Every candidate is rendered exactly once: as with
// footerHintTokens, availFn is pure over the Model snapshot, so re-running it per
// drop pass could only produce the same answer.
func listFooterTokens(m Model, position string) []prioritizedFooterToken {
	palette := listFooterPalette()
	if m.items.Len() == 0 {
		bindings := dashboardBindings()
		palette = []listFooterCandidate{
			listCommandBinding(bindings, commands.OpenLauncher, footerSectionLaunch, footerPriorityEssential).irreducible(),
			listCommandBinding(bindings, commands.SearchAllSessions, footerSectionApp, footerPriorityPrimary),
			listBinding(":", "commands", footerSectionApp, footerPriorityEssential, footerAlways).irreducible(),
			listCommandBinding(bindings, commands.ShowKeyboardHelp, footerSectionApp, footerPriorityEssential).irreducible(),
			listCommandBinding(bindings, commands.ClearFilters, footerSectionApp, footerPriorityEssential).availableWhen(footerAlways).shownWhen(listFilterActive),
			listCommandBinding(bindings, commands.Quit, footerSectionApp, footerPriorityPrimary),
		}
	}
	tokens := make([]prioritizedFooterToken, 0, len(palette))
	for _, c := range palette {
		tok, ok := c.render(m, position)
		if !ok {
			continue
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

// render returns the token this candidate draws for m, or ok=false when it has
// nothing to draw at all. An unavailable binding still draws: keeping the key
// visible in the muted style is what lets the footer's order stay fixed while
// the cursor moves.
func (c listFooterCandidate) render(m Model, position string) (prioritizedFooterToken, bool) {
	if c.textFor != nil {
		text := c.textFor(m, position)
		if text == "" {
			return prioritizedFooterToken{}, false
		}
		// Metadata has no bare-key spelling: it names no key, so the short row
		// either carries the same words or drops it like any other token.
		tok := footerRenderedToken(text)
		return prioritizedFooterToken{token: tok, short: tok, priority: c.priority}, true
	}
	if c.shownFn != nil && !c.shownFn(m) {
		return prioritizedFooterToken{}, false
	}
	key, label := c.key, c.label
	if c.keyFor != nil {
		key = c.keyFor(m)
	}
	if c.labelFor != nil {
		label = c.labelFor(m)
	}
	available := c.availFn(m)
	token := footerBindingToken(key, label)
	if !available {
		token = footerUnavailableBindingToken(key, label)
	}
	return prioritizedFooterToken{
		token:       token,
		short:       footerShortToken(key, available),
		irreducible: c.irreducibleFlag,
		priority:    c.priority,
	}, true
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
