package screenstate

import (
	"strings"
)

// composerLine reports whether one line is an EMPTY Codex composer.
//
// The regex clause matches any chevron line and captures whatever follows it;
// the decision about that remainder is here, in Go, and it is an EQUALITY test
// against the known placeholders rather than the substring search a phrase list
// normally gets. That end anchor is what stops "› Run the shell command: sleep
// 8" (a real transcript line in testdata/codex_idle_completed_t0.txt) from
// reading as an empty composer, and it must not become a prefix or contains
// test. The placeholders are compared folded because the pattern loader
// requires phrase lists to be lowercase.
func (c codexClauses) composerLine(line string) bool {
	if c.composerLineRE == nil {
		return false
	}
	match := c.composerLineRE.FindStringSubmatch(line)
	if match == nil {
		return false
	}
	rest := ""
	if len(match) > 1 {
		// Trimmed with regexp's own \s class, so the clause's trailing capture
		// behaves exactly as the inline `\s*$` anchor it replaced.
		rest = strings.TrimRight(match[1], "\t\n\f\r ")
	}
	if rest == "" {
		return true
	}
	rest = strings.ToLower(rest)
	for _, placeholder := range c.placeholders {
		if rest == placeholder {
			return true
		}
	}
	return false
}

func (c codexClauses) screenHasComposerLine(screen string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if c.composerLine(line) {
			return true
		}
	}
	return false
}

// latestStatusLine scans every line and returns the LAST recognized status
// line's kind and text, mirroring the classifiers' latest-status-line-wins scan
// below. It exists for the veto and for Observation.StatusLine; the settle
// decision stays with codexTerminalTurnScreen so the positional composer
// requirement has exactly one implementation.
func (c codexClauses) latestStatusLine(screen string) (codexStatusKind, string) {
	lines := strings.Split(screen, "\n")
	kind := codexStatusNone
	text := ""
	for i, line := range lines {
		switch {
		case c.terminalMarkerAt(lines, i):
			kind, text = codexStatusTerminal, line
		case c.liveWork(line):
			kind, text = codexStatusWorking, line
		}
	}
	return kind, text
}

// liveWork reports whether one line is positive evidence of an in-flight Codex
// turn.
//
// The two word-independent clauses sit alongside the word-dependent
// working_status_line rather than replacing it, so renaming the spinner cannot
// silently disable the classifier: a renamed spinner still ticks a timer and
// still names a key to interrupt, and either one is enough. Which of the three
// are consulted, and that ANY of them suffices, is Go's — a pattern file cannot
// collapse them.
func (c codexClauses) liveWork(line string) bool {
	return matchesLine(c.workingRE, line) ||
		matchesLine(c.liveElapsedRE, line) ||
		matchesLine(c.interruptHintRE, line)
}
