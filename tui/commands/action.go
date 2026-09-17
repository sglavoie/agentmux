// The per-row session menu: a selectable list of the actions one dashboard row
// offers, banded, with a stable explanation of whatever is under the cursor.
//
// It is a list the operator MOVES THROUGH, not a legend of keys. Enter runs the
// selected entry; the arrow keys and j/k choose it; the immediate letters still
// work and are printed against the right edge. The distinction matters for
// what is drawn: every entry is one line, so the whole menu is visible at once
// and the cursor has somewhere to be, and the explanation that used to hang off
// each row in a wrapped second column now belongs to the selection and is drawn
// once, in a block whose height does not change as the cursor moves.
//
// Like the palette beside it, this file holds shapes and pure rendering. It is
// generic over the host model so the dashboard, and the live session overview
// that session 6 raises the same menu from, project their own state onto one
// input value; it never names a Bubble Tea model and never decides what running
// an entry does.

package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/layout"
)

const (
	// actionsNaturalWidth is the menu's preferred outer width. It is a constant
	// rather than a measurement of the widest row because the menu is now a
	// column of short labels beside a one-cell key: sizing to content would make
	// the box breathe every time an entry's label changed, and the detail block
	// underneath wants a settled measure to wrap against.
	actionsNaturalWidth = 72

	// MenuExecuteKey is the key that runs the SELECTED entry. It is exported
	// because the host's key handler must take it before the immediate-mnemonic
	// scan, and because one action still declares it as its mnemonic: focusing
	// the session is what enter did before the menu had a cursor, and it is the
	// initial selection, so enter continues to do exactly that on an untouched
	// menu. ImmediateKey is what keeps the two from being drawn as if they were
	// separate bindings.
	MenuExecuteKey = "enter"

	// actionsSelectedMarker and actionsRowGutter are the two-cell gutter every
	// row reserves. The marker is redundant beside the selection highlight on a
	// color terminal and is the only selection signal without one.
	actionsSelectedMarker = "▸ "
	actionsRowGutter      = "  "
)

// Action describes one entry in the per-row session menu.
//
// KeyDisplay is what the menu prints for the immediate key; KeyMatch is the
// literal msg.String() value the host's handler compares against. ID is the
// same stable identity the palette files the action under, so a menu entry and
// a palette row can be shown to be the same action rather than two things that
// happen to be worded alike.
//
// Band is the menu band the entry sits in — the label drawn on the rule that
// separates it from the band above. It is a plain string because this package
// holds no session vocabulary: tui/sessionactions names the bands and hands
// them over already spelled.
//
// Like Command, the type parameter is the host model: Availability reads live
// state (last activity, idle threshold, configured project) and Run returns the
// mutated model plus its effect, both as closures the host owns.
type Action[M any] struct {
	ID           ID
	Band         string
	KeyDisplay   string
	KeyMatch     string
	Label        string
	Description  string
	Availability func(M) Availability
	Run          func(M) (M, tea.Cmd)
}

// AvailabilityFor is the safe way to ask: an entry with no rule is available, so
// a newly added action is offered rather than silently dimmed.
func (a Action[M]) AvailabilityFor(m M) Availability {
	if a.Availability == nil {
		return Available()
	}
	return a.Availability(m)
}

// Available is the boolean half of the verdict, kept as a method so the many
// callers that only ask "can this run" read the same rule as the renderer that
// also needs the reason.
func (a Action[M]) Available(m M) bool { return a.AvailabilityFor(m).Available }

// ImmediateKey is the key the menu PRINTS against this entry, which is not
// always the key that runs it. An action whose mnemonic is MenuExecuteKey prints
// nothing: enter runs whatever is selected, so showing it beside one row would
// claim a binding that stops being true the moment the cursor moves. That is the
// collision the selectable menu introduces, and hiding the glyph is where it is
// resolved — the entry keeps its declared mnemonic, and the footer says what
// enter does.
func (a Action[M]) ImmediateKey() string {
	if a.KeyMatch == MenuExecuteKey {
		return ""
	}
	return a.KeyDisplay
}

// ActionsInput is the projection the menu renders from.
//
// Title is the already-resolved session display title and Qualifier is the
// disambiguating context measured across the whole live set — the same pair the
// dashboard queue draws, supplied rather than derived because a collision is a
// property of the set and this value can see one session. Context is the
// secondary identity line (profile, harness, branch, directory).
//
// Cursor indexes Actions, not display rows: a band rule is not selectable, and
// an index into rows would move under the host whenever a band boundary did.
// Offset is the scroll position in display rows, which is what the window
// slices. BodyHeight is the host's own body measurement, charged before the
// value reaches this package.
type ActionsInput[M any] struct {
	Model      M
	Actions    []Action[M]
	Title      string
	Qualifier  string
	Context    string
	Cursor     int
	Offset     int
	Width      int
	BodyHeight int
}

// ActionRow is one display row of the menu window: either an entry or the
// labelled rule that opens a band. ActionIndex is -1 on a rule.
type ActionRow struct {
	Text        string
	ActionIndex int
	BandRule    bool
}

// boxWidth implements the shared popup margin contract: the menu prefers a
// settled width but always leaves two terminal cells on either side and reflows
// its contents when the viewport is narrower.
func (in ActionsInput[M]) boxWidth() int {
	width := actionsNaturalWidth
	if in.Width > 0 {
		width = min(width, in.Width-4)
	}
	if width < 1 {
		return 1
	}
	return width
}

// ContentWidth is the interior the rows, the detail block, and the hint are laid
// out against.
func (in ActionsInput[M]) ContentWidth() int {
	width := in.boxWidth() - styleOverlayBox.GetHorizontalFrameSize()
	if width < 1 {
		return 1
	}
	return width
}

// ClampCursor confines the cursor to the entries that exist. An empty menu
// clamps to zero, which selects nothing and is what SelectedAction reports.
func (in ActionsInput[M]) ClampCursor() int {
	return clamp(in.Cursor, 0, max(len(in.Actions)-1, 0))
}

// MoveCursor steps the cursor by delta entries and returns the clamped result.
// It does not wrap: an operator holding j at the bottom of a twelve-entry menu
// should stay on Close rather than land back on the first entry and run it.
func (in ActionsInput[M]) MoveCursor(delta int) int {
	if len(in.Actions) == 0 {
		return 0
	}
	return clamp(in.ClampCursor()+delta, 0, len(in.Actions)-1)
}

// SelectedAction returns the entry under the cursor. The second result is false
// only for an empty menu.
func (in ActionsInput[M]) SelectedAction() (Action[M], bool) {
	if len(in.Actions) == 0 {
		return Action[M]{}, false
	}
	return in.Actions[in.ClampCursor()], true
}

// SelectedID is the identity under the cursor, or the zero ID on an empty menu.
// The UI-test observation reports this rather than the cursor index so a
// scenario asserts on which action is selected rather than on a row number that
// a reordering would silently redefine.
func (in ActionsInput[M]) SelectedID() ID {
	if action, ok := in.SelectedAction(); ok {
		return action.ID
	}
	return ""
}

// Rows expands the entries into the display rows the scroll window consumes: a
// band rule wherever the band changes, then one row per entry.
//
// No rule is drawn above the first band. The rule exists to separate, and there
// is nothing above the first entry to separate it from; spending a row on it
// would cost the twelve-entry menu its fit at 80x24 to say something the reader
// can already see.
func (in ActionsInput[M]) Rows(width int) []ActionRow {
	rows := make([]ActionRow, 0, len(in.Actions)+2)
	selected := in.ClampCursor()
	band := ""
	for i, action := range in.Actions {
		if i > 0 && action.Band != band && action.Band != "" {
			rows = append(rows, ActionRow{
				Text:        layout.RuleRow(action.Band, width),
				ActionIndex: -1,
				BandRule:    true,
			})
		}
		if action.Band != "" {
			band = action.Band
		}
		rows = append(rows, ActionRow{Text: actionRowText(action, i == selected, width), ActionIndex: i})
	}
	return rows
}

// actionRowText lays out one entry: a two-cell selection gutter, the label, and
// the immediate key against the right edge.
func actionRowText[M any](action Action[M], selected bool, width int) string {
	gutter := actionsRowGutter
	if selected {
		gutter = actionsSelectedMarker
	}
	body := width - layout.Width(gutter)
	if body < 1 {
		return layout.Truncate(gutter, width, "")
	}
	label, shortcut := layout.ShortcutRow(action.Label, action.ImmediateKey(), body)
	return gutter + label + shortcut
}

// actionsChrome is the fixed rows drawn around the scrolling menu window, and
// the order in which a short terminal gives them up.
type actionsChrome struct {
	header  bool
	context bool
	blank   bool
	detail  int
	hint    bool
}

func (c actionsChrome) rows() int {
	total := c.detail
	for _, present := range []bool{c.header, c.context, c.blank, c.hint} {
		if present {
			total++
		}
	}
	return total
}

// shed gives up the next piece and reports whether anything was left to give.
//
// The order is what a menu can least afford to lose, last: the blank separator
// is decoration, the context line repeats what the dashboard row already showed,
// a two-line detail block can say most of what a one-line block does, and the
// header names the session — which the destructive confirmations restate before
// anything is destroyed.
//
// The hint goes last of all. It used to go before the header, which was right
// while the host's own footer said the same keys underneath: losing it then cost
// a duplicate. Neither host draws that row any more — repeating a modal's keys in
// the chrome around it is a second source of truth rather than a second reminder
// — so this line is now the ONLY statement of how to move, run, and leave the
// menu, and a modal whose way out is unstated is worse than one that does not
// name its subject.
func (c *actionsChrome) shed() bool {
	switch {
	case c.blank:
		c.blank = false
	case c.context:
		c.context = false
	case c.detail > 1:
		c.detail--
	case c.detail == 1:
		c.detail = 0
	case c.header:
		c.header = false
	case c.hint:
		c.hint = false
	default:
		return false
	}
	return true
}

func (in ActionsInput[M]) innerFit() int {
	if in.BodyHeight <= 0 {
		return 0
	}
	fit := in.BodyHeight - styleOverlayBox.GetVerticalFrameSize()
	if fit < 0 {
		return 0
	}
	return fit
}

// detailRows is the height of the explanation block, measured from the
// DESCRIPTIONS alone plus one reserved row for an unavailability reason.
//
// It is deliberately not measured from the selected entry, and not from the
// current availability verdicts either. Sizing to the selection would move the
// block every time the cursor moved, which is the jitter the fixed block exists
// to prevent; sizing to the live verdicts would move it when an idle threshold
// elapsed under a menu nobody was touching. Descriptions are static, so this is
// a function of the width and the vocabulary and of nothing that changes while
// the menu is open.
func (in ActionsInput[M]) detailRows() int {
	width := in.ContentWidth()
	longest := 1
	for _, action := range in.Actions {
		if lines := len(layout.WrapWords(action.Description, width)); lines > longest {
			longest = lines
		}
	}
	return min(longest+1, 5)
}

// chromeFor resolves the chrome and the menu window height for a row count.
//
// Two sheds happen here and they mean different things. The first is mandatory:
// chrome is given up until at least one entry can be drawn, because a menu
// showing no entries is not a menu. The second is a courtesy: if the whole list
// would fit without the blank separator, the separator goes rather than the list
// starting to scroll — twelve entries at 80x24 clear the body by exactly that
// one row, and a menu that scrolls at its primary size to preserve a blank line
// has its priorities backwards.
func (in ActionsInput[M]) chromeFor(total int) (actionsChrome, int) {
	chrome := actionsChrome{
		header:  true,
		context: in.Context != "",
		blank:   true,
		detail:  in.detailRows(),
		hint:    true,
	}
	fit := in.innerFit()
	if fit <= 0 {
		return chrome, total
	}
	for chrome.rows()+1 > fit && chrome.shed() {
	}
	if chrome.blank && chrome.rows()+total > fit {
		chrome.blank = false
	}
	visible := clamp(fit-chrome.rows(), 0, total)
	return chrome, visible
}

// ScrollBounds returns the display rows and how many of them fit.
func (in ActionsInput[M]) ScrollBounds() (rows []ActionRow, visible int) {
	rows = in.Rows(in.ContentWidth())
	_, visible = in.chromeFor(len(rows))
	return rows, visible
}

// ClampOffset returns the input's offset confined to the scrollable range.
func (in ActionsInput[M]) ClampOffset() int {
	rows, visible := in.ScrollBounds()
	return clamp(in.Offset, 0, max(len(rows)-visible, 0))
}

// ScrollToCursor windows the display rows so the selected entry is visible, and
// returns the offset the host should store. When the band rule immediately above
// the selection also fits, it is brought along: an entry scrolled to the top
// edge of the window is more legible under the name of its band than alone.
func (in ActionsInput[M]) ScrollToCursor() int {
	rows, visible := in.ScrollBounds()
	if len(rows) == 0 || visible <= 0 {
		return 0
	}
	cursor := in.ClampCursor()
	index := -1
	for i, row := range rows {
		if row.ActionIndex == cursor {
			index = i
			break
		}
	}
	if index < 0 {
		return in.ClampOffset()
	}

	start := index
	if start > 0 && rows[start-1].BandRule && visible > 1 {
		start--
	}
	if start < in.Offset {
		in.Offset = start
	}
	if index >= in.Offset+visible {
		in.Offset = index - visible + 1
	}
	return in.ClampOffset()
}

// PageSize is how far a page key moves the cursor: one windowful, and at least
// one entry so a viewport too short to show a whole page still advances.
func (in ActionsInput[M]) PageSize() int {
	_, visible := in.ScrollBounds()
	return max(visible, 1)
}

// headerLine names the session the menu is acting on, with the disambiguating
// qualifier the dashboard measured across the live set. Two rows called `api`
// offer the same twelve actions; which one they are about is the whole content
// of this line.
func (in ActionsInput[M]) headerLine(width int) string {
	header := "Actions for " + layout.RenderTitleIcon(in.Title)
	if in.Qualifier != "" {
		header += " (" + in.Qualifier + ")"
	}
	return styleHeader.Render(layout.Truncate(header, width, "…"))
}

// detailLines is the explanation of the selected entry: what running it would
// do, and — when it cannot run — why not.
//
// The reason is a separate paragraph rather than a clause appended to the
// description because it is the more urgent of the two: a block clipped for
// height loses its tail, and the tail should be the part the operator already
// knows.
func (in ActionsInput[M]) detailLines(width, height int) []string {
	if height <= 0 {
		return nil
	}
	action, ok := in.SelectedAction()
	if !ok {
		return layout.DetailBlock([]string{"No actions are available for this row."}, width, height)
	}
	paragraphs := []string{action.Description}
	if availability := action.AvailabilityFor(in.Model); !availability.Available {
		reason := availability.Reason
		if reason == "" {
			reason = "this action does not apply to the selected session"
		}
		paragraphs = []string{"Unavailable: " + reason, action.Description}
	}
	return layout.DetailBlock(paragraphs, width, height)
}

func actionsHintLine(width int, above, below bool) string {
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
	middle := joinFooterBindings(" · ",
		renderFooterBinding("↑/↓", "select"),
		renderFooterBinding("⏎", "run"),
		renderFooterBinding("a key", "runs it"),
		renderFooterBinding("esc/q", "close"),
	)
	middle = layout.StyledTruncate(middle, middleWidth, "…")
	if padding := middleWidth - layout.StyledWidth(middle); padding > 0 {
		middle += strings.Repeat(" ", padding)
	}
	return styleFooter.Render(left) + middle + styleFooter.Render(right)
}

func (in ActionsInput[M]) renderRow(row ActionRow) string {
	if row.BandRule {
		return styleDim.Render(row.Text)
	}
	action := in.Actions[row.ActionIndex]
	if row.ActionIndex == in.ClampCursor() {
		return renderSelectedRow(row.Text)
	}
	if !action.Available(in.Model) {
		return styleFooterUnavailable.Render(row.Text)
	}
	return row.Text
}

// RenderActions renders the modal interior.
func RenderActions[M any](in ActionsInput[M]) string {
	width := in.ContentWidth()
	rows := in.Rows(width)
	chrome, visible := in.chromeFor(len(rows))
	offset := clamp(in.Offset, 0, max(len(rows)-visible, 0))
	end := min(offset+visible, len(rows))

	lines := make([]string, 0, chrome.rows()+visible)
	if chrome.header {
		lines = append(lines, in.headerLine(width))
	}
	if chrome.context {
		lines = append(lines, styleDim.Render(layout.Truncate(in.Context, width, "…")))
	}
	for _, row := range rows[offset:end] {
		lines = append(lines, in.renderRow(row))
	}
	if chrome.blank {
		lines = append(lines, "")
	}
	for _, line := range in.detailLines(width, chrome.detail) {
		lines = append(lines, styleDim.Render(layout.PadRight(line, width)))
	}
	if chrome.hint {
		lines = append(lines, actionsHintLine(width, offset > 0, end < len(rows)))
	}
	return strings.Join(lines, "\n")
}

// RenderActionsBox is the production bounded-modal renderer registered with both
// root composition and the UI-test modal-fit detector.
func RenderActionsBox[M any](in ActionsInput[M]) string {
	return styleOverlayBox.Width(in.boxWidth()).Render(RenderActions(in))
}
