package tui

import "strings"

// helpMinVisible is the floor on the visible content window. Even on
// extremely short terminals we always render at least this many content
// lines so the overlay stays useful.
const helpMinVisible = 3

// helpSections is the static list of sections rendered in the keyboard
// shortcut overlay. Sections are ordered by frequency-of-use (Pareto),
// and each section caps at ≤7 entries so the user can chunk the list
// without scanning it linearly. Keys that overload across modes (e.g.
// `tab`/`esc`) are listed under the section where they're most often
// reached for; the footer/actions overlay disclose mode-specific meanings
// inline.
var helpSections = []struct {
	name  string
	lines []string
}{
	{"Navigate", []string{
		"  j / ↓     move down",
		"  k / ↑     move up",
		"  ctrl+d    half page down",
		"  ctrl+u    half page up",
		"  pgdn      page down",
		"  pgup      page up",
		"  g         go to top",
		"  G         go to bottom",
		"  tab       focus next filter chip",
		"  esc       clear filter / back out of overlay",
	}},
	{"Act on row", []string{
		"  enter     focus the session's tab",
		"  space     focus the session's tab (alternate path)",
		"  a         open actions overlay (lists every key below)",
		"  e         peek scrollback (active rows only)",
		"  r         restart session (confirm)",
		"  x         close session (confirm)",
		"  c         override session profile",
		"  C         clear session icon",
		"  L         lock / unlock session (blocks r/x/c)",
		"  m         mute / unmute session (suppresses notifications)",
	}},
	{"Launch / open", []string{
		"  l         open session launcher (live filter + preview)",
		"  o         open session directory in a plain shell tab",
		"  y         copy session title to clipboard",
		"  p         toggle preview pane (also active in launcher)",
	}},
	{"Filter", []string{
		"  any key   type-to-filter (live narrowing without /)",
		"  /         filter (DSL: @profile, !state, word)",
		"  f         filter pre-filled with @ (profile)",
		"  esc       clear type-buffer / clear filter chips",
		"  1 – 9     remove filter chip at that position",
	}},
	{"App", []string{
		"  ?         show / hide this help",
		"  d         show session detail (hook event log)",
		"  S         search past session transcripts",
		"  q         quit",
		"  ctrl+c    quit",
	}},
}

// helpFooterOverflowLine is the dim footnote shown after the section list
// describing the `…` overflow indicator used in the row-action footer.
const helpFooterOverflowLine = "  …         footer overflow — least-frequent row actions (y)"

// helpContentLines builds the flat list of styled content lines (excluding
// the title, blank, and indicator slots) that participate in scrolling.
// Each section header is followed by its entries and a trailing blank
// separator; the overflow footnote is appended at the end.
func helpContentLines() []string {
	var lines []string
	for _, sec := range helpSections {
		lines = append(lines, styleHeader.Render(sec.name))
		for _, line := range sec.lines {
			lines = append(lines, styleDim.Render(line))
		}
		lines = append(lines, "")
	}
	lines = append(lines, styleDim.Render(helpFooterOverflowLine))
	return lines
}

// clampHelpOffset pins m.helpOffset to the [0, maxOffset] range at the
// model's current dimensions. Mirrors clampDetailScroll: called from the
// WindowSizeMsg handler so a viewport shrink doesn't leave a stale offset
// the user has to scroll through before help reacts to j/k.
func clampHelpOffset(m *Model) {
	total, visible := helpScrollBounds(*m, helpVisibleHeight(*m))
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.helpOffset > maxOffset {
		m.helpOffset = maxOffset
	}
	if m.helpOffset < 0 {
		m.helpOffset = 0
	}
}

// helpOverlayInnerHeight returns the inner content height of the help
// overlay box (everything inside the rounded border). The box uses
// Padding(0, 1) so vertical padding is zero; the only chrome it owns is
// the 2 border rows. The remaining 4-row margin keeps the modal floating
// rather than consuming the full terminal even on short screens. A 30-row
// cap prevents very tall terminals from rendering an oversized modal.
func helpOverlayInnerHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	// Inside the box we render: title (1) + blank (1) + ▲ slot (1) +
	// content (≥ helpMinVisible) + ▼ slot (1). Ensure the inner height is
	// large enough to fit that floor even on tiny terminals.
	minInner := helpMinVisible + 4
	avail := m.height - 6 // border (2) + safety margin (4)
	if avail < minInner {
		return minInner
	}
	if avail > 30 {
		avail = 30
	}
	return avail
}

// helpVisibleHeight returns the number of scrollable content lines that
// fit inside the help overlay's inner area (innerHeight minus title (1) +
// blank (1) + ▲ slot (1) + ▼ slot (1) = 4 chrome lines).
func helpVisibleHeight(m Model) int {
	inner := helpOverlayInnerHeight(m)
	if inner <= 0 {
		return 0
	}
	v := inner - 4
	if v < helpMinVisible {
		return helpMinVisible
	}
	return v
}

// helpScrollBounds returns the total number of scrollable content lines
// and the number of those lines visible at the supplied content height.
// When the model has no height yet (pre-WindowSizeMsg) the visible window
// equals the total so scroll keys become no-ops naturally.
func helpScrollBounds(m Model, visibleHeight int) (total, visible int) {
	total = len(helpContentLines())
	if m.height == 0 {
		return total, total
	}
	visible = max(helpMinVisible, min(total, visibleHeight))
	return total, visible
}

// renderHelp renders the keyboard-shortcut overlay with a manual viewport.
// On short terminals the content is clipped to the visible window and the
// `▲` / `▼` indicators reveal off-screen content; the layout reserves a
// slot for each indicator unconditionally so the overlay does not jitter
// as the user scrolls.
func renderHelp(m Model) string {
	lines := helpContentLines()

	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Keyboard Shortcuts") + "\n\n")

	if m.height == 0 {
		// Pre-resize: emit every line verbatim so behavior matches the
		// pre-scroll implementation until the first WindowSizeMsg lands.
		sb.WriteString(strings.Join(lines, "\n"))
		sb.WriteByte('\n')
		return sb.String()
	}

	n := len(lines)
	_, visible := helpScrollBounds(m, helpVisibleHeight(m))
	offset := m.helpOffset
	if offset+visible > n {
		offset = n - visible
	}
	if offset < 0 {
		offset = 0
	}

	if offset > 0 {
		sb.WriteString(styleDim.Render("  ▲"))
	}
	sb.WriteByte('\n')

	end := offset + visible
	if end > n {
		end = n
	}
	for _, line := range lines[offset:end] {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	if end < n {
		sb.WriteString(styleDim.Render("  ▼"))
	}
	sb.WriteByte('\n')

	return sb.String()
}

// renderHelpBox wraps renderHelp in styleOverlayBox so the keyboard-shortcut
// overlay can be composited as a centered modal on top of the dimmed
// dashboard. The inner width is capped at 70 columns and clamped to
// m.width-4 on narrow terminals.
func renderHelpBox(m Model) string {
	innerWidth := 70
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(renderHelp(m), "\n"))
}
