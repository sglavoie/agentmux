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
var codexComposerLineRE = regexp.MustCompile(`(?m)^\s*(?:\x{203a}|>)\s*(?:Use /skills to list available skills)?\s*$`)
var codexContextZeroLineRE = regexp.MustCompile(`(?i)\bcontext\b.*(?:^|[^0-9])0\s*%`)
var codexLabeledTranscriptLineRE = regexp.MustCompile(`(?i)^\s*(?:user|codex|assistant|system)\s*:`)

// codexWorkedForLineRE matches Codex's completed-turn status line ("Worked for
// 1m 30s"). The trailing digit requirement anchors on a real duration so
// transcript prose such as "worked for the previous release" cannot match.
var codexWorkedForLineRE = regexp.MustCompile(`(?i)^[^\p{L}\p{N}]*worked for\s+\d`)

// codexWorkingStatusLineRE matches Codex's in-progress status line ("Working
// (5s • Esc to interrupt)"). A later match than the Worked-for line means the
// turn is still active, so the completed-turn classifier bails.
var codexWorkingStatusLineRE = regexp.MustCompile(`(?i)^[^\p{L}\p{N}]*working\b`)

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

// ClassifyCodexCompletedTurnScreen reports whether a Codex screen scrape shows a
// finished assistant turn that is safe to treat as a stop transition.
//
// It is deliberately conservative: false negatives are acceptable, false
// positives are not. It returns true only when the screen is not a visible
// prompt, the latest visible Codex status line is a completed "Worked for …"
// line (with no later "Working (…)" line), and an empty composer prompt appears
// after that status line. This mirrors the completed-turn state Codex leaves on
// screen when the assistant finishes but no Stop hook arrives.
func ClassifyCodexCompletedTurnScreen(screen string) bool {
	if _, ok := ClassifyCodexPromptScreen(screen); ok {
		return false
	}

	lines := strings.Split(screen, "\n")
	workedForIdx := -1
	latestStatusIsWorked := false
	for i, line := range lines {
		switch {
		case codexWorkedForLineRE.MatchString(line):
			workedForIdx = i
			latestStatusIsWorked = true
		case codexWorkingStatusLineRE.MatchString(line):
			latestStatusIsWorked = false
		}
	}
	if workedForIdx < 0 || !latestStatusIsWorked {
		return false
	}

	for i := workedForIdx + 1; i < len(lines); i++ {
		if codexComposerLineRE.MatchString(lines[i]) {
			return true
		}
	}
	return false
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

// ClassifyCodexClearScreen reports whether Kitty text extents look like Codex's
// idle UI immediately after `/clear`.
//
// False negatives are acceptable here: this probe only exists because Codex
// does not emit an immediate hook for an idle `/clear`. The classifier requires
// both a cleared-looking current screen and a transcript-free scrollback extent
// so a stopped session transcript cannot clear its own stop icon by mentioning
// `/clear` or showing a stale composer prompt.
func ClassifyCodexClearScreen(screen, allText string) bool {
	if !codexScreenLooksCleared(screen) {
		return false
	}
	return codexScrollbackLooksCleared(allText)
}

func codexScreenLooksCleared(screen string) bool {
	if !codexComposerLineRE.MatchString(screen) {
		return false
	}
	if !codexTextHasContextZero(screen) {
		return false
	}
	if _, ok := ClassifyCodexPromptScreen(screen); ok {
		return false
	}
	return !codexTextHasTranscriptMarkers(screen)
}

func codexScrollbackLooksCleared(allText string) bool {
	if strings.TrimSpace(allText) == "" {
		return false
	}
	if _, ok := ClassifyCodexPromptScreen(allText); ok {
		return false
	}

	hasComposer := false
	hasContextZero := false
	for _, line := range strings.Split(allText, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if codexComposerLineRE.MatchString(line) {
			hasComposer = true
			continue
		}
		if codexLineLooksTranscriptMarker(line) {
			return false
		}
		if codexContextZeroLineRE.MatchString(line) {
			hasContextZero = true
		}
	}
	return hasComposer && hasContextZero
}

func codexTextHasContextZero(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if codexContextZeroLineRE.MatchString(line) {
			return true
		}
	}
	return false
}

func codexTextHasTranscriptMarkers(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if codexLineLooksTranscriptMarker(line) {
			return true
		}
	}
	return false
}

func codexLineLooksTranscriptMarker(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	switch {
	case trimmed == "":
		return false
	case codexComposerLineRE.MatchString(line):
		return false
	case codexLineLooksChrome(trimmed):
		return false
	case strings.HasPrefix(trimmed, "\u203a") || strings.HasPrefix(trimmed, ">"):
		return true
	case strings.HasPrefix(trimmed, "\u2022") || strings.HasPrefix(trimmed, "\u25e6"):
		return true
	case strings.HasPrefix(trimmed, "\u258c"):
		return true
	case strings.HasPrefix(trimmed, "\u2514") || strings.HasPrefix(trimmed, "\u251c"):
		return true
	case strings.HasPrefix(trimmed, "\u2502"):
		return true
	case codexLabeledTranscriptLineRE.MatchString(trimmed):
		return true
	case strings.HasPrefix(lower, "ran ") || strings.HasPrefix(lower, "explored"):
		return true
	default:
		return false
	}
}

func codexLineLooksChrome(trimmed string) bool {
	if strings.Trim(trimmed, "\u2500") == "" {
		return true
	}
	if strings.Trim(trimmed, "\u2500\u2502\u256d\u256e\u2570\u256f") == "" {
		return true
	}
	return strings.HasPrefix(trimmed, "\u2502") && strings.HasSuffix(trimmed, "\u2502")
}
