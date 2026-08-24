package daemon

import "github.com/sglavoie/agentmux/internal/screenstate"

// CodexPromptKind identifies the user-blocking Codex TUI prompt visible in a
// Kitty screen scrape. The kinds themselves are harness knowledge and live in
// internal/screenstate; this alias and the constants below exist so daemon
// callers keep naming them without importing that package.
type CodexPromptKind = screenstate.CodexPromptKind

const (
	CodexPromptEditApproval           = screenstate.CodexPromptEditApproval
	CodexPromptRequestUserInput       = screenstate.CodexPromptRequestUserInput
	CodexPromptUnansweredConfirmation = screenstate.CodexPromptUnansweredConfirmation
	CodexPromptCommandApproval        = screenstate.CodexPromptCommandApproval
	CodexPromptPermissionGrant        = screenstate.CodexPromptPermissionGrant
)

// CodexPromptClassification is the daemon-ready result of classifying a Codex
// TUI prompt screen. IconState is why this type cannot move to
// internal/screenstate: that package reports what a screen shows and never what
// a tab should look like.
type CodexPromptClassification struct {
	Kind      CodexPromptKind
	Action    string
	IconState string
}

// ClassifyCodexPromptScreen classifies a visible Codex prompt from screen text
// and dresses the result with the daemon's action and icon state.
//
// The classification itself lives in screenstate.ClassifyCodexPrompt; this is a
// lookup on top of it, never a second reading of the screen.
func ClassifyCodexPromptScreen(screen string) (CodexPromptClassification, bool) {
	kind, ok := screenstate.ClassifyCodexPrompt(screen)
	if !ok {
		return CodexPromptClassification{}, false
	}
	return codexPromptResult(kind, codexPromptAction(kind)), true
}

// codexPromptAction maps a prompt kind to the daemon action it raises. An
// unrecognized kind cannot happen — ClassifyCodexPrompt only returns the five
// above — but it falls to the elicitation default rather than an empty action,
// since an empty action would resolve to no icon at all.
func codexPromptAction(kind CodexPromptKind) string {
	switch kind {
	case CodexPromptCommandApproval, CodexPromptPermissionGrant:
		return "permission"
	default:
		return "elicitation"
	}
}

func codexPromptResult(kind CodexPromptKind, action string) CodexPromptClassification {
	return CodexPromptClassification{
		Kind:      kind,
		Action:    action,
		IconState: IconState(action),
	}
}
