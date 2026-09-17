package commands

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

const (
	paletteNaturalWidth   = 88
	paletteMaxVisibleRows = 18
	PaletteCursorGlyph    = "▌"
)

// PaletteInput is everything the palette needs to match, measure, scroll, and
// paint itself. The root owns the query, cursor, and offset on its own model
// and projects them here; the leaf never mutates the model, it returns the
// offset the root should store.
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

type PaletteRow struct {
	Text         string
	CommandIndex int
	GroupHeader  bool
}

type PaletteSpan struct {
	Start int
	End   int
}

type PaletteLayout struct {
	Rows  []PaletteRow
	Spans []PaletteSpan
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

func (in PaletteInput[M]) ContentWidth() int {
	width := in.boxWidth() - styleOverlayBox.GetHorizontalFrameSize()
	if width < 1 {
		return 1
	}
	return width
}

// paletteTitleLine keeps every displayed shortcut on the right edge of the
// content area. On very narrow terminals the title gives way first because the
// shortcut is the more compact reference value.
func paletteTitleLine(title, shortcut string, width int) string {
	if width < 1 {
		return ""
	}
	shortcut = layout.Truncate(shortcut, width, "…")
	shortcutWidth := layout.Width(shortcut)
	if shortcutWidth >= width {
		return layout.PadLeft(shortcut, width)
	}

	titleBudget := width - shortcutWidth - 2
	if titleBudget < 1 {
		return layout.PadLeft(shortcut, width)
	}
	title = layout.Truncate(title, titleBudget, "…")
	return layout.PadRight(title, width-shortcutWidth) + shortcut
}

func paletteWrappedText(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	logical := layout.WrapWords(text, width)
	if len(logical) == 0 {
		return []string{""}
	}
	var rows []string
	for _, line := range logical {
		rows = append(rows, layout.WrapHard(line, width)...)
	}
	return rows
}

// ResultLayout expands logical commands into the exact display rows the scroll
// window consumes. Group headers are emitted only when at least one matching
// result belongs to that group. Command spans exclude the group header so
// selection visibility can keep the full command together.
func (in PaletteInput[M]) ResultLayout(width int) PaletteLayout {
	palette := PaletteLayout{
		Spans: make([]PaletteSpan, len(in.Results)),
	}
	if len(in.Results) == 0 {
		message := "No matching commands."
		if strings.TrimSpace(in.Query) != "" {
			message = fmt.Sprintf("No commands match %q.", in.Query)
		}
		for _, line := range paletteWrappedText(message, width) {
			palette.Rows = append(palette.Rows, PaletteRow{Text: line, CommandIndex: -1})
		}
		return palette
	}

	var previousGroup Group
	for i, candidate := range in.Results {
		if i == 0 || candidate.Group != previousGroup {
			palette.Rows = append(palette.Rows, PaletteRow{
				Text:         string(candidate.Group),
				CommandIndex: -1,
				GroupHeader:  true,
			})
			previousGroup = candidate.Group
		}

		start := len(palette.Rows)
		title := candidate.DisplayTitle(in.Model, in.Context)
		palette.Rows = append(palette.Rows, PaletteRow{
			Text:         paletteTitleLine(title, candidate.Shortcut, width),
			CommandIndex: i,
		})

		detail := candidate.Description
		availability := candidate.AvailabilityFor(in.Model, in.Context)
		if !availability.Available && availability.Reason != "" {
			detail += " — unavailable: " + availability.Reason
		}
		indent := "  "
		wrapWidth := width - layout.Width(indent)
		if wrapWidth < 1 {
			indent = ""
			wrapWidth = width
		}
		for _, line := range paletteWrappedText(detail, wrapWidth) {
			palette.Rows = append(palette.Rows, PaletteRow{
				Text:         layout.Truncate(indent+line, width, "…"),
				CommandIndex: i,
			})
		}
		palette.Spans[i] = PaletteSpan{Start: start, End: len(palette.Rows)}
	}
	return palette
}

func (in PaletteInput[M]) innerFit() int {
	if in.Height <= 0 {
		return 0
	}
	fit := in.BodyHeight - styleOverlayBox.GetVerticalFrameSize()
	if fit < 0 {
		return 0
	}
	return fit
}

// chromeRows returns the fixed rows surrounding the scrolling result window.
// Extremely short viewports shed the context line first, then the header, so
// the box still fits while retaining input and close guidance.
func (in PaletteInput[M]) chromeRows() (top int, hint bool) {
	if in.Height <= 0 {
		return 3, true
	}
	switch fit := in.innerFit(); {
	case fit >= 4:
		return 3, true
	case fit == 3:
		return 2, true
	case fit == 2:
		return 1, true
	case fit == 1:
		return 1, false
	default:
		return 0, false
	}
}

func (in PaletteInput[M]) visibleHeight(total int) int {
	if total <= 0 {
		return 0
	}
	visible := paletteMaxVisibleRows
	if in.Height > 0 {
		top, hint := in.chromeRows()
		chrome := top
		if hint {
			chrome++
		}
		visible = in.innerFit() - chrome
	}
	if visible < 0 {
		visible = 0
	}
	if visible > total {
		visible = total
	}
	return visible
}

// ScrollBounds returns the wrapped display rows and how many of them fit.
func (in PaletteInput[M]) ScrollBounds() (palette PaletteLayout, visible int) {
	palette = in.ResultLayout(in.ContentWidth())
	visible = in.visibleHeight(len(palette.Rows))
	return palette, visible
}

// ClampOffset returns the input's offset confined to the scrollable range.
func (in PaletteInput[M]) ClampOffset() int {
	palette, visible := in.ScrollBounds()
	maxOffset := len(palette.Rows) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	return clamp(in.Offset, 0, maxOffset)
}

// ScrollToCursor windows wrapped display rows, not command indices. When the
// viewport can hold the selected command, both its title and every wrapped
// description/reason row remain visible together. It returns the offset the
// caller should store.
func (in PaletteInput[M]) ScrollToCursor() int {
	palette, visible := in.ScrollBounds()
	if len(palette.Rows) == 0 || visible <= 0 || in.Cursor < 0 || in.Cursor >= len(palette.Spans) {
		return 0
	}

	span := palette.Spans[in.Cursor]
	start := span.Start
	if start > 0 && palette.Rows[start-1].GroupHeader && span.End-(start-1) <= visible {
		start--
	}
	if span.End-span.Start > visible {
		// The terminal is shorter than one fully wrapped command. Show the
		// selected title first; resizing taller will reveal the whole span.
		in.Offset = span.Start
	} else {
		if start < in.Offset {
			in.Offset = start
		}
		if span.End > in.Offset+visible {
			in.Offset = span.End - visible
		}
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

func paletteHintLine(width int, above, below bool) string {
	if width < 1 {
		return ""
	}
	left := "  "
	right := "  "
	if above {
		left = "▲ "
	}
	if below {
		right = " ▼"
	}
	middleWidth := width - layout.Width(left) - layout.Width(right)
	if middleWidth < 0 {
		middleWidth = 0
	}
	middle := joinFooterBindings(" · ", renderFooterBinding("↑/↓", "move"), renderFooterBinding("enter", "run"), renderFooterBinding("esc", "close"))
	middle = layout.StyledTruncate(middle, middleWidth, "…")
	if padding := middleWidth - layout.StyledWidth(middle); padding > 0 {
		middle += strings.Repeat(" ", padding)
	}
	return styleFooter.Render(left) + middle + styleFooter.Render(right)
}

func (in PaletteInput[M]) renderRow(row PaletteRow, width int) string {
	text := layout.Truncate(row.Text, width, "…")
	if row.GroupHeader {
		return styleHeader.Render(text)
	}
	if row.CommandIndex == in.Cursor && row.CommandIndex >= 0 {
		return renderSelectedRow(layout.PadRight(text, width))
	}
	if row.CommandIndex >= 0 {
		availability := in.Results[row.CommandIndex].AvailabilityFor(in.Model, in.Context)
		if !availability.Available {
			return styleDim.Render(text)
		}
		return text
	}
	return styleDim.Render(text)
}

// RenderPalette renders the modal interior. Results are already display-row
// wrapped before slicing, so no selected command is accidentally split by
// command-index-based scrolling.
func RenderPalette[M any](in PaletteInput[M]) string {
	width := in.ContentWidth()
	palette, visible := in.ScrollBounds()
	maxOffset := len(palette.Rows) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := clamp(in.Offset, 0, maxOffset)
	end := min(offset+visible, len(palette.Rows))

	topRows, showHint := in.chromeRows()
	top := []string{
		styleHeader.Render(layout.Truncate("Command Palette", width, "…")),
		styleDim.Render(layout.Truncate(in.contextLine(), width, "…")),
		styleSearch.Render(in.inputLine(width)),
	}
	switch topRows {
	case 2:
		top = []string{top[0], top[2]}
	case 1:
		top = top[2:]
	case 0:
		top = nil
	}

	lines := make([]string, 0, len(top)+visible+1)
	lines = append(lines, top...)
	for _, row := range palette.Rows[offset:end] {
		lines = append(lines, in.renderRow(row, width))
	}
	if showHint {
		lines = append(lines, paletteHintLine(width, offset > 0, end < len(palette.Rows)))
	}
	return strings.Join(lines, "\n")
}

// RenderPaletteBox is the production bounded-modal renderer registered with
// both root composition and the UI-test modal-fit detector.
func RenderPaletteBox[M any](in PaletteInput[M]) string {
	return styleOverlayBox.Width(in.boxWidth()).Render(RenderPalette(in))
}
