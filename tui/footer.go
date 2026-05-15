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
		line1 = styleFooter.Render("  space: toggle  enter: launch  C: profile  o: open dir  p: preview  esc: clear/exit")
		line2 = styleFooter.Render("  type to filter (DSL: @profile, word)  j/k: up/down")
	case ModeConfigPicker:
		line1 = styleFooter.Render("  enter: select  esc: cancel")
		line2 = styleFooter.Render("  j/k: up/down")
	case ModeHelp:
		line1 = styleFooter.Render("  esc/?/q: close help")
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
			nav := "j/k: up/down  enter: actions  /, f: filter  q: quit  p: preview"
			if !m.activeFilter.IsEmpty() {
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
// row's state icon (e.g. `⚡ active — j/k: up/down …`). Idle/empty rows have
// no gloss so the line starts with the plain nav hints.
func listFooterLine2(m Model, nav string) string {
	gloss := ""
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		s := m.filtered[m.cursor]
		if g := StateGloss(s.State); g != "" {
			icon := palette.StateIcon(s.State)
			gloss = padIconCell(icon) + " " + g + " — "
		}
	}
	return styleFooter.Render("  " + gloss + nav)
}

// footerEntry describes one slot in the stable footer palette. The palette is
// rendered in declaration order regardless of the cursor row's state; only the
// styling of each entry changes (available vs unavailable) so users always see
// every action at the same on-screen position.
type footerEntry struct {
	key     string
	label   string
	availFn func(Model) bool
}

// hasRow reports whether a row is selected (any row-targeted action requires
// a cursor sitting on a session).
func hasRow(m Model) bool { return len(m.filtered) > 0 && m.cursor < len(m.filtered) }

// listFooterPalette is the fixed-order set of keys shown on footer line 1 in
// ModeList. Order is invariant — entries never move between states. Each
// entry's availability function decides whether to render with the available
// or unavailable style.
var listFooterPalette = []footerEntry{
	{"space", "focus", hasRow},
	{"e", "peek", func(m Model) bool {
		return hasRow(m) && m.filtered[m.cursor].State == "bolt"
	}},
	{"r", "restart", hasRow},
	{"x", "close", hasRow},
	{"c", "profile", hasRow},
	{"C", "clear", func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		return stateGroup(m.filtered[m.cursor], m.lastActivity, m.idleThreshold) == 0
	}},
	{"o", "open dir", func(m Model) bool {
		if !hasRow(m) {
			return false
		}
		s := m.filtered[m.cursor]
		_, ok := findConfigSession(m.cfg, s.EffectiveProfile(), s.EffectiveProject())
		return ok
	}},
	{"y", "copy title", hasRow},
	{"l", "launch", func(Model) bool { return true }},
	{"/", "filter", func(Model) bool { return true }},
	{"p", "preview", func(Model) bool { return true }},
	{"?", "help", func(Model) bool { return true }},
}

func listFooterLine1(m Model) string {
	var sb strings.Builder
	sb.WriteString("  ")
	for i, e := range listFooterPalette {
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
	return sb.String()
}
