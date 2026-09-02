package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/layout"
)

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

func hasWorktreeResult(m Model) bool {
	return m.worktreeCursor >= 0 && m.worktreeCursor < len(m.worktreeFiltered)
}

func hasPresentWorktree(m Model) bool {
	return hasWorktreeResult(m) && m.worktreeFiltered[m.worktreeCursor].Exists
}

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

var worktreesFooterPalette = []footerEntry{
	{"/", "filter", footerSectionApp, footerAlways, footerPrioritySecondary},
	{"enter", "reopen", footerSectionRow, hasPresentWorktree, footerPriorityEssential},
	{"o", "shell", footerSectionRow, hasPresentWorktree, footerPriorityPrimary},
	{"O", "editor", footerSectionRow, hasPresentWorktree, footerPriorityPrimary},
	{"d", "remove", footerSectionRow, hasWorktreeResult, footerPriorityPrimary},
	{"D", "delete branch", footerSectionRow, hasWorktreeResult, footerPrioritySecondary},
	{"F", "forget", footerSectionRow, hasWorktreeResult, footerPrioritySecondary},
	{"Y", "path", footerSectionRow, func(m Model) bool { return selectedWorktreePath(m) != "" }, footerPrioritySecondary},
	{"r", "refresh", footerSectionApp, footerAlways, footerPrioritySecondary},
	{"?", "help", footerSectionApp, footerAlways, footerPriorityEssential},
	{"esc", "back", footerSectionApp, footerAlways, footerPriorityEssential},
	{"j/k", "up/down", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+d/u", "half", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+f/b", "page", footerSectionApp, footerAlways, footerPriorityNav},
	{"g/G", "top/bot", footerSectionApp, footerAlways, footerPriorityNav},
}

var aslRunsFooterPalette = []footerEntry{
	{"f", "focus", footerSectionRow, aslFocusAvailable, footerPriorityEssential},
	{"r", "respond", footerSectionRow, aslRespondAvailable, footerPriorityEssential},
	{"s", "stop", footerSectionRow, aslStopAvailable, footerPriorityPrimary},
	{"a", "abort", footerSectionRow, aslAbortAvailable, footerPriorityPrimary},
	{"w", "workbench", footerSectionRow, aslWorkbenchAvailable, footerPriorityPrimary},
	{"Y", "path", footerSectionRow, func(m Model) bool {
		row, ok := m.selectedASLRow()
		return ok && row.WorkspaceRoot != ""
	}, footerPrioritySecondary},
	{"?", "help", footerSectionApp, footerAlways, footerPriorityEssential},
	{"esc", "back", footerSectionApp, footerAlways, footerPriorityEssential},
	{"j/k", "up/down", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+d/u", "half", footerSectionApp, footerAlways, footerPriorityNav},
	{"ctrl+f/b", "page", footerSectionApp, footerAlways, footerPriorityNav},
	{"g/G", "top/bot", footerSectionApp, footerAlways, footerPriorityNav},
}

// aslRespondAvailable, aslStopAvailable, and aslAbortAvailable read exactly the
// same gate the key handlers do, so the footer can never offer an action the
// bridge would refuse.
func aslRespondAvailable(m Model) bool {
	row, ok := m.selectedASLRow()
	if !ok {
		return false
	}
	allowed, _ := aslActionAllowed(m, row, asl.ResponseGateMethod(asl.ResponseKindFor(row)))
	return allowed
}

func aslStopAvailable(m Model) bool {
	return aslControlAvailable(m, aslbridge.MethodRunStopAfterCurrent)
}

func aslAbortAvailable(m Model) bool {
	return aslControlAvailable(m, aslbridge.MethodRunAbort)
}

func aslControlAvailable(m Model, method string) bool {
	row, ok := m.selectedASLRow()
	if !ok {
		return false
	}
	allowed, _ := aslActionAllowed(m, row, method)
	return allowed
}

var aslResponseFooterPalette = []footerEntry{
	{"ctrl+s", "submit", footerSectionRow, footerAlways, footerPriorityEssential},
	{"tab", "editor/buttons", footerSectionRow, footerAlways, footerPriorityEssential},
	{"enter", "newline or button", footerSectionRow, footerAlways, footerPriorityPrimary},
	{"ctrl+n/p", "question", footerSectionRow, func(m Model) bool {
		return len(m.aslResponse.QuestionIDs) > 1
	}, footerPriorityPrimary},
	{"esc", "cancel", footerSectionApp, footerAlways, footerPriorityEssential},
}

// aslFocusAvailable mirrors what f actually does: it is offered only when the
// selected run resolves to at least one live Kitty session.
func aslFocusAvailable(m Model) bool {
	row, ok := m.selectedASLRow()
	if !ok {
		return false
	}
	if _, bound := asl.ExactBinding(row, m.sessions); bound {
		return true
	}
	return len(asl.FallbackCandidates(row, m.sessions)) > 0
}

func aslWorkbenchAvailable(m Model) bool {
	row, ok := m.selectedASLRow()
	return ok && row.PlanDir != "" && commands.BrowserSupported()
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
	sepW := layout.StyledWidth(sep)

	tokens := make([]footerToken, 0, len(entries)+1)
	for _, e := range entries {
		if e.availFn(m) {
			tokens = append(tokens, footerBindingToken(e.key, e.label))
		} else {
			tokens = append(tokens, footerUnavailableBindingToken(e.key, e.label))
		}
	}
	if truncated {
		tokens = append(tokens, footerBindingToken("?", "more"))
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
