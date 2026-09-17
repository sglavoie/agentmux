// The command palette: every dashboard command, matched by typing, grouped, and
// browsable.
//
// It is a list the operator MOVES THROUGH rather than a set of search results
// that happen to be selectable, which is what decides how it is drawn. Every
// command is one line — its title on the left, the dashboard key that also runs
// it against the right edge — so the five destinations at the top of the catalog
// are all on screen the moment the palette opens rather than three wrapped
// descriptions down. The description that used to hang under each row now
// belongs to the selection and is drawn once, in a block whose height does not
// change as the cursor moves.
//
// This is deliberately the same shape as the per-row session menu in action.go
// beside it, down to the two-cell selection gutter, the labelled band rules, and
// the shed order of the chrome: the two surfaces offer overlapping vocabularies
// and an operator should not have to learn them twice. The one divergence is the
// rule above the first group, which the menu omits and the palette draws — see
// Rows.
//
// Like action.go this file holds shapes and pure rendering. It is generic over
// the host model so the root projects its own state onto one input value; it
// never names a Bubble Tea model and never decides what running a command does.

package commands

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

const (
	// paletteNaturalWidth is the palette's preferred outer width. Like the
	// menu's it is a constant rather than a measurement of the widest row: the
	// result set changes on every keystroke, and a box that resized with it
	// would breathe under the operator's hands.
	paletteNaturalWidth = 88

	// paletteMaxVisibleRows caps the result window when the host has not
	// measured a height yet. A palette that filled an unmeasured viewport would
	// paint rows the terminal cannot show.
	paletteMaxVisibleRows = 18

	// paletteMaxDetailRows caps the selected-command explanation. A description
	// long enough to need a sixth line is a description to shorten, not a reason
	// to give the list less room.
	paletteMaxDetailRows = 5

	// PaletteCursorGlyph is the block the query line ends with. It is exported
	// because a test asserting on what the operator typed has to be able to say
	// where the typing stops.
	PaletteCursorGlyph = "▌"

	// paletteSelectedMarker and paletteRowGutter are the two-cell gutter every
	// result row reserves, spelled the same as the menu's so the two surfaces
	// line up. The marker is redundant beside the selection highlight on a color
	// terminal and is the only selection signal without one.
	paletteSelectedMarker = "▸ "
	paletteRowGutter      = "  "
)

// PaletteInput is everything the palette needs to match, measure, scroll, and
// paint itself. The root owns the query, cursor, and offset on its own model
// and projects them here; the leaf never mutates the model, it returns the
// offset the root should store.
//
// Cursor indexes Results, not display rows: a group rule is not selectable, and
// an index into rows would move under the root whenever a group boundary did.
// Offset is the scroll position in display rows, which is what the window
// slices.
//
// BodyHeight is the root's own measurement of the body area, charged before
// the value reaches this package: the palette knows nothing about the header,
// footer, or status bar surrounding it.
type PaletteInput[M any] struct {
	Model      M
	Query      string
	Results    []Command[M]
	Cursor     int
	Offset     int
	Context    Context
	Width      int
	Height     int
	BodyHeight int
}

// PaletteRow is one display row of the result window: either a command or the
// labelled rule that opens a group. CommandIndex is -1 on a rule and on the
// empty-result message.
//
// Group is carried rather than left to be read back out of Text because a rule's
// text is a rendered rule — dashes, a truncation, a width — and a caller asking
// which group a row opens should not have to parse one.
type PaletteRow struct {
	Text         string
	CommandIndex int
	Group        Group
	GroupRule    bool
}

// MatchesQuery applies case-insensitive AND matching to whitespace-separated
// query tokens. Each token may match any searchable command field.
func MatchesQuery[M any](candidate Command[M], m M, ctx Context, query string) bool {
	parts := []string{
		candidate.DisplayTitle(m, ctx),
		candidate.Description,
		candidate.Shortcut,
	}
	parts = append(parts, candidate.Aliases...)
	haystack := strings.ToLower(strings.Join(parts, "\n"))
	for _, token := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

// PaletteResults returns visible matching commands in their original catalog
// order. Catalog groups are contiguous, so filtering this way also preserves
// stable grouped-result ordering for the renderer.
func PaletteResults[M any](catalog []Command[M], m M, ctx Context, query string) []Command[M] {
	results := make([]Command[M], 0, len(catalog))
	for _, candidate := range catalog {
		if candidate.IsVisible(m, ctx) && MatchesQuery(candidate, m, ctx, query) {
			results = append(results, candidate)
		}
	}
	return results
}

// FirstAvailableIndex chooses the first executable result. When every match is
// disabled, zero deliberately selects the first disabled row so its inline
// reason can be inspected. Zero is also the harmless empty-list value.
func FirstAvailableIndex[M any](results []Command[M], m M, ctx Context) int {
	for i, candidate := range results {
		if candidate.AvailabilityFor(m, ctx).Available {
			return i
		}
	}
	return 0
}

// boxWidth implements the shared popup margin contract: the palette prefers a
// roomy desktop width, but always leaves two terminal cells on either side and
// reflows its contents when the viewport is narrower.
func (in PaletteInput[M]) boxWidth() int {
	width := paletteNaturalWidth
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
func (in PaletteInput[M]) ContentWidth() int {
	width := in.boxWidth() - styleOverlayBox.GetHorizontalFrameSize()
	if width < 1 {
		return 1
	}
	return width
}

// ClampCursor confines the cursor to the results that exist. An empty result set
// clamps to zero, which selects nothing and is what SelectedCommand reports.
func (in PaletteInput[M]) ClampCursor() int {
	return clamp(in.Cursor, 0, max(len(in.Results)-1, 0))
}

// MoveCursor steps the cursor by delta results and returns the clamped result.
// It does not wrap: an operator holding a navigation key at the end of the list
// should stay on the last command rather than land back on the first and run it.
func (in PaletteInput[M]) MoveCursor(delta int) int {
	if len(in.Results) == 0 {
		return 0
	}
	return clamp(in.ClampCursor()+delta, 0, len(in.Results)-1)
}

// SelectedCommand returns the result under the cursor. The second result is
// false only for an empty result set.
func (in PaletteInput[M]) SelectedCommand() (Command[M], bool) {
	if len(in.Results) == 0 {
		return Command[M]{}, false
	}
	return in.Results[in.ClampCursor()], true
}

// Rows expands the matching commands into the display rows the scroll window
// consumes: a labelled rule wherever the group changes, then one row per
// command.
//
// Unlike the session menu, a rule IS drawn above the first group. The menu
// always opens on the same band, so naming it would say nothing the reader
// cannot see; a filtered palette can begin in any of the three groups, and a
// leading block of unlabelled rows would leave "search" showing project history
// beside session history with nothing to say which is which. One row is the
// price, and the list scrolls at its natural size either way.
//
// An empty result set yields the wrapped no-match message as unselectable rows,
// so the window has something to show and the caller needs no separate case.
func (in PaletteInput[M]) Rows(width int) []PaletteRow {
	if len(in.Results) == 0 {
		message := "No matching commands."
		if strings.TrimSpace(in.Query) != "" {
			message = fmt.Sprintf("No commands match %q.", in.Query)
		}
		lines := layout.WrapWords(message, width)
		rows := make([]PaletteRow, 0, len(lines))
		for _, line := range lines {
			rows = append(rows, PaletteRow{Text: line, CommandIndex: -1})
		}
		return rows
	}

	rows := make([]PaletteRow, 0, len(in.Results)+3)
	selected := in.ClampCursor()
	var group Group
	for i, candidate := range in.Results {
		if i == 0 || candidate.Group != group {
			rows = append(rows, PaletteRow{
				Text:         layout.RuleRow(string(candidate.Group), width),
				CommandIndex: -1,
				Group:        candidate.Group,
				GroupRule:    true,
			})
			group = candidate.Group
		}
		rows = append(rows, PaletteRow{
			Text:         in.rowText(candidate, i == selected, width),
			CommandIndex: i,
			Group:        candidate.Group,
		})
	}
	return rows
}

// rowText lays out one command: a two-cell selection gutter, the title, and the
// dashboard shortcut against the right edge.
func (in PaletteInput[M]) rowText(candidate Command[M], selected bool, width int) string {
	gutter := paletteRowGutter
	if selected {
		gutter = paletteSelectedMarker
	}
	body := width - layout.Width(gutter)
	if body < 1 {
		return layout.Truncate(gutter, width, "")
	}
	title, shortcut := layout.ShortcutRow(candidate.DisplayTitle(in.Model, in.Context), candidate.Shortcut, body)
	return gutter + title + shortcut
}

// paletteChrome is the fixed rows drawn around the scrolling result window, and
// the order in which a short terminal gives them up.
//
// The query line is not a member: it is what the operator is typing into and the
// palette without it is a list with no way to filter, so it is counted as a
// constant row in rows() and is never shed.
type paletteChrome struct {
	header  bool
	context bool
	blank   bool
	detail  int
	hint    bool
}

func (c paletteChrome) rows() int {
	total := c.detail + 1 // the query line is never shed
	for _, present := range []bool{c.header, c.context, c.blank, c.hint} {
		if present {
			total++
		}
	}
	return total
}

// shed gives up the next piece and reports whether anything was left to give.
//
// The order is what the palette can least afford to lose, last: the blank
// separator is decoration, the context line repeats what the dashboard row
// already showed, a two-line detail block can say most of what a one-line block
// does, the hint is recoverable by pressing esc, and the header is what names
// the surface the query line belongs to.
func (c *paletteChrome) shed() bool {
	switch {
	case c.blank:
		c.blank = false
	case c.context:
		c.context = false
	case c.detail > 1:
		c.detail--
	case c.detail == 1:
		c.detail = 0
	case c.hint:
		c.hint = false
	case c.header:
		c.header = false
	default:
		return false
	}
	return true
}

func (in PaletteInput[M]) innerFit() int {
	if in.Height <= 0 || in.BodyHeight <= 0 {
		return 0
	}
	fit := in.BodyHeight - styleOverlayBox.GetVerticalFrameSize()
	if fit < 0 {
		return 0
	}
	return fit
}

// detailRows is the height of the explanation block, measured from the
// DESCRIPTIONS of the current results plus one reserved row for an
// unavailability reason.
//
// It is deliberately not measured from the selected command, and not from the
// current availability verdicts either. Sizing to the selection would move the
// block every time the cursor moved, which is the jitter the fixed block exists
// to prevent; sizing to the live verdicts would move it when an idle threshold
// elapsed under a palette nobody was touching. It does change when the query
// does, which is the one moment the whole list below it is being replaced
// anyway.
//
// The second bound is what the menu beside it does not need. The menu holds
// twelve entries and the palette holds thirty, and a narrow window wraps every
// description to four or five lines, so an unbounded block would spend a quarter
// of a 40x18 terminal explaining one command while four of the thirty were on
// screen. The explanation is a footnote to the list: below a roomy viewport it
// gives way rather than the list does.
func (in PaletteInput[M]) detailRows(fit int) int {
	width := in.ContentWidth()
	longest := 1
	for _, candidate := range in.Results {
		if lines := len(layout.WrapWords(candidate.Description, width)); lines > longest {
			longest = lines
		}
	}
	rows := min(longest+1, paletteMaxDetailRows)
	if fit > 0 {
		rows = min(rows, max(fit/4, 1))
	}
	return rows
}

// chromeFor resolves the chrome and the result window height for a row count.
//
// Two sheds happen here and they mean different things. The first is mandatory:
// chrome is given up until at least one result can be drawn, because a palette
// showing no results is not a palette. The second is a courtesy: if the whole
// list would fit without the blank separator, the separator goes rather than the
// list starting to scroll.
func (in PaletteInput[M]) chromeFor(total int) (paletteChrome, int) {
	fit := in.innerFit()
	chrome := paletteChrome{
		header:  true,
		context: true,
		blank:   true,
		detail:  in.detailRows(fit),
		hint:    true,
	}
	if fit <= 0 {
		return chrome, min(total, paletteMaxVisibleRows)
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
func (in PaletteInput[M]) ScrollBounds() (rows []PaletteRow, visible int) {
	rows = in.Rows(in.ContentWidth())
	_, visible = in.chromeFor(len(rows))
	return rows, visible
}

// ClampOffset returns the input's offset confined to the scrollable range.
func (in PaletteInput[M]) ClampOffset() int {
	rows, visible := in.ScrollBounds()
	return clamp(in.Offset, 0, max(len(rows)-visible, 0))
}

// ScrollToCursor windows the display rows so the selected command is visible,
// and returns the offset the root should store. When the group rule immediately
// above the selection also fits, it is brought along: a command scrolled to the
// top edge of the window is more legible under the name of its group than alone.
func (in PaletteInput[M]) ScrollToCursor() int {
	rows, visible := in.ScrollBounds()
	if len(rows) == 0 || visible <= 0 {
		return 0
	}
	cursor := in.ClampCursor()
	index := -1
	for i, row := range rows {
		if row.CommandIndex == cursor {
			index = i
			break
		}
	}
	if index < 0 {
		return in.ClampOffset()
	}

	start := index
	if start > 0 && rows[start-1].GroupRule && visible > 1 {
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

func (in PaletteInput[M]) contextLine() string {
	if !in.Context.HasTarget {
		return "Session: none · session commands are disabled"
	}
	title := dashboard.SessionDisplayTitle(in.Context.Target)
	profile := in.Context.Target.EffectiveProfile()
	if profile == "" {
		return "Session: " + title
	}
	return "Session: " + title + " · " + profile
}

func (in PaletteInput[M]) inputLine(width int) string {
	return layout.TruncateLeft(":"+in.Query+PaletteCursorGlyph, width, "…")
}

// detailLines is the explanation of the selected command: what running it would
// do, and — when it cannot run — why not.
//
// The reason is a separate paragraph rather than a clause appended to the
// description because it is the more urgent of the two: a block clipped for
// height loses its tail, and the tail should be the part the operator already
// knows. This is what keeps a disabled command worth selecting.
func (in PaletteInput[M]) detailLines(width, height int) []string {
	if height <= 0 {
		return nil
	}
	candidate, ok := in.SelectedCommand()
	if !ok {
		return layout.DetailBlock([]string{"Nothing matches the query."}, width, height)
	}
	paragraphs := []string{candidate.Description}
	if availability := candidate.AvailabilityFor(in.Model, in.Context); !availability.Available {
		reason := availability.Reason
		if reason == "" {
			reason = "this command does not apply to the selected session"
		}
		paragraphs = []string{"Unavailable: " + reason, candidate.Description}
	}
	return layout.DetailBlock(paragraphs, width, height)
}

func paletteHintLine(width int, above, below bool) string {
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
	middle := joinFooterBindings(" · ", renderFooterBinding("↑/↓", "move"), renderFooterBinding("enter", "run"), renderFooterBinding("esc", "close"))
	middle = layout.StyledTruncate(middle, middleWidth, "…")
	if padding := middleWidth - layout.StyledWidth(middle); padding > 0 {
		middle += strings.Repeat(" ", padding)
	}
	return styleFooter.Render(left) + middle + styleFooter.Render(right)
}

func (in PaletteInput[M]) renderRow(row PaletteRow) string {
	if row.CommandIndex < 0 {
		return styleDim.Render(row.Text)
	}
	if row.CommandIndex == in.ClampCursor() {
		return renderSelectedRow(row.Text)
	}
	if !in.Results[row.CommandIndex].AvailabilityFor(in.Model, in.Context).Available {
		return styleFooterUnavailable.Render(row.Text)
	}
	return row.Text
}

// RenderPalette renders the modal interior.
func RenderPalette[M any](in PaletteInput[M]) string {
	width := in.ContentWidth()
	rows := in.Rows(width)
	chrome, visible := in.chromeFor(len(rows))
	offset := clamp(in.Offset, 0, max(len(rows)-visible, 0))
	end := min(offset+visible, len(rows))

	lines := make([]string, 0, chrome.rows()+visible)
	if chrome.header {
		lines = append(lines, styleHeader.Render(layout.Truncate("Command Palette", width, "…")))
	}
	if chrome.context {
		lines = append(lines, styleDim.Render(layout.Truncate(in.contextLine(), width, "…")))
	}
	lines = append(lines, styleSearch.Render(in.inputLine(width)))
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
		lines = append(lines, paletteHintLine(width, offset > 0, end < len(rows)))
	}
	return strings.Join(lines, "\n")
}

// RenderPaletteBox is the production bounded-modal renderer registered with
// both root composition and the UI-test modal-fit detector.
func RenderPaletteBox[M any](in PaletteInput[M]) string {
	return styleOverlayBox.Width(in.boxWidth()).Render(RenderPalette(in))
}
