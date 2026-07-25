package tui

import "strings"

// Per-mode footer palettes for the Past Sessions and Plans views. Each palette
// is a flat, declaration-ordered list: footerHintLines renders it into at most
// a fixed number of physical rows, dropping the least important entries when
// the terminal is too narrow to show them all.
const (
	footerHintIndent    = "  "
	footerHintSeparator = " · "
	// footerHintMore is appended once truncation drops an entry, pointing at the
	// help overlay where the full keymap is listed. Every mode that renders a
	// palette binds `?`, so the marker is always actionable.
	footerHintMore = "? more"
	// footerHintMaxLines is the physical row budget every mode palette renders
	// into, matching the two-row footers these palettes replaced.
	footerHintMaxLines = 2
)

// Priority bands shared by the mode palettes. 0 is reserved for entries that
// must survive any width (esc, enter, `?`); navigation hints go last because
// they are the most guessable.
const (
	footerPriorityEssential = 0
	footerPriorityPrimary   = 1
	footerPrioritySecondary = 2
	footerPriorityExtra     = 3
	footerPriorityNav       = 4
)

func footerAlways(Model) bool { return true }

func hasSessionResult(m Model) bool {
	return m.sessionCursor >= 0 && m.sessionCursor < len(m.sessionResults)
}

func hasPlanResult(m Model) bool {
	return m.planCursor >= 0 && m.planCursor < len(m.planResults)
}

func hasViewerSession(m Model) bool { return m.viewerSession != nil }

func hasViewerMessages(m Model) bool { return len(m.viewerMessages) > 0 }

var sessionSearchFooterPalette = []footerEntry{
	{"/", "filter", footerSectionApp, footerAlways, footerPrioritySecondary},
	{"h/p/r", "chips", footerSectionApp, footerAlways, footerPrioritySecondary},
	{"enter", "view", footerSectionRow, hasSessionResult, footerPriorityEssential},
	{"c", "continue", footerSectionRow, hasSessionResult, footerPriorityPrimary},
	{"f", "fork", footerSectionRow, hasSessionResult, footerPriorityPrimary},
	{"w", "worktree", footerSectionRow, hasSessionResult, footerPrioritySecondary},
	{"H", "handoff", footerSectionRow, hasSessionResult, footerPrioritySecondary},
	{"E", "edit handoff", footerSectionRow, hasSessionResult, footerPriorityExtra},
	{"d", "delete", footerSectionRow, hasSessionResult, footerPrioritySecondary},
	{"i", "ID", footerSectionRow, func(m Model) bool {
		return hasSessionResult(m) && m.sessionResults[m.sessionCursor].Meta.ID != ""
	}, footerPriorityExtra},
	{"Y", "path", footerSectionRow, func(m Model) bool {
		return selectedSessionSearchPath(m) != ""
	}, footerPriorityExtra},
	{"esc", "back", footerSectionApp, footerAlways, footerPriorityEssential},
	{"j/k", "up/down", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+d/u", "half", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+f/b", "page", footerSectionApp, footerAlways, footerPriorityNav},
	{"g/G", "top/bot", footerSectionApp, footerAlways, footerPriorityNav},
}

var sessionViewFooterPalette = []footerEntry{
	{"j/k", "move", footerSectionApp, footerAlways, footerPriorityNav},
	{"n/N", "match", footerSectionApp, func(m Model) bool {
		return len(m.viewerMatchLines) > 0
	}, footerPrioritySecondary},
	{"enter", "view message", footerSectionRow, hasViewerMessages, footerPriorityEssential},
	{"c", "continue", footerSectionRow, hasViewerSession, footerPriorityPrimary},
	{"f", "fork", footerSectionRow, hasViewerSession, footerPriorityPrimary},
	{"w", "worktree", footerSectionRow, hasViewerSession, footerPrioritySecondary},
	{"H", "handoff", footerSectionRow, hasViewerSession, footerPrioritySecondary},
	{"E", "edit handoff", footerSectionRow, hasViewerSession, footerPriorityExtra},
	{"i", "ID", footerSectionRow, func(m Model) bool {
		return m.viewerSession != nil && m.viewerSession.ID != ""
	}, footerPriorityExtra},
	{"y", "copy", footerSectionRow, hasViewerMessages, footerPrioritySecondary},
	{"Y", "path", footerSectionRow, func(m Model) bool {
		return currentSessionViewPath(m) != ""
	}, footerPriorityExtra},
	{"esc", "back", footerSectionApp, footerAlways, footerPriorityEssential},
	{"ctrl+d/u", "half", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+f/b", "page", footerSectionApp, footerAlways, footerPriorityNav},
	{"g/G", "top/bot", footerSectionApp, footerAlways, footerPriorityNav},
}

var planSearchFooterPalette = []footerEntry{
	{"/", "filter", footerSectionApp, footerAlways, footerPrioritySecondary},
	{"r", "project", footerSectionApp, footerAlways, footerPrioritySecondary},
	{"enter", "view", footerSectionRow, hasPlanResult, footerPriorityEssential},
	{"o", "sort", footerSectionApp, footerAlways, footerPriorityPrimary},
	{"e", "editor", footerSectionRow, hasPlanResult, footerPriorityPrimary},
	{"Y", "path", footerSectionRow, func(m Model) bool {
		return selectedPlanSearchPath(m) != ""
	}, footerPriorityExtra},
	{"esc", "back", footerSectionApp, footerAlways, footerPriorityEssential},
	{"j/k", "up/down", footerSectionApp, footerAlways, footerPriorityNav},
	{"g/G", "top/bot", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+d/u", "half", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+f/b", "page", footerSectionApp, footerAlways, footerPriorityNav},
}

var planViewFooterPalette = []footerEntry{
	{"/", "search", footerSectionApp, footerAlways, footerPrioritySecondary},
	{"n/N", "match", footerSectionApp, func(m Model) bool {
		return len(m.planViewerMatchLines) > 0
	}, footerPrioritySecondary},
	{"y", "copy", footerSectionRow, func(m Model) bool {
		return len(m.planViewerLines) > 0
	}, footerPrioritySecondary},
	{"Y", "path", footerSectionRow, func(m Model) bool {
		return currentPlanViewPath(m) != ""
	}, footerPriorityExtra},
	{"e", "editor", footerSectionRow, func(m Model) bool {
		return m.viewerPlan != nil
	}, footerPriorityPrimary},
	{"esc", "back", footerSectionApp, footerAlways, footerPriorityEssential},
	{"j/k", "scroll", footerSectionApp, footerAlways, footerPriorityNav},
	{"g/G", "top/bot", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+d/u", "half", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+f/b", "page", footerSectionApp, footerAlways, footerPriorityNav},
}

// footerHintLines renders entries into physical footer rows, wrapped to
// m.width. While the block needs more than maxLines rows, the entry with the
// highest priority number is dropped (last declared first among ties) and
// footerHintMore is appended so the loss is visible. Entries with
// priority 0 are never dropped, so the result may still exceed maxLines once
// only those remain. maxLines <= 0 disables truncation entirely.
func footerHintLines(m Model, entries []footerEntry, maxLines int) []string {
	kept := make([]footerEntry, len(entries))
	copy(kept, entries)

	truncated := false
	for {
		lines := strings.Split(footerHintBlock(m, kept, truncated), "\n")
		if maxLines <= 0 || len(lines) <= maxLines {
			return lines
		}
		drop := footerHintDropIndex(kept)
		if drop < 0 {
			return lines
		}
		kept = append(kept[:drop], kept[drop+1:]...)
		truncated = true
	}
}

// footerHintBlock renders one palette pass through the same
// availFn -> style -> footerToken -> wrapFooterLine pipeline as
// listFooterSectionsLine, returning the newline-joined physical rows.
func footerHintBlock(m Model, entries []footerEntry, truncated bool) string {
	sep := styleFooter.Render(footerHintSeparator)
	sepW := displayStyledWidth(sep)

	tokens := make([]footerToken, 0, len(entries)+1)
	for _, e := range entries {
		text := e.key + " " + e.label
		rendered := styleFooter.Render(text)
		if !e.availFn(m) {
			rendered = styleFooterUnavailable.Render(text)
		}
		tokens = append(tokens, footerRenderedToken(rendered))
	}
	if truncated {
		tokens = append(tokens, footerRenderedToken(styleFooter.Render(footerHintMore)))
	}
	for i := 0; i < len(tokens)-1; i++ {
		tokens[i].sep = sep
		tokens[i].sepWidth = sepW
	}
	return wrapFooterLine(footerHintIndent, footerHintIndent, tokens, m.width)
}

// footerHintDropIndex returns the index of the next entry to drop: the highest
// priority number, and among equal priorities the last declared. Returns -1
// when every remaining entry is priority 0.
func footerHintDropIndex(entries []footerEntry) int {
	drop := -1
	for i, e := range entries {
		if e.priority <= footerPriorityEssential {
			continue
		}
		if drop < 0 || e.priority >= entries[drop].priority {
			drop = i
		}
	}
	return drop
}
