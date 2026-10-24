package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// helpMinVisible is the floor on the visible content window. Even on
// extremely short terminals we always render at least this many content
// lines so the overlay stays useful.
const helpMinVisible = 3

// openHelp raises the shortcut overlay from returnMode. Every mode that binds
// `?` goes through here so handleHelpKey has a return target and the footer's
// `? more` truncation marker always points at a live key.
//
// It opens at the section for the view it was raised from: the Plans keys are
// the eleventh section, and an overlay that always starts at the dashboard
// made `?` from Plans a scroll before it was an answer. The dashboard itself
// has no entry here because its section already leads.
func (m *Model) openHelp(returnMode Mode) {
	m.helpReturnMode = returnMode
	m.mode = ModeHelp
	m.helpOffset = helpSectionOffset(*m, helpSectionFor[returnMode])
}

// helpSectionFor names the help section each view's `?` lands on.
var helpSectionFor = map[Mode]string{
	ModePicker:        "Launch / open",
	ModeSessionSearch: "Past Sessions",
	ModeSessionView:   "Past Sessions",
	ModePlanSearch:    "Plans",
	ModePlanView:      "Plans",
	ModeWorktrees:     "Worktrees",
	ModeASLRuns:       "Runs",
	ModeDetail:        "Session detail",
}

// helpSectionOffset is the scroll offset, in the same wrapped display rows the
// overlay windows over, that puts the named section's header at the top —
// clamped so the window stays full, which is what keeps j/k responsive from
// the first press. An unnamed or unknown section opens at the top.
func helpSectionOffset(m Model, section string) int {
	if section == "" || m.height == 0 {
		return 0
	}
	for row, line := range helpWrappedContentLines(m) {
		if ansi.Strip(line) == section {
			total, visible := helpScrollBounds(m, helpVisibleHeight(m))
			if limit := total - visible; row > limit {
				row = limit
			}
			if row < 0 {
				row = 0
			}
			return row
		}
	}
	return 0
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
		"  pgdn      page down (ctrl+f too)",
		"  pgup      page up (ctrl+b too)",
		"  home      go to top",
		"  end       go to bottom",
		"  g/G       aliases for home/end",
		"  ctrl+d/u  half-page down/up in the focused pane",
		"  n         next Needs you session (reveals it; opens nothing)",
		"  N         previous Needs you session",
		"  tab       switch between the queue and the inspector",
		"  shift+tab same switch (two panes are a cycle of length two)",
		"  esc       leave full screen / pane focus, then clear filters",
		"  (n/N move the cursor only — Enter is what opens a terminal)",
	}},
	{"Projects view", []string{
		"  enter     fold / unfold the selected project",
		"  :         then \"Show Projects View\" to group by project",
		"  (projects start expanded; folds last until amx exits)",
		"  (a folded project's members are still reachable with n/N)",
	}},
	{"Project switcher", []string{
		"  p         open the project switcher (header control does the same)",
		"  j/k ↑/↓   move between scopes; g/G jump to the ends",
		"  /         search the scopes by label",
		"  enter     apply the highlighted scope as the project filter",
		"  esc/q/p   close without changing the filter",
		"  mouse     click a scope row to apply it",
		"  (All is always offered; every other active filter is preserved)",
	}},
	{"Health details", []string{
		"  :         then \"Show Health Details\" (no dashboard key of its own)",
		"  j/k ↑/↓   scroll; g/G jump to the ends; ctrl+f/b page",
		"  d         dismiss the configuration notice for this session",
		"  esc/q     close",
		"  (reads the cached checks; it never triggers a health refresh)",
		"  (dismissing the notice never silences a daemon or hooks failure)",
	}},
	// Act on row is generated from the session-action table: see
	// helpActOnRowLines.
	{"Act on row", helpActOnRowLines()},
	// The launcher keeps its own direct keymap: it is a modal surface with no
	// dashboard rows behind it, so its letters collide with nothing. Only the
	// dashboard's keymap shrank, which is why every line after the first is
	// scoped to the launcher. Launching another session from a row is
	// a row action (a, then C), listed under Act on row.
	{"Launch / open", []string{
		"  l         open session launcher (live filter + preview)",
		"  /         filter launcher projects (DSL: @profile, word)",
		"  space     select / deselect the highlighted project (in the launcher)",
		"  enter     launch the selected projects, else the highlighted one",
		"  f         cycle the launch profile (in the launcher)",
		"  C         choose a profile for this launch (in the launcher)",
		"  ctrl+e    edit launch settings in config.toml (in the launcher)",
		"  d         choose a working directory for this launch (in the launcher)",
		"  P         pin / unpin highlighted launcher project (in the launcher)",
		"  p         toggle the launcher's preview pane (in the launcher)",
		"  s         search past sessions for the highlighted project (launcher)",
		"  o         open selected launcher directories (highlighted fallback)",
		"  O         open those directories in the configured editor (launcher)",
	}},
	// Session detail has no dashboard key of its own any more — `d` opened it
	// and is retired — but the view it opens still has a full keymap, and that
	// keymap is not reachable from any footer palette (ModeDetail renders its own
	// hint line). Listing it here is what keeps `?` an accurate description of
	// the mode the reader is about to be in. `[`/`]` are the timeline's phase
	// steps, not the dashboard's retired group jumps; they never collide because
	// the dashboard no longer binds them at all.
	{"Session detail", []string{
		"  :         then \"Show Session Details\" to open it",
		"  m         full session metadata ↔ event list",
		"  t         flat event list ↔ phase timeline",
		"  j/k ↑/↓   scroll; g/G jump to the ends; ctrl+f/b page",
		"  enter     fold / unfold the phase under the cursor (timeline)",
		"  [ / ]     previous / next phase header (timeline)",
		"  z / Z     collapse all / expand all phases (timeline)",
		"  s         show / hide suppressed events (timeline)",
		"  T         show / hide the timestamp column (timeline)",
		"  ?         this help",
		"  esc/q/d   close",
	}},
	{"Filter", []string{
		"  /         filter (DSL: #project, @profile, %harness, !state, word)",
		"  !state    !attention / !active / !idle select one whole group",
		"  p         project switcher — writes the #project component only",
		"  esc       clear every filter component",
		"  mouse     click a chip to remove it",
		"  (/ opens prefilled: backspace edits one component off the end)",
	}},
	{"App", []string{
		"  :         open command palette — every command, searchable",
		fmt.Sprintf("  u         %s: resume or relaunch (%d seconds)", recoverEndedHint, int(undoWindow.Seconds())),
		"  ?         show / hide this help",
		"  i         show / hide the session inspector",
		"  q         quit",
		"  ctrl+c    quit",
		"  s         Past Sessions for the selected row's project",
		"  S         Past Sessions across every project",
		"  t         send text or keys to the selected tmux session",
		"  (Plans, Worktrees, Runs, session detail, the phase timeline,",
		"   health details, resort, and the list arrangement are palette",
		"   entries — search the palette by name)",
	}},
}

// helpActOnRowLines is the Act on row section, generated from the session
// action table (sessionactions.MenuDefinitions) so a mnemonic, label, or
// description edited there is what help says, in the order the menu shows it.
//
// The row commands have no dashboard letters of their own: they are reached
// through the actions menu and the palette, both of which draw each entry with
// its own availability. The keys inside the menu are its own mnemonics rather
// than dashboard bindings, which is why they read "a, then e" rather than "e".
// The menu is selectable as well as typeable, and both are listed because
// neither replaced the other: j/k and enter reach every entry without knowing
// a letter, and the letters run their entry from anywhere in the list. Enter is
// spelled twice — on the dashboard it opens the row, inside the menu it runs
// the selection — so each is scoped by the line it appears on.
func helpActOnRowLines() []string {
	var open sessionactions.Definition
	var mnemonics []string
	for _, def := range sessionactions.MenuDefinitions() {
		if def.Mnemonic.Key == keyEnter {
			open = def
			continue
		}
		mnemonics = append(mnemonics, fmt.Sprintf("  a, then %s %s — %s",
			def.Mnemonic.Display, strings.ToLower(def.MenuLabel), def.MenuDescription))
	}
	lines := []string{
		"  enter     " + helpOpenSessionText(open),
		"  F12       detach from an attached tmux session (it keeps running)",
		"  a         open the actions menu for the selected session",
		"  j/k ↑/↓   move the selection (in the menu)",
		"  enter     run the selected action (in the menu)",
	}
	lines = append(lines, mnemonics...)
	return append(lines,
		"  (restart, restart with another profile, and end session follow the confirmation preference)",
		"  (a locked row draws those three dimmed; a, then L unlocks it)",
		"  (a dimmed entry is still selectable; the menu says why it will not run)",
	)
}

// helpOpenSessionText is what Enter on a session row does, named as the menu
// names it and qualified by backend: the same key attaches a tmux session to
// this terminal and focuses a Kitty session's tab.
func helpOpenSessionText(open sessionactions.Definition) string {
	label := "open session"
	if open.MenuLabel != "" {
		label = strings.ToLower(open.MenuLabel)
	}
	return label + " (attach on tmux, focus tab on Kitty)"
}

// helpFooterMoreLine is the dim footnote shown after the section list. It names
// the marker the capped footers append when their line budget omits a hint, and
// says where the omitted keys went. The marker is a live binding rather than a
// glyph: pressing `?` is what opens this overlay, so the footnote is describing
// the key that was already pressed to read it.
const helpFooterMoreLine = "  ? more    hidden footer keys are all listed above"

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
		{"list", sessionSearchFooterPalette},
		{"viewer", sessionViewFooterPalette},
	}},
	{"Plans", []helpPalette{
		{"list", planSearchFooterPalette},
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

// helpStateIconSection generates the icon→meaning reference from the same
// LegendStates and StateGloss the rows themselves paint from.
//
// This is where the dashboard's legend strip went. The strip was a reference
// rather than a status — it taught the icon column once and then stopped being
// read — but it spent a row above the list on every spacious frame forever, and
// it was suppressed on exactly the narrow and short terminals where an
// unfamiliar glyph is hardest to guess at. Generated here it costs no rows, is
// available at every size, and still cannot drift from what the list draws.
func helpStateIconSection() []string {
	var lines []string
	for _, state := range dashboard.LegendStates {
		gloss := dashboard.StateGloss(state)
		icon := palette.StateIcon(state)
		if gloss == "" || icon == "" {
			continue
		}
		lines = append(lines, "  "+layout.PadRight(layout.PadIconCell(icon), 10)+gloss)
	}
	return lines
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
	// A one-palette section has one scope, so repeating its name after every
	// meaning says nothing. Tab is why this case exists: it is declared twice in
	// the dashboard palette — `switch pane` beside an inspector, `focus next`
	// without one — and the tagged form spelled that "switch pane (dashboard) ·
	// focus next (dashboard)", which names the scope twice and the condition
	// that selects between them not at all.
	if palettes == 1 {
		parts := make([]string, 0, len(meanings))
		for _, m := range meanings {
			parts = append(parts, m.label)
		}
		return strings.Join(parts, " · ")
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
	raw := helpRawContentLines()
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, line.style(line.text))
	}
	return lines
}

// helpLine is one logical help line before styling: the wrapper needs the
// plain text to break it between words, and the styler to paint each piece.
type helpLine struct {
	text  string
	style helpBodyStyler
}

func helpRawContentLines() []helpLine {
	// Dashboard leads: it is the one section guaranteed complete, so a reader
	// sent here by `? more` finds the missing key without scrolling past seven
	// sections first. The static sections below repeat some of these keys where a
	// fuller explanation earns the space.
	lines := appendHelpSection(nil, "Dashboard", helpDashboardSection(), helpKeyBodyLine)
	// The icon reference is the one section whose left column is a glyph the
	// daemon paints rather than one the operator presses.
	lines = appendHelpSection(lines, "State icons", helpStateIconSection(), helpDimBodyLine)
	for _, sec := range helpSections {
		lines = appendHelpSection(lines, sec.name, sec.lines, helpKeyBodyLine)
	}
	for _, sec := range helpPaletteSections {
		lines = appendHelpSection(lines, sec.name, helpPaletteLines(sec.palettes), helpKeyBodyLine)
	}
	lines = append(lines, helpLine{helpFooterMoreLine, helpKeyBodyLine})
	return lines
}

// helpKeyColumnWidth is how many cells the key column of a help body line
// occupies. Every producer in this file lays out to it — the generated palette
// lines, the hand-written static sections, and the icon reference — which is
// what lets appendHelpSection split a line positionally instead of guessing at
// its content.
const helpKeyColumnWidth = 10

// helpBodyStyler paints one body line of a help section.
type helpBodyStyler func(string) string

// helpDimBodyLine is the plain treatment: the whole line recedes. It is what a
// section whose left column is not a key gets, because a key style there would
// invite the reader to press a glyph that binds nothing.
func helpDimBodyLine(line string) string { return styleDim.Render(line) }

// helpKeyBodyLine paints the key column in the footer's key style and leaves the
// meaning dim.
//
// The overlay is the one screen whose entire subject is which key does what, and
// a reader opens it already knowing what they want to do — they are scanning for
// the glyph to press, not reading prose. Dimming the glyph along with the
// sentence made the keymap uniformly quiet in the place the key should be the
// loudest thing on the row, and it was the only surface in the dashboard where a
// key was not teal. Here it is, so a reader who has learned the footer does not
// have to learn a second spelling.
//
// A parenthetical note has no key column and keeps the dim treatment: those
// lines are asides about the keys above them, not bindings of their own. Neither
// does a line indented past the key column, which is how this file spells the
// continuation of the line above — the words that carry on a sentence are not a
// binding just because they happen to land where one would start.
func helpKeyBodyLine(line string) string {
	const indent = "  "
	if !strings.HasPrefix(line, indent) || strings.HasPrefix(line, indent+"(") {
		return helpDimBodyLine(line)
	}
	body := line[len(indent):]
	if strings.HasPrefix(body, " ") {
		return helpDimBodyLine(line)
	}
	key := layout.Truncate(body, helpKeyColumnWidth, "")
	meaning := body[len(key):]
	if strings.TrimSpace(key) == "" || meaning == "" {
		return helpDimBodyLine(line)
	}
	return indent + styleFooterKey.Render(key) + styleDim.Render(meaning)
}

// appendHelpSection appends one header, its body lines painted by style, and
// the trailing blank separator the scroll math counts.
func appendHelpSection(lines []helpLine, name string, body []string, style helpBodyStyler) []helpLine {
	lines = append(lines, helpLine{name, helpHeaderLine})
	for _, line := range body {
		lines = append(lines, helpLine{line, style})
	}
	return append(lines, helpLine{"", helpPlainLine})
}

func helpHeaderLine(line string) string { return styleHeader.Render(line) }
func helpPlainLine(line string) string  { return line }

// helpWrapLine breaks one logical line between words. A key line's
// continuation is indented past the key column, which helpKeyBodyLine already
// reads as the dim continuation of the line above; a parenthetical note
// continues under its opening parenthesis. Only a column too narrow to hold a
// hanging indent falls back to breaking anywhere.
func helpWrapLine(line helpLine, width int) []string {
	if layout.Width(line.text) <= width {
		return []string{line.style(line.text)}
	}
	const indent = "  "
	hang := len(indent) + helpKeyColumnWidth
	if strings.HasPrefix(line.text, indent+"(") {
		hang = len(indent) + 1
	}
	if !strings.HasPrefix(line.text, indent) || hang >= len(line.text) || width-hang < 8 {
		return layout.StyledWrapHard(line.style(line.text), width)
	}
	lead := layout.Truncate(line.text, hang, "")
	rest := line.text[len(lead):]
	if strings.TrimSpace(lead) != "" && !strings.HasSuffix(lead, " ") && !strings.HasPrefix(rest, " ") {
		// The key column runs into the meaning: there is no word boundary at
		// the hang, so a hanging wrap would split the key.
		return layout.StyledWrapHard(line.style(line.text), width)
	}
	pieces := layout.WrapWords(rest, width-hang)
	if len(pieces) == 0 {
		return []string{line.style(line.text)}
	}
	out := []string{line.style(lead + pieces[0])}
	pad := strings.Repeat(" ", hang)
	for _, piece := range pieces[1:] {
		out = append(out, line.style(pad+piece))
	}
	return out
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
	for _, line := range helpRawContentLines() {
		out = append(out, helpWrapLine(line, width)...)
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
