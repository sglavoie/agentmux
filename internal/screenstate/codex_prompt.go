package screenstate

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/patternset"
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
//
// These five constants stay Go constants and are deliberately NOT data. They
// are an API contract, re-exported by internal/daemon/codex_prompt.go, which
// maps each kind to a prompt action and an icon; a kind a user could invent in
// a pattern file would arrive at that mapping with nowhere to go. The pattern
// file names the copy that identifies a kind, never the set of kinds.
type CodexPromptKind string

const (
	CodexPromptEditApproval           CodexPromptKind = "edit_approval"
	CodexPromptRequestUserInput       CodexPromptKind = "request_user_input"
	CodexPromptUnansweredConfirmation CodexPromptKind = "unanswered_confirmation"
	CodexPromptCommandApproval        CodexPromptKind = "command_approval"
	CodexPromptPermissionGrant        CodexPromptKind = "permission_grant"
)

// The prompt UI copy lives in patterns_codex.toml alongside the screen clauses,
// loaded by patterns.go and overridable per user. The clause names below are
// the contract between that file and this one; each is documented at its
// definition site there.
//
// Two of these are [group] clauses rather than flat lists, and they are the
// reason the group schema exists at all:
//
//   - prompt_permission_grant carries the `allow AND permission` conjunction
//     that used to be written inline here, so "allow" alone can never read as a
//     permission prompt.
//   - prompt_request_user_input carries the three-bucket
//     `(navigation || submission) && pane action` shape that codex-cli
//     0.134.0's bottom-pane question UI needs, because no single substring
//     identifies it.
//
// Expressing both as groups is what keeps them user-repairable: a Codex build
// that reworded one bucket is a phrase-list edit, not a build.
//
// FAIL-CLOSED ORIENTATION. A clause the installed set does not define reports
// no match, and every prompt clause therefore fails toward "no prompt is
// visible". That direction is NOT self-evidently safe here, so it is spelled
// out:
//
//   - In Classify, an undetected prompt withholds VetoBlockingPrompt, so a pane
//     genuinely waiting on the user could settle. This is the same category of
//     failure as labeled_transcript_line's — a NEGATIVE test whose absence
//     removes a refusal rather than weakening a positive claim.
//   - The settle paths therefore refuse outright when the prompt clauses are
//     incomplete rather than classifying with a blinded prompt test; see
//     codexPromptClausesDefined and its two call sites in codex.go. Classify
//     itself cannot refuse — inventing a Blocked pane out of a missing clause
//     would be a visible lie — so it degrades, and the settle refusal is what
//     keeps that degradation from producing a false stop transition.
const (
	codexClausePromptChoiceLine       = "prompt_choice_line"
	codexClausePromptChoiceFooter     = "prompt_choice_footer"
	codexClausePromptEditApproval     = "prompt_edit_approval"
	codexClausePromptRequestUserInput = "prompt_request_user_input"
	codexClausePromptUnanswered       = "prompt_unanswered_confirmation"
	codexClausePromptCommandApproval  = "prompt_command_approval"
	codexClausePromptPermissionGrant  = "prompt_permission_grant"
)

// codexPromptClauseNames is every clause the prompt classifier reads, for the
// completeness check the settle paths make. The kind clauses are included and
// not just the two gate clauses: a missing kind clause blinds exactly one
// prompt kind, which is a pane that waits forever on that one prompt while
// every other prompt still refuses correctly — the hardest failure of the set
// to diagnose from the outside.
var codexPromptClauseNames = []string{
	codexClausePromptChoiceLine,
	codexClausePromptChoiceFooter,
	codexClausePromptEditApproval,
	codexClausePromptRequestUserInput,
	codexClausePromptUnanswered,
	codexClausePromptCommandApproval,
	codexClausePromptPermissionGrant,
}

// codexPromptRequirements is codexPromptClauseNames as a loader-enforceable
// contract: every prompt clause is mandatory in a Codex override, for exactly
// the reason the fail-closed note above gives. It is folded into
// codexRequirements in codex.go.
//
// Every entry is AnyClause because the prompt classifier reads all of them
// through Patterns.Match, which handles a regex, a phrase list or a group
// alike — two of them ship AS groups, and a user is free to turn a flat list
// into a group or the reverse.
//
// The reasons are per-clause rather than shared because they are not the same
// failure: the two gate clauses blind prompt detection wholesale, while a kind
// clause blinds exactly one prompt kind and leaves the rest working, which is
// the harder of the two to diagnose from the outside.
var codexPromptRequirements = patternset.Requirements{
	{
		Name: codexClausePromptChoiceLine, Kind: patternset.AnyClause,
		Reason: "it is half the visible-choice gate every prompt kind runs through, and the half " +
			"that is a shape rather than wording",
	},
	{
		Name: codexClausePromptChoiceFooter, Kind: patternset.AnyClause,
		Reason: "it is the other half of the visible-choice gate; without both, no prompt is ever " +
			"detected and a pane waiting on the user can settle",
	},
	{
		Name: codexClausePromptEditApproval, Kind: patternset.AnyClause,
		Reason: "without it an edit-approval prompt is invisible to the settle paths",
	},
	{
		Name: codexClausePromptRequestUserInput, Kind: patternset.AnyClause,
		Reason: "without it a bottom-pane question is invisible to the settle paths",
	},
	{
		Name: codexClausePromptUnanswered, Kind: patternset.AnyClause,
		Reason: "without it an unanswered confirmation is invisible to the settle paths",
	},
	{
		Name: codexClausePromptCommandApproval, Kind: patternset.AnyClause,
		Reason: "without it a command-approval prompt is invisible to the settle paths",
	},
	{
		Name: codexClausePromptPermissionGrant, Kind: patternset.AnyClause,
		Reason: "without it a permission-grant prompt is invisible to the settle paths",
	},
}

// codexPromptClausesDefined reports whether a pattern set defines every clause
// the prompt classifier reads. See the fail-closed note above.
//
// The loader now refuses an override that fails codexPromptRequirements, which
// covers the same ground for anything that came through Load. This stays as the
// settle paths' own guard, because a pattern set can reach an adapter without
// having been loaded — a pinned test set is the everyday case.
func codexPromptClausesDefined(patterns Patterns) bool {
	for _, name := range codexPromptClauseNames {
		if !patterns.Has(name) {
			return false
		}
	}
	return true
}

// ClassifyCodexPrompt classifies a visible Codex prompt from screen text,
// against the installed Codex pattern set.
//
// It intentionally requires prompt footer or choice evidence so old transcript
// text containing prompt wording does not create a synthetic waiting state.
func ClassifyCodexPrompt(screen string) (CodexPromptKind, bool) {
	return classifyCodexPrompt(codexPatterns(), screen, strings.ToLower(screen))
}

// classifyCodexPrompt is the body, taking the pattern set and the screen's
// lowercase fold from the caller. Both are parameters because every in-package
// caller has already resolved a set and folded the screen once for its own
// clauses, and prompt classification must not repeat either.
//
// THE SHAPE OF THIS FUNCTION IS THE POLICY, and none of it is expressible in a
// pattern file: the visible-choice gate runs before any kind is considered, and
// the kinds are tested in a fixed order because their clauses overlap — a
// request-input pane also carries submit wording, and a permission prompt also
// carries choice wording. Reordering the file cannot reorder these arms.
func classifyCodexPrompt(patterns Patterns, raw, lower string) (CodexPromptKind, bool) {
	if !codexPromptHasVisibleChoice(patterns, raw, lower) {
		return "", false
	}

	switch {
	case patterns.Match(codexClausePromptEditApproval, raw, lower):
		return CodexPromptEditApproval, true
	case patterns.Match(codexClausePromptRequestUserInput, raw, lower):
		return CodexPromptRequestUserInput, true
	case patterns.Match(codexClausePromptUnanswered, raw, lower):
		return CodexPromptUnansweredConfirmation, true
	case patterns.Match(codexClausePromptCommandApproval, raw, lower):
		return CodexPromptCommandApproval, true
	case patterns.Match(codexClausePromptPermissionGrant, raw, lower):
		return CodexPromptPermissionGrant, true
	default:
		return "", false
	}
}

// codexPromptHasVisibleChoice reports whether the screen carries evidence that
// a prompt is being drawn right now, rather than quoted in scrollback.
//
// The disjunction stays in Go rather than becoming a two-ref [group], following
// claude.go's blocking-prompt gate: this is the gate ClassifyCodexPrompt runs
// BEFORE anything else, and which evidence satisfies a gate is control flow.
// The two clauses are independent on purpose — the footer is wording Codex may
// reword, the choice line is a shape it is unlikely to abandon.
func codexPromptHasVisibleChoice(patterns Patterns, raw, lower string) bool {
	return patterns.Match(codexClausePromptChoiceFooter, raw, lower) ||
		patterns.Match(codexClausePromptChoiceLine, raw, lower)
}
