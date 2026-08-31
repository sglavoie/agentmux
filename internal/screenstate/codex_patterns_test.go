package screenstate

import (
	"fmt"
	"regexp"

	"github.com/sglavoie/agentmux/internal/harness"
)

// The Codex UI copy moved into patterns_codex.toml, but several tests assert
// the pattern SHAPES directly rather than through Classify — they are the record
// of the exact lines each shape was tuned to accept and reject, and asserting
// them through the adapter would let a veto hide a clause that stopped matching.
//
// Resolving them from the embedded set here keeps those tests byte-identical
// and, more usefully, points them at the shipped pattern file: a typo in the
// TOML now fails the corpus rather than only the fixtures.
var (
	codexWorkedForLineRE            = embeddedCodexRegexp(codexClauseWorkedForLine)
	codexLiveElapsedStatusLineRE    = embeddedCodexRegexp(codexClauseLiveElapsed)
	codexBackgroundTerminalFooterRE = embeddedCodexRegexp(codexClauseBackgroundFooter)
)

// codexComposerLineRE keeps the pre-extraction spelling of the composer test.
// The composer is no longer one regex — the clause captures the text after the
// chevron and Go compares it to the placeholder list — so this is the matcher
// interface the corpus tests use rather than a *regexp.Regexp.
var codexComposerLineRE = codexComposerMatcher{clauses: embeddedCodexClauses()}

type codexComposerMatcher struct{ clauses codexClauses }

// MatchString reports whether any line of s is an empty Codex composer, which
// is what the (?m) regex this replaced reported.
func (m codexComposerMatcher) MatchString(s string) bool {
	return m.clauses.screenHasComposerLine(s)
}

// codexLineLooksLiveWork keeps the pre-extraction spelling of the live-work
// line test for the fixture corpus, which asserts it per line.
func codexLineLooksLiveWork(line string) bool {
	return embeddedCodexClauses().liveWork(line)
}

func embeddedCodexClauses() codexClauses { return codexClausesFor(embeddedCodexPatterns()) }

func embeddedCodexPatterns() Patterns {
	set, ok := PatternsFor(harness.Codex)
	if !ok {
		panic("screenstate: no embedded pattern set for " + harness.Codex)
	}
	return set
}

// embeddedCodexRegexp panics on a missing clause, deliberately: this runs at
// test-binary init over the EMBEDDED patterns, where a missing clause is a
// build-time bug. The probe path's tolerance for an absent clause is for user
// overrides, not for the file this repository ships.
func embeddedCodexRegexp(name string) *regexp.Regexp {
	re := embeddedCodexPatterns().Regexp(name)
	if re == nil {
		panic(fmt.Sprintf("screenstate: embedded %s patterns have no regex %q", harness.Codex, name))
	}
	return re
}
