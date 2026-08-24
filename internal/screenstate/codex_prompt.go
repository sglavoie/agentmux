package screenstate

import (
	"regexp"
	"strings"
)

// This file classifies the user-blocking prompts Codex's TUI renders. It is the
// harness-free half of what used to live entirely in internal/daemon: the kinds
// and the evidence, with no icon and no action. The daemon keeps the kind →
// action → icon mapping, because an icon is the reducer's business and never
// this package's (see the package doc).
//
// The Codex adapter needs the kinds here rather than the daemon's, since a
// visible prompt is exactly what makes a Codex pane Blocked.

// CodexPromptKind identifies the user-blocking Codex TUI prompt visible in a
// Kitty screen scrape.
type CodexPromptKind string

const (
	CodexPromptEditApproval           CodexPromptKind = "edit_approval"
	CodexPromptRequestUserInput       CodexPromptKind = "request_user_input"
	CodexPromptUnansweredConfirmation CodexPromptKind = "unanswered_confirmation"
	CodexPromptCommandApproval        CodexPromptKind = "command_approval"
	CodexPromptPermissionGrant        CodexPromptKind = "permission_grant"
)

var codexChoiceLineRE = regexp.MustCompile(`(?m)^\s*(?:\x{203a}|>)\s*\d+\.`)

// ClassifyCodexPrompt classifies a visible Codex prompt from screen text.
//
// It intentionally requires prompt footer or choice evidence so old transcript
// text containing prompt wording does not create a synthetic waiting state.
func ClassifyCodexPrompt(screen string) (CodexPromptKind, bool) {
	if !codexPromptHasVisibleChoice(screen) {
		return "", false
	}

	lower := strings.ToLower(screen)
	switch {
	case strings.Contains(lower, "would you like to make the following edits") ||
		strings.Contains(lower, "would you like to make the following edit"):
		return CodexPromptEditApproval, true
	case codexPromptLooksLikeRequestUserInput(lower):
		return CodexPromptRequestUserInput, true
	case strings.Contains(lower, "unanswered confirmation"):
		return CodexPromptUnansweredConfirmation, true
	case strings.Contains(lower, "would you like to run the following command") ||
		strings.Contains(lower, "approve command") ||
		strings.Contains(lower, "command approval"):
		return CodexPromptCommandApproval, true
	case strings.Contains(lower, "permission grant") ||
		strings.Contains(lower, "grant permission") ||
		(strings.Contains(lower, "allow") && strings.Contains(lower, "permission")):
		return CodexPromptPermissionGrant, true
	default:
		return "", false
	}
}

func codexPromptHasVisibleChoice(screen string) bool {
	lower := strings.ToLower(screen)
	return strings.Contains(lower, "press enter to confirm") ||
		strings.Contains(lower, "enter to submit answer") ||
		strings.Contains(lower, "to submit answer") ||
		strings.Contains(lower, "to submit all") ||
		strings.Contains(lower, "to navigate questions") ||
		strings.Contains(lower, "change question") ||
		strings.Contains(lower, "submit with unanswered questions") ||
		codexChoiceLineRE.MatchString(screen)
}

func codexPromptLooksLikeRequestUserInput(lower string) bool {
	if strings.Contains(lower, "request_user_input") ||
		strings.Contains(lower, "submit with unanswered questions") {
		return true
	}

	hasQuestionNavigation := strings.Contains(lower, "to navigate questions") ||
		strings.Contains(lower, "change question")
	hasAnswerSubmission := strings.Contains(lower, "to submit answer") ||
		strings.Contains(lower, "to submit all")
	hasRequestPaneAction := strings.Contains(lower, "add notes") ||
		strings.Contains(lower, "type your answer") ||
		strings.Contains(lower, "select an option to add notes") ||
		strings.Contains(lower, "none of the above") ||
		strings.Contains(lower, "proceed")

	return (hasQuestionNavigation || hasAnswerSubmission) && hasRequestPaneAction
}
