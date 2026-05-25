package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/palette"
)

func renderFooter(m Model) string {
	var sb strings.Builder

	var line1, line2 string
	switch m.mode {
	case ModeSearch:
		line1 = styleFooter.Render("  filter: @profile · !state · word · \\@/\\! literal")
		line2 = styleFooter.Render("  enter: apply  esc: cancel")
	case ModeConfirm:
		line1 = styleFooter.Render("  [y]es   [n]o   [esc] cancel")
		line2 = ""
	case ModePicker:
		line1 = styleFooter.Render("  space: toggle  enter: launch  C: profile  o: open dir  O: open in editor  p: preview  esc: clear/exit")
		line2 = styleFooter.Render("  type to filter (DSL: @profile, word)  j/k: up/down")
	case ModeConfigPicker:
		line1 = styleFooter.Render("  enter: select  esc: cancel")
		line2 = styleFooter.Render("  j/k: up/down")
	case ModeHelp:
		line1 = styleFooter.Render("  j/k: up/down  g/G: top/bottom  esc/?/q: close help")
		line2 = ""
	case ModeActions:
		line1 = styleFooter.Render("  <key>: run action  esc/q: back")
		line2 = ""
	default: // ModeList
		if m.previewEnabled {
			// In preview mode the only binding shown is `p: hide preview`,
			// which is rendered right-aligned in the title row.
			line1 = ""
			line2 = ""
		} else {
			line1 = listFooterLine1(m)
			nav := "j/k: up/down  enter: focus  a: actions  /, f: filter  ?: help  q: quit  p: preview"
			if m.typeBuffer != "" {
				nav = "j/k: up/down  enter: apply  esc: clear  type more to narrow"
			} else if !m.activeFilter.IsEmpty() {
				nav += "  esc: clear filters"
				if n := len(m.activeFilter.Components()); n > 0 {
					nav += fmt.Sprintf("  1-%d: remove chip  tab: focus next", n)
				}
			}
			line2 = listFooterLine2(m, nav)
		}
	}

	sb.WriteString(line1)
	if line2 != "" {
		sb.WriteString("\n")
		sb.WriteString(line2)
	}
	if m.mode == ModeList && m.previewNotice != "" &&
		(m.previewNoticeExpires.IsZero() || time.Now().Before(m.previewNoticeExpires)) {
		sb.WriteString("\n")
		sb.WriteString(styleError.Render("  " + m.previewNotice))
	}
	return sb.String()
}

// listFooterLine2 prefixes the navigation hints with a gloss of the cursor
// row's state icon (e.g. `⚡ active — j/k: up/down …`). The gloss text picks
// up the state's accent color so the meaning of the row icon is reinforced;
// the dash separator and trailing nav stay in the dim footer style. When the
// filtered list overflows the visible window a `start–end of total` position
// fragment is prepended so the user sees where the cursor sits.
func listFooterLine2(m Model, nav string) string {
	if pos := listPositionFragment(m); pos != "" {
		nav = pos + "   " + nav
	}
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		s := m.filtered[m.cursor]
		if g := StateGloss(s.State); g != "" {
			icon := palette.StateIcon(s.State)
			glossText := padIconCell(icon) + " " + g
			return "  " + stateAccentStyle(s.State).Render(glossText) + styleFooter.Render(" — "+nav)
		}
	}
	return styleFooter.Render("  " + nav)
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
// every action at the same on-screen position. `section` groups entries on
// footer line 1 so users can map shortcuts to their semantic bucket (row
// actions, launching, filtering, app-wide commands).
type footerEntry struct {
	key     string
	label   string
	section string
	availFn func(Model) bool
}

// Section identifiers for footer line 1.
const (
	footerSectionRow    = "row"
	footerSectionLaunch = "launch"
	footerSectionFilter = "filter"
	footerSectionApp    = "app"
)

// listFooterRowCap caps the number of row-section entries rendered on footer
// line 1. Anything beyond is collapsed into a single overflow indicator so the
// palette stays scannable; entries are still listed in the help overlay.
const listFooterRowCap = 7

// listFooterSectionOrder is the left-to-right ordering of sections on line 1.
var listFooterSectionOrder = []string{
	footerSectionRow,
	footerSectionLaunch,
	footerSectionFilter,
	footerSectionApp,
}

// listFooterSectionSeparator is the glyph drawn between sections on footer
// line 1. Wider than the within-section " · " so the eye can chunk groups.
const listFooterSectionSeparator = " ▏ "

// hasRow reports whether a row is selected (any row-targeted action requires
// a cursor sitting on a session). Row actions are also marked unavailable
// while the type-to-filter buffer is non-empty: their keys divert into the
// buffer until the user presses esc or enter.
func hasRow(m Model) bool {
	if m.typeBuffer != "" {
		return false
	}
	return len(m.filtered) > 0 && m.cursor < len(m.filtered)
}

// listFooterPalette is the fixed-order set of keys shown on footer line 1 in
// ModeList. Order is invariant — entries never move between states. Each
// entry's availability function decides whether to render with the available
// or unavailable style.
var listFooterPalette = []footerEntry{
	{"enter", "focus", footerSectionRow, hasRow},
	{"a", "actions", footerSectionRow, hasRow},
	{"e", "peek", footerSectionRow, func(m Model) bool {
		return hasRow(m) && m.filtered[m.cursor].State == "bolt"
	}},
	{"r", "restart", footerSectionRow, hasRow},
	{"x", "close", footerSectionRow, hasRow},
	{"c", "profile", footerSectionRow, hasRow},
	{"C", "clear", footerSectionRow, func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		return stateGroup(m.filtered[m.cursor], m.lastActivity, m.idleThreshold) == 0
	}},
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
	{"y", "copy title", footerSectionRow, hasRow},
	{"l", "launch", footerSectionLaunch, func(Model) bool { return true }},
	{"/", "filter", footerSectionFilter, func(Model) bool { return true }},
	{"p", "preview", footerSectionApp, func(Model) bool { return true }},
	{"R", "resort", footerSectionApp, func(Model) bool { return true }},
	{"?", "help", footerSectionApp, func(Model) bool { return true }},
}

func listFooterLine1(m Model) string {
	var sb strings.Builder
	sb.WriteString("  ")

	first := true
	for _, sec := range listFooterSectionOrder {
		entries := footerEntriesForSection(sec)
		if len(entries) == 0 {
			continue
		}
		if !first {
			sb.WriteString(styleFooter.Render(listFooterSectionSeparator))
		}
		first = false

		visible := entries
		overflow := false
		if sec == footerSectionRow && len(entries) > listFooterRowCap {
			visible = entries[:listFooterRowCap]
			overflow = true
		}

		for i, e := range visible {
			if i > 0 {
				sb.WriteString(styleFooter.Render(" · "))
			}
			text := e.key + " " + e.label
			if e.availFn(m) {
				sb.WriteString(styleFooter.Render(text))
			} else {
				sb.WriteString(styleFooterUnavailable.Render(text))
			}
		}
		if overflow {
			sb.WriteString(styleFooter.Render(" · …"))
		}
	}
	return sb.String()
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
