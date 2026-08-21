package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// commandMatchesQuery applies case-insensitive AND matching to whitespace-
// separated query tokens. Each token may match any searchable command field.
func commandMatchesQuery(candidate command, m Model, ctx commandContext, query string) bool {
	parts := []string{
		candidate.displayTitle(m, ctx),
		candidate.description,
		candidate.shortcut,
	}
	parts = append(parts, candidate.aliases...)
	haystack := strings.ToLower(strings.Join(parts, "\n"))
	for _, token := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

// commandPaletteResults returns visible matching commands in their original
// catalog order. Catalog groups are contiguous, so filtering this way also
// preserves stable grouped-result ordering for the renderer.
func commandPaletteResults(catalog []command, m Model, ctx commandContext, query string) []command {
	results := make([]command, 0, len(catalog))
	for _, candidate := range catalog {
		if candidate.isVisible(m, ctx) && commandMatchesQuery(candidate, m, ctx, query) {
			results = append(results, candidate)
		}
	}
	return results
}

// firstAvailableCommandIndex chooses the first executable result. When every
// match is disabled, zero deliberately selects the first disabled row so its
// inline reason can be inspected. Zero is also the harmless empty-list value.
func firstAvailableCommandIndex(results []command, m Model, ctx commandContext) int {
	for i, candidate := range results {
		if candidate.availabilityFor(m, ctx).available {
			return i
		}
	}
	return 0
}

// openCommandPalette captures the current dashboard target and initializes a
// fresh result set for the dashboard ':' binding.
func (m *Model) openCommandPalette() {
	m.clearJumpChord()
	m.commandQuery = ""
	m.commandCursor = 0
	m.commandOffset = 0
	m.commandContext = captureCommandContext(*m)
	m.mode = ModeCommandPalette
	m.recomputeCommandPaletteResults()
}

// closeCommandPalette returns to the dashboard and zeros every transient
// palette field. Callers that execute a command must copy its context first.
func (m *Model) closeCommandPalette() {
	m.mode = ModeList
	m.commandQuery = ""
	m.commandResults = nil
	m.commandCursor = 0
	m.commandOffset = 0
	m.commandContext = commandContext{}
}

// recomputeCommandPaletteResults refreshes matching rows after a query edit
// and restores deterministic selection and scroll state.
func (m *Model) recomputeCommandPaletteResults() {
	m.commandResults = commandPaletteResults(commandCatalog(), *m, m.commandContext, m.commandQuery)
	m.commandCursor = firstAvailableCommandIndex(m.commandResults, *m, m.commandContext)
	m.commandOffset = 0
	scrollCommandPaletteToCursor(m)
}

// handleCommandPaletteKey controls the palette state machine. Printable
// dashboard shortcuts are query text here; only the explicit control keys
// below navigate, close, execute, or quit.
func (m Model) handleCommandPaletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit

	case keyEsc:
		m.closeCommandPalette()
		return m, nil

	case keyEnter:
		if m.commandCursor < 0 || m.commandCursor >= len(m.commandResults) {
			return m, nil
		}
		candidate := m.commandResults[m.commandCursor]
		ctx := m.commandContext
		if !candidate.isVisible(m, ctx) || !candidate.availabilityFor(m, ctx).available {
			return m, nil
		}
		m.closeCommandPalette()
		return candidate.run(m, ctx)

	case keyBackspace:
		runes := []rune(m.commandQuery)
		if len(runes) == 0 {
			return m, nil
		}
		m.commandQuery = string(runes[:len(runes)-1])
		m.recomputeCommandPaletteResults()
		return m, nil

	case keyUp, keyCtrlK, keyCtrlP:
		if m.commandCursor > 0 {
			m.commandCursor--
		}
		scrollCommandPaletteToCursor(&m)
		return m, nil

	case keyDown, keyCtrlJ, keyCtrlN:
		if m.commandCursor+1 < len(m.commandResults) {
			m.commandCursor++
		}
		scrollCommandPaletteToCursor(&m)
		return m, nil

	default:
		if text := msg.Key().Text; text != "" {
			m.commandQuery += text
			m.recomputeCommandPaletteResults()
		}
		return m, nil
	}
}

const (
	commandPaletteNaturalWidth   = 88
	commandPaletteMaxVisibleRows = 18
	commandPaletteCursorGlyph    = "▌"
)

type commandPaletteDisplayRow struct {
	text         string
	commandIndex int
	groupHeader  bool
}

type commandPaletteCommandSpan struct {
	start int
	end   int
}

type commandPaletteLayout struct {
	rows  []commandPaletteDisplayRow
	spans []commandPaletteCommandSpan
}

// commandPaletteBoxWidth implements the shared popup margin contract: the
// palette prefers a roomy desktop width, but always leaves two terminal cells
// on either side and reflows its contents when the viewport is narrower.
func commandPaletteBoxWidth(m Model) int {
	width := commandPaletteNaturalWidth
	if m.width > 0 {
		width = min(width, m.width-4)
	}
	if width < 1 {
		return 1
	}
	return width
}

func commandPaletteContentWidth(m Model) int {
	width := commandPaletteBoxWidth(m) - styleOverlayBox.GetHorizontalFrameSize()
	if width < 1 {
		return 1
	}
	return width
}

// commandPaletteTitleLine keeps every displayed shortcut on the right edge of
// the content area. On very narrow terminals the title gives way first because
// the shortcut is the more compact reference value.
func commandPaletteTitleLine(title, shortcut string, width int) string {
	if width < 1 {
		return ""
	}
	shortcut = displayTruncate(shortcut, width, "…")
	shortcutWidth := displayWidth(shortcut)
	if shortcutWidth >= width {
		return displayPadLeft(shortcut, width)
	}

	titleBudget := width - shortcutWidth - 2
	if titleBudget < 1 {
		return displayPadLeft(shortcut, width)
	}
	title = displayTruncate(title, titleBudget, "…")
	return displayPadRight(title, width-shortcutWidth) + shortcut
}

func commandPaletteWrappedText(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	logical := wrapText(text, width)
	if len(logical) == 0 {
		return []string{""}
	}
	var rows []string
	for _, line := range logical {
		rows = append(rows, displayWrapHard(line, width)...)
	}
	return rows
}

// commandPaletteResultLayout expands logical commands into the exact display
// rows the scroll window consumes. Group headers are emitted only when at
// least one matching result belongs to that group. Command spans exclude the
// group header so selection visibility can keep the full command together.
func commandPaletteResultLayout(m Model, width int) commandPaletteLayout {
	layout := commandPaletteLayout{
		spans: make([]commandPaletteCommandSpan, len(m.commandResults)),
	}
	if len(m.commandResults) == 0 {
		message := "No matching commands."
		if strings.TrimSpace(m.commandQuery) != "" {
			message = fmt.Sprintf("No commands match %q.", m.commandQuery)
		}
		for _, line := range commandPaletteWrappedText(message, width) {
			layout.rows = append(layout.rows, commandPaletteDisplayRow{text: line, commandIndex: -1})
		}
		return layout
	}

	var previousGroup commandGroup
	for i, candidate := range m.commandResults {
		if i == 0 || candidate.group != previousGroup {
			layout.rows = append(layout.rows, commandPaletteDisplayRow{
				text:         string(candidate.group),
				commandIndex: -1,
				groupHeader:  true,
			})
			previousGroup = candidate.group
		}

		start := len(layout.rows)
		title := candidate.displayTitle(m, m.commandContext)
		layout.rows = append(layout.rows, commandPaletteDisplayRow{
			text:         commandPaletteTitleLine(title, candidate.shortcut, width),
			commandIndex: i,
		})

		detail := candidate.description
		availability := candidate.availabilityFor(m, m.commandContext)
		if !availability.available && availability.reason != "" {
			detail += " — unavailable: " + availability.reason
		}
		indent := "  "
		wrapWidth := width - displayWidth(indent)
		if wrapWidth < 1 {
			indent = ""
			wrapWidth = width
		}
		for _, line := range commandPaletteWrappedText(detail, wrapWidth) {
			layout.rows = append(layout.rows, commandPaletteDisplayRow{
				text:         displayTruncate(indent+line, width, "…"),
				commandIndex: i,
			})
		}
		layout.spans[i] = commandPaletteCommandSpan{start: start, end: len(layout.rows)}
	}
	return layout
}

func commandPaletteInnerFit(m Model) int {
	if m.height <= 0 {
		return 0
	}
	fit := rootBodyHeight(m) - styleOverlayBox.GetVerticalFrameSize()
	if fit < 0 {
		return 0
	}
	return fit
}

// commandPaletteChromeRows returns the fixed rows surrounding the scrolling
// result window. Extremely short viewports shed the context line first, then
// the header, so the box still fits while retaining input and close guidance.
func commandPaletteChromeRows(m Model) (top int, hint bool) {
	if m.height <= 0 {
		return 3, true
	}
	fit := commandPaletteInnerFit(m)
	switch {
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

func commandPaletteVisibleHeight(m Model, total int) int {
	if total <= 0 {
		return 0
	}
	visible := commandPaletteMaxVisibleRows
	if m.height > 0 {
		top, hint := commandPaletteChromeRows(m)
		chrome := top
		if hint {
			chrome++
		}
		visible = commandPaletteInnerFit(m) - chrome
	}
	if visible < 0 {
		visible = 0
	}
	if visible > total {
		visible = total
	}
	return visible
}

func commandPaletteScrollBounds(m Model) (layout commandPaletteLayout, visible int) {
	layout = commandPaletteResultLayout(m, commandPaletteContentWidth(m))
	visible = commandPaletteVisibleHeight(m, len(layout.rows))
	return layout, visible
}

func clampCommandPaletteOffset(m *Model) {
	if m.mode != ModeCommandPalette {
		return
	}
	layout, visible := commandPaletteScrollBounds(*m)
	maxOffset := len(layout.rows) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	m.commandOffset = clamp(m.commandOffset, 0, maxOffset)
}

// scrollCommandPaletteToCursor windows wrapped display rows, not command
// indices. When the viewport can hold the selected command, both its title and
// every wrapped description/reason row remain visible together.
func scrollCommandPaletteToCursor(m *Model) {
	if m.mode != ModeCommandPalette {
		return
	}
	layout, visible := commandPaletteScrollBounds(*m)
	if len(layout.rows) == 0 || visible <= 0 || m.commandCursor < 0 || m.commandCursor >= len(layout.spans) {
		m.commandOffset = 0
		return
	}

	span := layout.spans[m.commandCursor]
	start := span.start
	if start > 0 && layout.rows[start-1].groupHeader && span.end-(start-1) <= visible {
		start--
	}
	if span.end-span.start > visible {
		// The terminal is shorter than one fully wrapped command. Show the
		// selected title first; resizing taller will reveal the whole span.
		m.commandOffset = span.start
	} else {
		if start < m.commandOffset {
			m.commandOffset = start
		}
		if span.end > m.commandOffset+visible {
			m.commandOffset = span.end - visible
		}
	}
	clampCommandPaletteOffset(m)
}

func commandPaletteContextLine(m Model) string {
	if !m.commandContext.hasTarget {
		return "Session: none · session commands are disabled"
	}
	title := runningSessionDisplayTitle(m.commandContext.target)
	profile := m.commandContext.target.EffectiveProfile()
	if profile == "" {
		return "Session: " + title
	}
	return "Session: " + title + " · " + profile
}

func commandPaletteInputLine(m Model, width int) string {
	return displayTruncateLeft(":"+m.commandQuery+commandPaletteCursorGlyph, width, "…")
}

func commandPaletteHintLine(width int, above, below bool) string {
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
	middleWidth := width - displayWidth(left) - displayWidth(right)
	if middleWidth < 0 {
		middleWidth = 0
	}
	middle := displayTruncate("↑/↓ move · enter run · esc close", middleWidth, "…")
	return left + displayPadRight(middle, middleWidth) + right
}

func renderCommandPaletteRow(m Model, row commandPaletteDisplayRow, width int) string {
	text := displayTruncate(row.text, width, "…")
	if row.groupHeader {
		return styleHeader.Render(text)
	}
	if row.commandIndex == m.commandCursor && row.commandIndex >= 0 {
		return renderSelectedRow(displayPadRight(text, width))
	}
	if row.commandIndex >= 0 {
		availability := m.commandResults[row.commandIndex].availabilityFor(m, m.commandContext)
		if !availability.available {
			return styleDim.Render(text)
		}
		return text
	}
	return styleDim.Render(text)
}

// renderCommandPalette renders the modal interior. Results are already
// display-row wrapped before slicing, so no selected command is accidentally
// split by command-index-based scrolling.
func renderCommandPalette(m Model) string {
	width := commandPaletteContentWidth(m)
	layout, visible := commandPaletteScrollBounds(m)
	maxOffset := len(layout.rows) - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := clamp(m.commandOffset, 0, maxOffset)
	end := min(offset+visible, len(layout.rows))

	topRows, showHint := commandPaletteChromeRows(m)
	top := []string{
		styleHeader.Render(displayTruncate("Command Palette", width, "…")),
		styleDim.Render(displayTruncate(commandPaletteContextLine(m), width, "…")),
		styleSearch.Render(commandPaletteInputLine(m, width)),
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
	for _, row := range layout.rows[offset:end] {
		lines = append(lines, renderCommandPaletteRow(m, row, width))
	}
	if showHint {
		lines = append(lines, styleDim.Render(commandPaletteHintLine(width, offset > 0, end < len(layout.rows))))
	}
	return strings.Join(lines, "\n")
}

// renderCommandPaletteBox is the production bounded-modal renderer registered
// with both root composition and the UI-test modal-fit detector.
func renderCommandPaletteBox(m Model) string {
	return styleOverlayBox.Width(commandPaletteBoxWidth(m)).Render(renderCommandPalette(m))
}
