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
// the terminal is too narrow to show them all. The truncation policy itself
// lives in prioritizedFooterLines at the bottom of this file, over rendered
// tokens rather than palette entries, so footers built some other way can
// reuse it.
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

// footerNever is the availability of an entry that has nothing behind it — a
// command-backed candidate whose command no key binds. It renders dim rather
// than crashing, which keeps a mistake in the binding table visible in the
// footer instead of taking the dashboard down with it.
func footerNever(Model) bool { return false }

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

// footerHintLines renders a static footerEntry palette into physical footer
// rows, wrapped to m.width. It is the adapter Past Sessions, Plans, Worktrees,
// and ASL Runs use: it turns declaration-ordered entries into prioritized
// rendered tokens and leaves the truncation policy to prioritizedFooterLines,
// whose documented rules it inherits unchanged.
func footerHintLines(m Model, entries []footerEntry, maxLines int) []string {
	return prioritizedFooterLines(footerHintTokens(m, entries), footerHintIndent, m.width, maxLines)
}

// footerHintTokens renders each entry once through the same
// availFn -> style -> footerToken pipeline as listFooterSectionsLine. A single
// pass is enough because availFn is a pure function of the Model snapshot the
// footer is being rendered for: re-running it once per drop, as the pre-token
// loop did, could only produce the same answer.
func footerHintTokens(m Model, entries []footerEntry) []prioritizedFooterToken {
	tokens := make([]prioritizedFooterToken, 0, len(entries))
	for _, e := range entries {
		tok := footerBindingToken(e.key, e.label)
		if !e.availFn(m) {
			tok = footerUnavailableBindingToken(e.key, e.label)
		}
		tokens = append(tokens, prioritizedFooterToken{token: tok, priority: e.priority})
	}
	return tokens
}

// prioritizedFooterToken is one already-rendered footer token plus the priority
// that decides when width pressure drops it. Rendering happens before the
// truncation loop runs, so the loop needs to know nothing about what produced a
// token — a static footerEntry palette or any other source of rendered tokens.
type prioritizedFooterToken struct {
	token    footerToken
	priority int
}

// prioritizedFooterLines renders prioritized rendered tokens into physical
// footer rows, wrapped to width. While the block needs more than maxLines rows,
// the token with the highest priority number is dropped (last declared first
// among ties) and footerHintMore is appended so the loss is visible. Tokens at
// footerPriorityEssential are never dropped, so the result may still exceed
// maxLines once only those remain. maxLines <= 0 disables truncation entirely,
// and width <= 0 yields the single unwrapped row wrapFooterLine falls back to.
//
// The helper owns the separator and the more marker so every caller truncates
// identically; it interprets nothing about what a token means.
func prioritizedFooterLines(tokens []prioritizedFooterToken, indent string, width, maxLines int) []string {
	kept := make([]prioritizedFooterToken, len(tokens))
	copy(kept, tokens)

	truncated := false
	for {
		lines := strings.Split(prioritizedFooterBlock(kept, indent, width, truncated), "\n")
		if maxLines <= 0 || len(lines) <= maxLines {
			return lines
		}
		drop := prioritizedFooterDropIndex(kept)
		if drop < 0 {
			return lines
		}
		kept = append(kept[:drop], kept[drop+1:]...)
		truncated = true
	}
}

// prioritizedFooterBlock renders one pass of the surviving tokens through
// wrapFooterLine, separating them with footerHintSeparator and appending the
// more marker when an earlier pass dropped something. Separators are assigned
// here rather than taken from the caller so the last token on the block — which
// changes as tokens drop — never trails one.
func prioritizedFooterBlock(tokens []prioritizedFooterToken, indent string, width int, truncated bool) string {
	sep := styleFooter.Render(footerHintSeparator)
	sepW := layout.StyledWidth(sep)

	rendered := make([]footerToken, 0, len(tokens)+1)
	for _, t := range tokens {
		rendered = append(rendered, t.token)
	}
	if truncated {
		rendered = append(rendered, footerBindingToken("?", "more"))
	}
	for i := range rendered {
		if i < len(rendered)-1 {
			rendered[i].sep = sep
			rendered[i].sepWidth = sepW
		} else {
			rendered[i].sep = ""
			rendered[i].sepWidth = 0
		}
	}
	return wrapFooterLine(indent, indent, rendered, width)
}

// prioritizedFooterDropIndex returns the index of the next token to drop: the
// highest priority number, and among equal priorities the last declared.
// Returns -1 when every remaining token is essential.
func prioritizedFooterDropIndex(tokens []prioritizedFooterToken) int {
	drop := -1
	for i, t := range tokens {
		if t.priority <= footerPriorityEssential {
			continue
		}
		if drop < 0 || t.priority >= tokens[drop].priority {
			drop = i
		}
	}
	return drop
}
