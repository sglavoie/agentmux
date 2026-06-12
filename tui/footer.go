package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/palette"
)

func renderFooter(m Model) string {
	var lines []string
	switch m.mode {
	case ModeSearch:
		lines = nonEmptyFooterLines(
			styleFooter.Render("  filter: @profile · !state · word · \\@/\\! literal"),
			styleFooter.Render("  enter: apply  esc: cancel"),
		)
	case ModeConfirm:
		lines = nonEmptyFooterLines(styleFooter.Render("  [y]es   [n]o   [esc] cancel"))
	case ModePicker:
		lines = nonEmptyFooterLines(
			styleFooter.Render("  space: toggle  enter: launch  s: sessions  C: profile  o: open dir  O: open in editor  p: preview  esc: clear/exit"),
			styleFooter.Render("  DSL: @profile, word  j/k: up/down"),
		)
	case ModeConfigPicker:
		lines = nonEmptyFooterLines(
			styleFooter.Render("  enter: select  esc/q: cancel"),
			styleFooter.Render("  j/k: up/down"),
		)
	case ModeHelp:
		lines = nonEmptyFooterLines(styleFooter.Render("  j/k: up/down  g/G: top/bottom  esc/?/q: close help"))
	case ModeActions:
		lines = nonEmptyFooterLines(styleFooter.Render("  <key>: run action  esc/q: back"))
	case ModeDetail:
		// Inner box footer already carries the scroll position and detail
		// keymap; the outer hint area stays blank so the dimmed dashboard
		// footer doesn't surface irrelevant row-action keys.
		lines = nil
	case ModeChipPicker:
		lines = nonEmptyFooterLines(
			styleFooter.Render("  enter: select  esc/q: cancel"),
			styleFooter.Render("  /: search  j/k: up/down  g/G: top/bottom  pgup/pgdn: page"),
		)
	case ModeSessionSearch:
		lines = nonEmptyFooterLines(
			styleFooter.Render("  / filter · h/p/r chips · enter view · d delete · Y path · esc back"),
			styleFooter.Render("  j/k: up/down  ctrl+d/u: half  ctrl+f/b: page  g/G: top/bottom"),
		)
	case ModeSessionView:
		lines = nonEmptyFooterLines(
			styleFooter.Render("  j/k move · n/N match · y copy · Y path · enter view message · esc back"),
			styleFooter.Render("  ctrl+d/u: half  ctrl+f/b: page  g/G: top/bot"),
		)
	case ModePlanSearch:
		lines = nonEmptyFooterLines(
			styleFooter.Render("  / filter · enter view · o editor · s sort · Y path · esc back"),
			styleFooter.Render("  j/k up/down · g/G top/bot · ctrl+d/u half · ctrl+f/b page"),
		)
	case ModePlanView:
		lines = nonEmptyFooterLines(
			styleFooter.Render("  / search · n/N match · y copy · Y path · o editor · esc back"),
			styleFooter.Render("  j/k scroll · g/G top/bot · ctrl+d/u half · ctrl+f/b page"),
		)
	case ModeMessageDetail:
		lines = nonEmptyFooterLines(styleFooter.Render("  j/k scroll · g/G top/bottom · esc/q/enter close"))
	case ModeDeleteChoice:
		lines = nonEmptyFooterLines(styleFooter.Render("  [i] index only · ") + styleError.Render("[f] also delete file") + styleFooter.Render(" · [esc] cancel"))
	default: // ModeList
		switch {
		case m.pendingJumpDir != 0:
			// Bracket chord pending: show the chord vocabulary instead of
			// the usual nav row so the user can complete or cancel.
			arrow := "next"
			if m.pendingJumpDir == -1 {
				arrow = "prev"
			}
			lines = listFooterLines(m, styleFooter.Render(fmt.Sprintf("  jump %s: p)perm  e)fail  a)active  q)question  s)stop  c)compact  b)sub  ]]/[[)attention  (esc cancels)", arrow)))
		case m.previewEnabled:
			// In preview mode the only binding shown is `p: hide preview`,
			// which is rendered right-aligned in the title row.
			lines = nil
		default:
			navTokens := []string{
				"j/k: up/down",
				"]/[: jump-chord",
				"}/{: nav+focus",
				"enter: focus",
				"a: actions",
				"/: filter",
				"?: help",
				"q: quit",
				"p: preview",
			}
			if !m.activeFilter.IsEmpty() {
				navTokens = append(navTokens, "esc: clear filters")
				if n := len(m.activeFilter.Components()); n > 0 {
					navTokens = append(navTokens,
						fmt.Sprintf("1-%d: remove chip", n),
						"tab: focus next",
					)
				}
			}
			lines = listFooterLines(m, listFooterLine2(m, navTokens))
		}
	}

	var sb strings.Builder
	if len(lines) > 0 {
		sb.WriteString(strings.Join(lines, "\n"))
	}
	return sb.String()
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

func listFooterLines(m Model, selectedLine string) []string {
	lines := []string{
		listFooterLine1(m),
		listFooterGlobalLine(m),
	}
	if selectedLine != "" {
		lines = append(lines, "", selectedLine)
	}
	return lines
}

// listFooterLine2 prefixes the navigation hints with a gloss of the cursor
// row's state icon (e.g. `⚡ active — j/k: up/down …`). The gloss text picks
// up the state's accent color so the meaning of the row icon is reinforced;
// the dash separator and trailing nav stay in the dim footer style. When the
// filtered list overflows the visible window a `start–end of total` position
// fragment is prepended so the user sees where the cursor sits.
//
// navFragments is the ordered list of navigation hints to render; each
// fragment is joined with the standard double-space separator and wrapped to
// fit m.width.
func listFooterLine2(m Model, navFragments []string) string {
	sep := styleFooter.Render("  ")
	sepW := lipgloss.Width(sep)

	frags := navFragments
	if pos := listPositionFragment(m); pos != "" {
		// The position fragment sits before the nav hints with a wider
		// three-space gap so the eye chunks it as a separate group.
		gap := styleFooter.Render("   ")
		gapW := lipgloss.Width(gap)
		tokens := make([]footerToken, 0, len(frags)+1)
		posRendered := styleFooter.Render(pos)
		tokens = append(tokens, footerToken{
			rendered: posRendered,
			width:    lipgloss.Width(posRendered),
			sep:      gap,
			sepWidth: gapW,
		})
		for i, f := range frags {
			rendered := styleFooter.Render(f)
			tok := footerToken{
				rendered: rendered,
				width:    lipgloss.Width(rendered),
			}
			if i < len(frags)-1 {
				tok.sep = sep
				tok.sepWidth = sepW
			}
			tokens = append(tokens, tok)
		}
		firstLead, contIndent := listFooterLine2Leads(m)
		return wrapFooterLine(firstLead, contIndent, tokens, m.width)
	}

	tokens := make([]footerToken, 0, len(frags))
	for i, f := range frags {
		rendered := styleFooter.Render(f)
		tok := footerToken{
			rendered: rendered,
			width:    lipgloss.Width(rendered),
		}
		if i < len(frags)-1 {
			tok.sep = sep
			tok.sepWidth = sepW
		}
		tokens = append(tokens, tok)
	}
	firstLead, contIndent := listFooterLine2Leads(m)
	return wrapFooterLine(firstLead, contIndent, tokens, m.width)
}

// listFooterLine2Leads returns the rendered prefix for the first line and the
// indent used on continuation lines. When the cursor row has a state gloss
// the first line carries the accent-colored "⚡ active —" lead; continuation
// lines fall back to the plain two-space indent so the gloss does not repeat.
func listFooterLine2Leads(m Model) (string, string) {
	indent := "  "
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		s := m.filtered[m.cursor]
		if g := StateGloss(s.State); g != "" {
			icon := palette.StateIcon(s.State)
			glossText := padIconCell(icon) + " " + g
			lead := indent + stateAccentStyle(s.State).Render(glossText) + styleFooter.Render(" — ")
			return lead, indent
		}
	}
	return indent, indent
}

// listPositionFragment returns a `start–end of total` indicator (mirroring
// detailFooter) when the filtered list overflows the visible window.
// Returns "" when everything fits or the model has not yet been resized.
func listPositionFragment(m Model) string {
	total := len(m.filtered)
	if total == 0 || m.height == 0 {
		return ""
	}
	visible := listVisibleHeight(m)
	if visible >= total {
		return ""
	}
	start := m.listOffset + 1
	end := m.listOffset + visible
	if end > total {
		end = total
	}
	if start < 1 {
		start = 1
	}
	return fmt.Sprintf("%d–%d of %d", start, end, total)
}

// footerEntry describes one slot in the stable footer palette. The palette is
// rendered in declaration order regardless of the cursor row's state; only the
// styling of each entry changes (available vs unavailable) so users always see
// every action at the same on-screen position. `section` groups entries onto
// command rows so users can map shortcuts to their semantic bucket (row
// actions, launching, app-wide commands).
type footerEntry struct {
	key     string
	label   string
	section string
	availFn func(Model) bool
}

// Section identifiers for footer command rows.
const (
	footerSectionRow    = "row"
	footerSectionLaunch = "launch"
	footerSectionApp    = "app"
)

// hasRow reports whether a row is selected; any row-targeted action requires a
// cursor sitting on a session.
func hasRow(m Model) bool {
	return len(m.filtered) > 0 && m.cursor < len(m.filtered)
}

// listFooterPalette is the fixed-order set of keys shown in the ModeList footer
// command rows. Order is invariant — entries never move between states. Each
// entry's availability function decides whether to render with the available
// or unavailable style.
var listFooterPalette = []footerEntry{
	{"e", "peek", footerSectionRow, func(m Model) bool {
		return hasRow(m) && m.filtered[m.cursor].State == "bolt"
	}},
	{"T", "timeline", footerSectionRow, hasRow},
	{"r", "restart", footerSectionRow, hasRow},
	{"c", "profile", footerSectionRow, hasRow},
	{"C", "launch", footerSectionRow, hasRow},
	{"s", "project sessions", footerSectionRow, hasRow},
	{"x", "clear", footerSectionRow, func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		return stateGroup(m.filtered[m.cursor], m.lastActivity, m.idleThreshold) == 0
	}},
	{"X", "close", footerSectionRow, hasRow},
	{"o", "open dir", footerSectionRow, func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		s := m.filtered[m.cursor]
		_, ok := findConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject())
		return ok
	}},
	{"O", "open in editor", footerSectionRow, func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		s := m.filtered[m.cursor]
		_, ok := findConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject())
		return ok
	}},
	{"l", "launch", footerSectionLaunch, func(Model) bool { return true }},
	{"S", "sessions", footerSectionApp, func(Model) bool { return true }},
	{"P", "plans", footerSectionApp, func(Model) bool { return true }},
	{"R", "resort", footerSectionApp, func(Model) bool { return true }},
}

func listFooterLine1(m Model) string {
	return listFooterSectionsLine(m, []string{footerSectionRow})
}

func listFooterGlobalLine(m Model) string {
	return listFooterSectionsLine(m, []string{footerSectionLaunch, footerSectionApp})
}

func listFooterSectionsLine(m Model, sections []string) string {
	const indent = "  "
	innerSep := styleFooter.Render(" · ")
	innerSepW := lipgloss.Width(innerSep)

	var tokens []footerToken
	for _, sec := range sections {
		entries := footerEntriesForSection(sec)
		if len(entries) == 0 {
			continue
		}
		for i, e := range entries {
			text := e.key + " " + e.label
			var rendered string
			if e.availFn(m) {
				rendered = styleFooter.Render(text)
			} else {
				rendered = styleFooterUnavailable.Render(text)
			}
			tok := footerToken{
				rendered: rendered,
				width:    lipgloss.Width(rendered),
			}
			if i < len(entries)-1 || hasLaterEntries(sections, sec) {
				tok.sep = innerSep
				tok.sepWidth = innerSepW
			}
			tokens = append(tokens, tok)
		}
	}
	return wrapFooterLine(indent, indent, tokens, m.width)
}

func hasLaterEntries(sections []string, current string) bool {
	foundCurrent := false
	for _, sec := range sections {
		if !foundCurrent {
			if sec == current {
				foundCurrent = true
			}
			continue
		}
		if len(footerEntriesForSection(sec)) > 0 {
			return true
		}
	}
	return false
}

// footerToken pairs a styled rendered string with its visible width and an
// optional separator that follows it within a section. Pre-measuring with
// lipgloss.Width lets us account for ANSI styling correctly.
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

	leadW := lipgloss.Width(firstLineLead)
	contW := lipgloss.Width(continuationIndent)

	var out strings.Builder
	out.WriteString(firstLineLead)

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
			out.WriteString("\n")
			out.WriteString(continuationIndent)
			lineW = contW
			lineHasToken = false
			cost = t.width
		}
		if lineHasToken {
			out.WriteString(pendingSep)
		}
		out.WriteString(t.rendered)
		lineW += cost
		lineHasToken = true
		pendingSep = t.sep
		pendingSepW = t.sepWidth
	}
	return out.String()
}

// footerEntriesForSection returns the palette entries whose section matches
// the given identifier, preserving declaration order.
func footerEntriesForSection(section string) []footerEntry {
	var out []footerEntry
	for _, e := range listFooterPalette {
		if e.section == section {
			out = append(out, e)
		}
	}
	return out
}
