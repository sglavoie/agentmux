package dashboard

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/screenstate"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// The blocked-reason column: a short tag beside the state icon saying WHY a
// Needs Attention row is waiting, so two 🔑 rows a few lines apart stop looking
// like the same thing.
//
// The daemon carries the reason as a bare harness prompt-kind string
// (state.WindowState.PromptKind → protocol.WindowStateSnapshot.Reason →
// session.RunningSession.Reason). Turning one into a word a user reads is a
// PRESENTATION decision and lives here beside the icons, colors, group labels,
// and glosses — never in internal/screenstate's pattern files. That boundary is
// load-bearing rather than tidy: a pattern file is the user-editable record of
// what a harness's prompt copy LOOKS like, so a user repairing a reworded Claude
// build edits detection. Letting the same file name the tag would let that edit
// silently rename a dashboard column, and would let a kind a user invented
// arrive here with nowhere to go.

// The reason tags. Short by design — the column is fixed-width for the whole
// render pass and every cell of it is width the directory column gives up, so a
// tag pays for itself in every row on screen, not just the one it describes.
const (
	reasonTagPermission = "perm"
	reasonTagEdit       = "edit"
	reasonTagInput      = "input"
	reasonTagTrust      = "trust"
	reasonTagAsk        = "ask"
)

// reasonTags glosses every prompt kind either harness can stamp onto a window.
// The keys are the screenstate constants rather than string literals so a
// renamed kind is a compile error here instead of a column that quietly stops
// glossing; TestReasonTagsCoverEveryPromptKind pins the other direction, that a
// NEW kind cannot be added without deciding what it reads as.
//
// The map is deliberately many-to-one. Codex draws command approval and
// permission grants as two prompts and Claude draws one, but the user's answer
// is the same in all three — "may I do this?" — so all three read `perm`. The
// tag names what is being asked of the reader, not which harness dialog asked
// it; the harness cell three columns over already says that.
var reasonTags = map[string]string{
	// Codex: five kinds, classified from the prompt visible in the pane.
	string(screenstate.CodexPromptCommandApproval):        reasonTagPermission,
	string(screenstate.CodexPromptPermissionGrant):        reasonTagPermission,
	string(screenstate.CodexPromptEditApproval):           reasonTagEdit,
	string(screenstate.CodexPromptRequestUserInput):       reasonTagInput,
	string(screenstate.CodexPromptUnansweredConfirmation): reasonTagAsk,

	// Claude: four kinds, refining an already-firing blocking-prompt veto.
	string(screenstate.ClaudePromptPermission):  reasonTagPermission,
	string(screenstate.ClaudePromptEdit):        reasonTagEdit,
	string(screenstate.ClaudePromptTrust):       reasonTagTrust,
	string(screenstate.ClaudePromptElicitation): reasonTagAsk,
}

// reasonTag returns the tag for a row, or "" when there is nothing to say.
//
// Three separate situations collapse into that empty string on purpose, because
// the honest rendering of all three is the same: say nothing.
//
//   - The row is not in the attention group. A reason explains a wait, and a
//     row that is not waiting has none to explain. The daemon already clears the
//     kind when the icon leaves the blocking class, so this is belt-and-braces
//     over a field the dashboard does not own.
//   - The row is blocked but nothing labelled it. This is the COMMON case, not a
//     failure: Copilot has no screen adapter, a hook-raised 🔑 arrives before any
//     probe reads the pane, and a pattern override may have dropped the clause
//     that names the kind. See TestClaudePromptKindAbsentUnderAFlatOverride.
//   - The kind is one this build does not know. A daemon newer than the
//     dashboard reading it — the same skew protocol's omitempty fields exist for
//     — is the only way to reach it. An unknown kind is not rendered raw: a bare
//     `unanswered_confirmation` in a 5-cell column truncates to noise, and the
//     icon already carries the fact that the row is waiting.
func reasonTag(s session.RunningSession, group int) string {
	if group != 0 {
		return ""
	}
	return reasonTags[s.Reason]
}

// reasonColumnWidth returns a stable width for every row in the current
// filtered result set: the widest tag any attention row glosses to, plus one
// trailing space. Zero means no row has a tag, and the column is not reserved
// at all.
//
// Mirrors activityColumnWidth and tabNumberColumnWidth — one scan per render
// pass, so the column cannot jitter between rows of the same frame — and the
// zero return is how it also mirrors their conditional reservation: a view with
// nothing to explain gives the whole column back to the directory rather than
// padding every row around an empty slot.
func reasonColumnWidth(rows []session.RunningSession, group func(session.RunningSession) int) int {
	widest := 0
	for _, s := range rows {
		if w := layout.Width(reasonTag(s, group(s))); w > widest {
			widest = w
		}
	}
	if widest == 0 {
		return 0
	}
	return widest + 1
}

// renderReasonCell builds one reason cell padded to width. The trailing space is
// part of the cell so callers can concatenate it after the icon without an extra
// separator, exactly as renderActivityCell does before the directory.
//
// A row with no tag pads rather than collapsing. That is the whole point of
// measuring the column once per pass: the attention row nothing has labelled
// keeps its icon, badge, and title in the same columns as the row beside it that
// is explained, so a missing reason reads as a gap in the explanation rather
// than as a differently-shaped row.
func renderReasonCell(s session.RunningSession, group, width int) string {
	if width <= 0 {
		return ""
	}
	tag := reasonTag(s, group)
	pad := width - layout.Width(tag)
	if pad < 0 {
		pad = 0
	}
	if tag == "" {
		return strings.Repeat(" ", pad)
	}
	return styleReasonTag.Render(tag) + strings.Repeat(" ", pad)
}
