package screenstate

import (
	"regexp"
	"strings"
	"testing"
)

// This file guards the one genuinely non-mechanical part of the prompt
// extraction: two boolean shapes that used to be Go expressions are now [group]
// clauses, and a group that silently collapsed into a flat OR would still pass
// every fixture in codex_prompt_test.go while classifying ordinary prose as a
// blocking prompt.

// TestCodexPromptPermissionGrantIsAConjunction pins the `allow AND permission`
// shape. Either weak half alone must not classify: "allow" is common enough in
// prose that a flat list containing it would pin panes at a fake prompt.
func TestCodexPromptPermissionGrantIsAConjunction(t *testing.T) {
	set := embeddedCodexPatterns()
	if _, ok := set.Group(codexClausePromptPermissionGrant); !ok {
		t.Fatalf("%s is not a group; the conjunction has been flattened", codexClausePromptPermissionGrant)
	}

	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "allow alone", body: "Allow the build to finish before retrying.", want: false},
		{name: "permission alone", body: "The permission model changed in 0.149.", want: false},
		{name: "both words", body: "Allow Codex permission to run commands here?", want: true},
		{name: "named grant", body: "Grant permission for this workspace?", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := set.Match(codexClausePromptPermissionGrant, tc.body, strings.ToLower(tc.body)); got != tc.want {
				t.Fatalf("Match(%s) = %v, want %v", codexClausePromptPermissionGrant, got, tc.want)
			}
		})
	}
}

// TestCodexPromptRequestUserInputIsThreeBuckets pins the
// (navigation || submission) && pane-action shape codex-cli 0.134.0's
// bottom-pane question UI needs. Neither side alone may match: the pane's
// action words appear in ordinary prose, and its navigation hints appear under
// other prompts.
func TestCodexPromptRequestUserInputIsThreeBuckets(t *testing.T) {
	set := embeddedCodexPatterns()
	if _, ok := set.Group(codexClausePromptRequestUserInput); !ok {
		t.Fatalf("%s is not a group; the three-bucket shape has been flattened",
			codexClausePromptRequestUserInput)
	}

	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "pane action alone", body: "Proceed once you add notes to the ticket.", want: false},
		{name: "navigation alone", body: "Use ctrl+p to navigate questions in the docs.", want: false},
		{name: "submission alone", body: "enter to submit answer", want: false},
		{name: "navigation and pane action", body: "↑/↓ to navigate questions\nAdd notes", want: true},
		{name: "submission and pane action", body: "⏎ to submit answer\nType your answer (optional)", want: true},
		{name: "named tool", body: "request_user_input", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := set.Match(codexClausePromptRequestUserInput, tc.body, strings.ToLower(tc.body)); got != tc.want {
				t.Fatalf("Match(%s) = %v, want %v", codexClausePromptRequestUserInput, got, tc.want)
			}
		})
	}
}

// TestCodexPromptClausesGateTheSettlePaths pins the fail-closed decision the
// extraction forced: the prompt classifier is a NEGATIVE test in both settle
// paths, so an override that drops a prompt clause must withhold settling
// rather than settle with a blinded prompt test. Withholding is a stuck ⚡; the
// alternative is a stop transition on a pane waiting for the user.
func TestCodexPromptClausesGateTheSettlePaths(t *testing.T) {
	completed := "─────────\n• Completed\n─────────\n› Ask Codex to do anything\n"

	full := embeddedCodexPatterns()
	if !codexPromptClausesDefined(full) {
		t.Fatalf("embedded set does not define every prompt clause")
	}
	if !codexTerminalTurnScreen(codexClausesFor(full), completed, codexClauses.completedTurnMarkerAt) {
		t.Fatalf("completed-turn screen did not settle against the embedded set")
	}

	for _, name := range codexPromptClauseNames {
		t.Run(name, func(t *testing.T) {
			partial := withoutClause(full, name)
			if codexPromptClausesDefined(partial) {
				t.Fatalf("clause %q still present after removal", name)
			}
			if codexTerminalTurnScreen(codexClausesFor(partial), completed, codexClauses.completedTurnMarkerAt) {
				t.Fatalf("settled with prompt clause %q missing; the negative test was blinded", name)
			}
		})
	}
}

// withoutClause copies a pattern set with one clause removed, standing in for a
// user override written against a different release.
func withoutClause(patterns Patterns, name string) Patterns {
	copied := Patterns{
		harness: patterns.harness,
		source:  patterns.source,
		regexes: make(map[string]*regexp.Regexp, len(patterns.regexes)),
		phrases: make(map[string][]string, len(patterns.phrases)),
		groups:  make(map[string]Group, len(patterns.groups)),
	}
	for key, value := range patterns.regexes {
		if key != name {
			copied.regexes[key] = value
		}
	}
	for key, value := range patterns.phrases {
		if key != name {
			copied.phrases[key] = value
		}
	}
	for key, value := range patterns.groups {
		if key != name {
			copied.groups[key] = value
		}
	}
	return copied
}
