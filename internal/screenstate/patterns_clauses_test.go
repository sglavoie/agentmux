package screenstate

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

// TestEmbeddedPatternsDefineEveryAdapterClause closes the gap session 2 left
// open: an adapter asking for a clause the shipped TOML does not define gets a
// silent no-match on the probe path, and only a fixture would notice.
//
// It is a build-time contract over the EMBEDDED files only. A user override is
// allowed to be missing clauses — that tolerance is the whole reason
// Patterns.Match answers false for an unknown name — but the file this
// repository ships must define every clause its adapter reads, or a rename in
// one half of the pair degrades classification with nothing failing.
//
// The lists are written out rather than derived because Go cannot enumerate
// constants; keep them in step with the claudeClause* and codexClause* blocks.
func TestEmbeddedPatternsDefineEveryAdapterClause(t *testing.T) {
	cases := map[string][]string{
		harness.Claude: {
			claudeClauseComposerLine,
			claudeClauseChoiceLine,
			claudeClauseInterrupted,
			claudeClauseWorking,
			claudeClauseCompleted,
			claudeClauseInterruptHint,
			claudeClauseBackgroundTask,
			claudeClauseBlockingPrompt,
			// The four prompt-kind clauses are OPTIONAL to a user override
			// (see claudeRequirements) but mandatory in the file this
			// repository ships: blocking_prompt is a group that refs them, so
			// a rename here would leave the shipped veto matching nothing.
			claudeClausePromptPermission,
			claudeClausePromptTrust,
			claudeClausePromptEdit,
			claudeClausePromptElicitation,
		},
		harness.Codex: {
			codexClauseComposerLine,
			codexClauseComposerPlaceholder,
			codexClauseContextZeroLine,
			codexClauseLabeledTranscript,
			codexClauseWorkedForLine,
			codexClauseCompletedMarker,
			codexClauseWorkingStatusLine,
			codexClauseInterruptHint,
			codexClauseLiveElapsed,
			codexClauseBackgroundFooter,
			codexClauseInterruptedLine,
			codexClausePromptChoiceLine,
			codexClausePromptChoiceFooter,
			codexClausePromptEditApproval,
			codexClausePromptRequestUserInput,
			codexClausePromptUnanswered,
			codexClausePromptCommandApproval,
			codexClausePromptPermissionGrant,
		},
	}
	for harnessName, clauses := range cases {
		t.Run(harnessName, func(t *testing.T) {
			set, ok := PatternsFor(harnessName)
			if !ok {
				t.Fatalf("PatternsFor(%q) returned no set", harnessName)
			}
			if got := set.Source(); got != SourceEmbedded {
				t.Fatalf("Source() = %q, want %q; the test binary must run against the embedded set",
					got, SourceEmbedded)
			}
			for _, name := range clauses {
				if !set.Has(name) {
					t.Errorf("embedded %s patterns define no clause %q", harnessName, name)
				}
			}
		})
	}
}

// TestCodexComposerPlaceholderListIsTheAlternation pins the split the extraction
// made: the placeholders are now a first-class list, and the composer clause
// must accept exactly the chevron-plus-placeholder lines the single inline
// alternation used to accept — no more (the remainder is compared for equality,
// not searched for) and no less.
func TestCodexComposerPlaceholderListIsTheAlternation(t *testing.T) {
	set := embeddedCodexPatterns()
	placeholders := set.Phrases(codexClauseComposerPlaceholder)
	if len(placeholders) != 3 {
		t.Fatalf("composer_placeholder has %d entries, want the 3 codex-cli renders", len(placeholders))
	}
	clauses := codexClausesFor(set)
	for _, placeholder := range placeholders {
		// The list is lowercase because the loader requires it; the real screen
		// is title-cased, so the equality test must fold. Both spellings pass.
		for _, line := range []string{"› " + placeholder, "›  " + placeholder + "  "} {
			if !clauses.composerLine(line) {
				t.Errorf("composerLine(%q) = false, want true", line)
			}
		}
		if clauses.composerLine("› " + placeholder + " and then some") {
			t.Errorf("composerLine matched a superstring of %q; the equality test is gone", placeholder)
		}
	}
}
