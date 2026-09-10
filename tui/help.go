package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/tui/layout"
)

// helpMinVisible is the floor on the visible content window. Even on
// extremely short terminals we always render at least this many content
// lines so the overlay stays useful.
const helpMinVisible = 3

// openHelp raises the shortcut overlay from returnMode. Every mode that binds
// `?` goes through here so handleHelpKey has a return target and the footer's
// `? more` truncation marker always points at a live key.
func (m *Model) openHelp(returnMode Mode) {
	m.helpOffset = 0
	m.helpReturnMode = returnMode
	m.mode = ModeHelp
}

// helpBaseView renders the view the overlay was raised from so the dimmed
// backdrop matches the mode esc returns to. Help records its return mode
// directly rather than going through activeRootMode, which is why it names the
// mode itself; the dispatch behind it is the shared one.
func helpBaseView(m Model) string {
	return rootBaseViewFor(m, m.helpReturnMode)
}

// helpSections is the static list of dashboard-scoped sections rendered in the
// keyboard shortcut overlay. Sections are ordered by frequency-of-use (Pareto);
// the overlay is scrollable (see ▲/▼ indicators) so taller sections are
// acceptable when grouping is more useful than capping length. Keys that
// overload across modes (e.g. `tab`/`esc`) are listed under the section
// where they're most often reached for; the footer/actions overlay
// disclose mode-specific meanings inline. Past Sessions, Plans, and Worktrees keys are
// not listed here — helpPaletteSections generates them from the footer
// palettes so each of those bindings is declared exactly once.
var helpSections = []struct {
	name  string
	lines []string
}{
	{"Navigate", []string{
		"  j / ↓     move down",
		"  k / ↑     move up",
		"  ctrl+d    half page down",
		"  ctrl+u    half page up",
		"  ctrl+f    page down",
		"  ctrl+b    page up",
		"  pgdn      page down",
		"  pgup      page up",
		"  g         go to top",
		"  G         go to bottom",
		"  }         next attention session + focus tab",
		"  {         previous attention session + focus tab",
		"  tab       focus next filter chip",
		"  esc       clear filter / back out of overlay",
	}},
	{"Bracket jump", []string{
		"  ]]        next attention session",
		"  [[        previous attention session",
		"  ]p / [p   next/prev permission request",
		"  ]e / [e   next/prev failure (error or warning)",
		"  ]a / [a   next/prev active session",
		"  ]q / [q   next/prev awaiting-response (question)",
		"  ]s / [s   next/prev stopped session",
		"  ]c / [c   next/prev compacting session",
		"  ]b / [b   next/prev subagent session",
		"  esc       cancel pending chord",
		"  (attention set configurable via [defaults.jump])",
	}},
	{"Projects view", []string{
		"  v         group by project / back to urgency order",
		"  →         expand the selected project",
		"  ←         collapse it (from a session, selects its project first)",
		"  enter     fold / unfold the selected project",
		"  space     fold / unfold the selected project",
		"  (projects start expanded; folds last until amx exits)",
	}},
	{"Project rail", []string{
		"  b         show / hide the project rail (urgency only)",
		"  ← / →     move focus between rail and session list",
		"  j/k ↑/↓   move the rail cursor",
		"  enter     apply the highlighted row as a project filter",
		"  mouse     click to scope; wheel scrolls the pane under it",
		"  (responsive hiding never clears an active project filter)",
	}},
	{"Act on row", []string{
		"  enter     focus the session's tab (a project folds instead)",
		"  space     focus the session's tab (alternate path)",
		"  a         open actions overlay (lists every key below)",
		"  e         peek scrollback (active rows only)",
		"  r         restart session (confirm)",
		"  x         clear session icon",
		"  X         close session (confirm)",
		"  c         override session profile",
		"  L         lock / unlock session (blocks r/x/c)",
		"  m         mute / unmute session (suppresses notifications)",
	}},
	{"Launch / open", []string{
		"  l         open session launcher (live filter + preview)",
		"  P         pin / unpin highlighted launcher project",
		"  C         launch new session with profile selection",
		"  o         open selected launcher directories (highlighted fallback)",
		"  y         copy session title",
		"  p         toggle preview pane (also active in launcher)",
	}},
	{"Filter", []string{
		"  /         filter (DSL: #project, @profile, %harness, !state, word)",
		"  !state    !attention / !active / !idle select one whole group",
		"  f         cycle profile (dashboard live / launcher default)",
		"  esc       clear filter chips",
		"  1 – 9     remove filter chip at that position",
	}},
	{"App", []string{
		"  :         open command palette",
		"  ?         show / hide this help",
		"  d         show session detail (hook event log)",
		"  S         search past session transcripts",
		"  P         search saved plans (from dashboard)",
		"  W         manage Git worktrees",
		"  A         show agent-session-loop runs",
		"  q         quit",
		"  ctrl+c    quit",
	}},
}

// helpFooterMoreLine is the dim footnote shown after the section list. It names
// the marker the capped footers append when their line budget omits a hint, and
// says where the omitted keys went. The marker is a live binding rather than a
// glyph: pressing `?` is what opens this overlay, so the footnote is describing
// the key that was already pressed to read it.
const helpFooterMoreLine = "  ? more    footer overflow — every omitted key is listed above"

// helpPalette is one mode's footer palette tagged with the scope name the
// overlay uses to disambiguate keys the section's palettes label differently or
// declare in only one of the two modes.
type helpPalette struct {
	scope   string
	entries []footerEntry
}

// helpPaletteSections are the sections generated from the Past Sessions,
// Plans, and Worktrees footer palettes. The palettes are the single source of truth for those
// modes: narrow terminals drop their lowest-priority hints (nav keys first), so
// the overlay is the only place the complete keymap is always visible.
var helpPaletteSections = []struct {
	name     string
	palettes []helpPalette
}{
	{"Past Sessions", []helpPalette{
		{"search", sessionSearchFooterPalette},
		{"viewer", sessionViewFooterPalette},
	}},
	{"Plans", []helpPalette{
		{"search", planSearchFooterPalette},
		{"viewer", planViewFooterPalette},
	}},
	{"Worktrees", []helpPalette{
		{"browser", worktreesFooterPalette},
	}},
	{"Runs", []helpPalette{
		{"runs", aslRunsFooterPalette},
	}},
}

// helpDashboardSection generates the Dashboard keymap from the ModeList footer
// palette. The palette is the single source of truth for it, which is what makes
// this section the authority behind `? more`: the capped footer sheds its lower
// priority bands on a narrow terminal, and this is where the keys it shed are
// guaranteed to still be named.
//
// Only the palette's keybindings reach here. The position fragment and the state
// gloss compete for the same footer space but bind nothing, so listing them in a
// keymap would advertise two keys that do not exist.
func helpDashboardSection() []string {
	return helpPaletteLines([]helpPalette{{"dashboard", listFooterBindings()}})
}

// helpKeyMeaning pairs a palette scope with the label that palette declares for
// a key.
type helpKeyMeaning struct {
	scope string
	label string
}

// helpPaletteLines renders palette entries into helpSections' padded two-column
// layout, one line per key, ordered by the priority band of its first
// declaration so actions precede the guessable navigation keys.
func helpPaletteLines(palettes []helpPalette) []string {
	var keys []string
	first := make(map[string]footerEntry)
	meanings := make(map[string][]helpKeyMeaning)
	for _, palette := range palettes {
		for _, e := range palette.entries {
			if _, ok := first[e.key]; !ok {
				first[e.key] = e
				keys = append(keys, e.key)
			}
			meanings[e.key] = append(meanings[e.key], helpKeyMeaning{palette.scope, e.label})
		}
	}
	sort.SliceStable(keys, func(i, j int) bool {
		return first[keys[i]].priority < first[keys[j]].priority
	})
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("  %-10s%s", key, helpMeaningLabel(meanings[key], len(palettes))))
	}
	return lines
}

// helpMeaningLabel collapses one key's declarations into a single label. Only a
// key that every palette of the section declares with the same label reads as a
// bare label. Anything else keeps each meaning tagged with the scope that
// declares it: palettes that disagree so a viewer-only meaning is never hidden
// behind the search label, and keys bound in just one of the two modes so the
// overlay never advertises a command that does nothing in its sibling.
func helpMeaningLabel(meanings []helpKeyMeaning, palettes int) string {
	same := len(meanings) == palettes
	for _, m := range meanings[1:] {
		if m.label != meanings[0].label {
			same = false
			break
		}
	}
	if same {
		return meanings[0].label
	}
	parts := make([]string, 0, len(meanings))
	for _, m := range meanings {
		parts = append(parts, fmt.Sprintf("%s (%s)", m.label, m.scope))
	}
	return strings.Join(parts, " · ")
}

// helpContentLines builds the flat list of styled content lines (excluding
// the title, blank, and indicator slots) that participate in scrolling.
// Each section header is followed by its entries and a trailing blank
// separator; the overflow footnote is appended at the end.
func helpContentLines() []string {
	// Dashboard leads: it is the one section guaranteed complete, so a reader
	// sent here by `? more` finds the missing key without scrolling past seven
	// sections first. The static sections below repeat some of these keys where a
	// fuller explanation earns the space.
	lines := appendHelpSection(nil, "Dashboard", helpDashboardSection())
	for _, sec := range helpSections {
		lines = appendHelpSection(lines, sec.name, sec.lines)
	}
	for _, sec := range helpPaletteSections {
		lines = appendHelpSection(lines, sec.name, helpPaletteLines(sec.palettes))
	}
	lines = append(lines, styleDim.Render(helpFooterMoreLine))
	return lines
}

// appendHelpSection appends one styled header, its dim body lines, and the
// trailing blank separator the scroll math counts.
func appendHelpSection(lines []string, name string, body []string) []string {
	lines = append(lines, styleHeader.Render(name))
	for _, line := range body {
		lines = append(lines, styleDim.Render(line))
	}
	return append(lines, "")
}

// helpBoxInnerWidth returns the value passed to styleOverlayBox.Width for the
// help overlay: 70 columns, clamped to m.width-4 on narrow terminals and
// floored at 1. renderHelpBox and the content wrapper share it so the scroll
// math counts the same display rows lipgloss ultimately renders.
func helpBoxInnerWidth(m Model) int {
	innerWidth := 70
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return innerWidth
}

// helpContentWidth returns the cell width a help content line may occupy
// before styleOverlayBox would re-wrap it. styleOverlayBox.Width(...) fixes
// the total rendered width (border + Padding(0, 1) included), so the text
// area is that width minus the box's horizontal frame. Wrapping to this width
// guarantees each row renders as a single display line inside the box, keeping
// the scroll math's row count in step with what lipgloss draws.
func helpContentWidth(m Model) int {
	w := helpBoxInnerWidth(m) - styleOverlayBox.GetHorizontalFrameSize()
	if w < 1 {
		w = 1
	}
	return w
}

// helpWrappedContentLines wraps each styled helpContentLines() entry to the
// overlay's content width so total, offset, visible, and the ▲/▼ indicators
// all operate on display rows rather than logical lines. Blank separators are
// preserved as blank rows.
func helpWrappedContentLines(m Model) []string {
	width := helpContentWidth(m)
	var out []string
	for _, line := range helpContentLines() {
		out = append(out, layout.StyledWrapHard(line, width)...)
	}
	return out
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
	// large enough to fit that floor when possible; on shorter terminals,
	// shrink the content window so the boxed popup still fits above the
	// footer and status rows.
	minInner := helpMinVisible + 4
	fit := rootBodyHeight(m) - styleOverlayBox.GetVerticalFrameSize()
	if fit < 0 {
		return 0
	}
	avail := fit - 4 // safety margin
	if avail < minInner {
		if fit < minInner {
			return fit
		}
		return minInner
	}
	if avail > 30 {
		avail = 30
	}
	if avail > fit {
		avail = fit
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
		if v < 0 {
			return 0
		}
		return v
	}
	return v
}

// helpScrollBounds returns the total number of scrollable content lines
// and the number of those lines visible at the supplied content height.
// When the model has no height yet (pre-WindowSizeMsg) the visible window
// equals the total so scroll keys become no-ops naturally.
func helpScrollBounds(m Model, visibleHeight int) (total, visible int) {
	if m.height == 0 {
		// Pre-resize: renderHelp emits every logical line verbatim, so the
		// bounds count logical lines and scroll keys stay no-ops.
		total = len(helpContentLines())
		return total, total
	}
	total = len(helpWrappedContentLines(m))
	if visibleHeight < 0 {
		visibleHeight = 0
	}
	visible = min(total, visibleHeight)
	return total, visible
}

// renderHelp renders the keyboard-shortcut overlay with a manual viewport.
// On short terminals the content is clipped to the visible window and the
// `▲` / `▼` indicators reveal off-screen content; the layout reserves a
// slot for each indicator unconditionally so the overlay does not jitter
// as the user scrolls.
func renderHelp(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Keyboard Shortcuts") + "\n\n")

	if m.height == 0 {
		// Pre-resize: emit every line verbatim so behavior matches the
		// pre-scroll implementation until the first WindowSizeMsg lands.
		sb.WriteString(strings.Join(helpContentLines(), "\n"))
		sb.WriteByte('\n')
		return sb.String()
	}

	// Wrap to display rows before windowing so total/offset/visible and the
	// ▲/▼ indicators count the same rows the overlay box renders.
	lines := helpWrappedContentLines(m)
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
	return styleOverlayBox.Width(helpBoxInnerWidth(m)).Render(strings.TrimRight(renderHelp(m), "\n"))
}
