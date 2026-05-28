package daemon

import (
	"regexp"
	"strings"
)

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

// CodexPromptClassification is the daemon-ready result of classifying a Codex
// TUI prompt screen.
type CodexPromptClassification struct {
	Kind      CodexPromptKind
	Action    string
	IconState string
}

var codexChoiceLineRE = regexp.MustCompile(`(?m)^\s*(?:\x{203a}|>)\s*\d+\.`)

// ClassifyCodexPromptScreen classifies a visible Codex prompt from screen text.
//
// It intentionally requires prompt footer or choice evidence so old transcript
// text containing prompt wording does not create a synthetic waiting state.
func ClassifyCodexPromptScreen(screen string) (CodexPromptClassification, bool) {
	if !codexPromptHasVisibleChoice(screen) {
		return CodexPromptClassification{}, false
	}

	lower := strings.ToLower(screen)
	switch {
	case strings.Contains(lower, "would you like to make the following edits") ||
		strings.Contains(lower, "would you like to make the following edit"):
		return codexPromptResult(CodexPromptEditApproval, "elicitation"), true
	case codexPromptLooksLikeRequestUserInput(lower):
		return codexPromptResult(CodexPromptRequestUserInput, "elicitation"), true
	case strings.Contains(lower, "unanswered confirmation"):
		return codexPromptResult(CodexPromptUnansweredConfirmation, "elicitation"), true
	case strings.Contains(lower, "would you like to run the following command") ||
		strings.Contains(lower, "approve command") ||
		strings.Contains(lower, "command approval"):
		return codexPromptResult(CodexPromptCommandApproval, "permission"), true
	case strings.Contains(lower, "permission grant") ||
		strings.Contains(lower, "grant permission") ||
		(strings.Contains(lower, "allow") && strings.Contains(lower, "permission")):
		return codexPromptResult(CodexPromptPermissionGrant, "permission"), true
	default:
		return CodexPromptClassification{}, false
	}
}

func codexPromptResult(kind CodexPromptKind, action string) CodexPromptClassification {
	return CodexPromptClassification{
		Kind:      kind,
		Action:    action,
		IconState: IconState(action),
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
