package tui

import "strings"

// helpChrome is the number of vertical lines the help overlay reserves
// outside the scrollable content window: title (1) + blank after title (1)
// + scroll-up indicator slot (1) + scroll-down indicator slot (1) + footer
// reserved by render() (1). ModeHelp does not surface toasts, so they do
// not reserve space.
const helpChrome = 5

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
	total, visible := helpScrollBounds(*m)
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

// helpScrollBounds returns the total number of scrollable content lines
// and the number of those lines visible at the model's current height.
// When the model has no height yet (pre-WindowSizeMsg) the visible window
// equals the total so scroll keys become no-ops naturally.
func helpScrollBounds(m Model) (total, visible int) {
	total = len(helpContentLines())
	if m.height == 0 {
		return total, total
	}
	visible = max(helpMinVisible, min(total, m.height-helpChrome))
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
	_, visible := helpScrollBounds(m)
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
