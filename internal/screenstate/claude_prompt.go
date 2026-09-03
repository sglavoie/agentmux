package screenstate

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// This file names the user-blocking prompts Claude Code draws, in the same
// shape codex_prompt.go names Codex's. It is a REFINEMENT layer and nothing
// more: claude.go already decided that a screen carrying one of these is
// Blocked, and this only says which of them it was.
//
// That difference from Codex runs through every design choice below, so it is
// stated once here rather than repeated. For Codex the kind clauses ARE the
// prompt detection — classifyCodexPrompt is what raises the state — so a
// missing kind clause blinds a veto and codexPromptRequirements makes every one
// of them mandatory. For Claude the veto comes from blocking_prompt as a whole,
// which stays required in claudeRequirements; the four clauses here only turn
// an already-firing veto into a label. Losing one costs a reason string, not a
// refusal, so none of them is required and an override that predates the split
// keeps working untouched. See the paragraph above claudeRequirements.

// ClaudePromptKind identifies the user-blocking Claude Code prompt visible in a
// Kitty screen scrape.
//
// These four constants stay Go constants and are deliberately NOT data. They
// are an API contract: the daemon carries a kind onto a window's state and the
// dashboard glosses it, and a kind a user could invent in a pattern file would
// arrive at that mapping with nowhere to go. The pattern file names the copy
// that identifies a kind, never the set of kinds — so widening
// prompt_permission for a reworded Claude build is a supported repair, and
// adding a fifth list to patterns_claude.toml reaches nothing.
//
// The four values are also deliberately distinct from every CodexPromptKind.
// The two harnesses share one field (state.WindowState.PromptKind is a bare
// string, so that Claude and Codex kinds can live in it without a shared enum),
// which makes the union of the two sets the domain any renderer must map
// totally. TestClaudeAndCodexPromptKindsDoNotCollide pins it.
type ClaudePromptKind string

const (
	// ClaudePromptPermission is Claude asking to run or use something — a Bash
	// command, a tool, an MCP server.
	ClaudePromptPermission ClaudePromptKind = "permission"
	// ClaudePromptTrust is the startup workspace-trust dialog. It is not a
	// permission: nothing in the session is waiting on the answer, because the
	// session has not started.
	ClaudePromptTrust ClaudePromptKind = "trust"
	// ClaudePromptEdit is Claude asking to write to the filesystem — an edit to
	// an existing file, or the creation of a new one.
	ClaudePromptEdit ClaudePromptKind = "edit"
	// ClaudePromptElicitation is Claude asking the user to choose rather than to
	// approve.
	ClaudePromptElicitation ClaudePromptKind = "elicitation"
)

// The prompt UI copy lives in patterns_claude.toml alongside the screen
// clauses, loaded by patterns.go and overridable per user. The clause names
// below are the contract between that file and this one; each is documented at
// its definition site there.
//
// The shipped blocking_prompt is a [group] whose `any` refs exactly these four,
// which is what keeps the copy in one place: the veto and the label read the
// same phrases and cannot drift apart. The group shape is not required of an
// override, though — see ClassifyClaudePrompt.
const (
	claudeClausePromptPermission  = "prompt_permission"
	claudeClausePromptTrust       = "prompt_trust"
	claudeClausePromptEdit        = "prompt_edit"
	claudeClausePromptElicitation = "prompt_elicitation"
)

// ClassifyClaudePrompt reports which blocking prompt a Claude screen is
// showing, against the installed Claude pattern set.
//
// It answers false for a set that does not define the four clauses — the
// ordinary fail-closed direction of Patterns.Match — and that is the whole
// backward-compatibility story: an override that kept blocking_prompt as a flat
// phrase list still vetoes through claude.go and simply yields no kind here.
// The caller must therefore read a false result as "no label available", never
// as "no prompt is on screen".
//
// Unlike ClassifyCodexPrompt this runs NO visible-choice gate of its own.
// Codex's gate exists because its classification is what RAISES a blocked
// state, so prompt wording quoted in scrollback would invent one. This function
// raises nothing: its only caller asks about a window whose icon already says a
// prompt is up, and adding a gate here would only lose the label on a real
// prompt whose choice line has scrolled off.
func ClassifyClaudePrompt(screen string) (ClaudePromptKind, bool) {
	return classifyClaudePrompt(claudePatterns(), screen, strings.ToLower(screen))
}

// classifyClaudePrompt is the body, taking the pattern set and the screen's
// lowercase fold from the caller, for the same reason classifyCodexPrompt does:
// an in-package caller has already resolved a set and folded the screen once.
//
// THE ORDER OF THE ARMS IS GO'S AND THE PATTERN FILE CANNOT REORDER IT. The
// clauses overlap in practice — a create prompt and a permission prompt both
// open "do you want to", and an elicitation's "would you like to" turns up
// under prompts of every other kind — so the arms run most specific first and
// the loosest wording last. Reordering patterns_claude.toml reorders nothing.
func classifyClaudePrompt(patterns Patterns, raw, lower string) (ClaudePromptKind, bool) {
	switch {
	case patterns.Match(claudeClausePromptTrust, raw, lower):
		return ClaudePromptTrust, true
	case patterns.Match(claudeClausePromptEdit, raw, lower):
		return ClaudePromptEdit, true
	case patterns.Match(claudeClausePromptPermission, raw, lower):
		return ClaudePromptPermission, true
	case patterns.Match(claudeClausePromptElicitation, raw, lower):
		return ClaudePromptElicitation, true
	default:
		return "", false
	}
}

// claudePatterns is the installed Claude pattern set, for the package-level
// classifiers that are called without an adapter. It mirrors codexPatterns.
func claudePatterns() Patterns {
	set, _ := PatternsFor(harness.Claude)
	return set
}
