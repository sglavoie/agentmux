package screenstate

import (
	"fmt"
	"regexp"

	"github.com/sglavoie/agentmux/internal/harness"
)

// The Claude status-line regexes moved into patterns_claude.toml, but
// TestClaudeCompletedStatusLineRejectsWorkingDurations and
// TestClaudeWorkingStatusLineCorpus assert the pattern SHAPES directly rather
// than through Classify — they are the record of the exact lines each shape was
// tuned to accept and reject, and asserting them through the adapter would let a
// veto hide a regex that stopped matching.
//
// Resolving them from the embedded set here keeps those tests byte-identical
// and, more usefully, points them at the shipped pattern file: a typo in the
// TOML now fails the corpus rather than only the fixtures.
var (
	claudeWorkingStatusLineRE   = embeddedClaudeRegexp(claudeClauseWorking)
	claudeCompletedStatusLineRE = embeddedClaudeRegexp(claudeClauseCompleted)
)

// embeddedClaudeRegexp panics on a missing clause, deliberately: this runs at
// test-binary init over the EMBEDDED patterns, where a missing clause is a
// build-time bug. The probe path's tolerance for an absent clause is for user
// overrides, not for the file this repository ships.
func embeddedClaudeRegexp(name string) *regexp.Regexp {
	set, ok := PatternsFor(harness.Claude)
	if !ok {
		panic("screenstate: no embedded pattern set for " + harness.Claude)
	}
	re := set.Regexp(name)
	if re == nil {
		panic(fmt.Sprintf("screenstate: embedded %s patterns have no regex %q", harness.Claude, name))
	}
	return re
}
