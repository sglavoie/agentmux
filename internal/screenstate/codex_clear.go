package screenstate

import (
	"strings"
)

// ClassifyCodexClearScreen reports whether Kitty text extents look like Codex's
// idle UI immediately after `/clear`.
//
// False negatives are acceptable here: this probe only exists because Codex
// does not emit an immediate hook for an idle `/clear`. The classifier requires
// both a cleared-looking current screen and a transcript-free scrollback extent
// so a stopped session transcript cannot clear its own stop icon by mentioning
// `/clear` or showing a stale composer prompt.
//
// It needs none of the hardening the two turn classifiers got: `Context …0%`
// plus a transcript-free scrollback is a conjunction a working screen cannot
// satisfy, whatever Codex renames its spinner to. It also never reaches the
// stabilization sample gate — it feeds the clear probe, not the stop probe — so
// it grades no tier.
func ClassifyCodexClearScreen(screen, allText string) bool {
	clauses := codexClausesFor(codexPatterns())
	if !clauses.screenLooksCleared(screen) {
		return false
	}
	return clauses.scrollbackLooksCleared(allText)
}

// CodexScreenLooksCleared reports whether the CURRENT screen alone looks
// cleared. It is exported for the clear probe's cheap first pass, which uses it
// to decide whether the second, more expensive `--extent=all` read is worth
// making at all.
func CodexScreenLooksCleared(screen string) bool {
	return codexClausesFor(codexPatterns()).screenLooksCleared(screen)
}

func (c codexClauses) screenLooksCleared(screen string) bool {
	// labeled_transcript_line is the one Codex clause whose absence would fail
	// OPEN: it is a NEGATIVE test, so losing it makes a transcript-bearing
	// screen look cleared rather than merely unreadable. Refuse instead of
	// classifying with a weakened transcript test.
	if c.labeledRE == nil {
		return false
	}
	// The prompt test below is negative in exactly the same way; refuse rather
	// than let a blinded one clear a pane that is waiting on the user.
	if !codexPromptClausesDefined(c.set) {
		return false
	}
	if !c.screenHasComposerLine(screen) {
		return false
	}
	if !c.textHasContextZero(screen) {
		return false
	}
	if _, ok := classifyCodexPrompt(c.set, screen, strings.ToLower(screen)); ok {
		return false
	}
	return !c.textHasTranscriptMarkers(screen)
}

func (c codexClauses) scrollbackLooksCleared(allText string) bool {
	if strings.TrimSpace(allText) == "" {
		return false
	}
	if _, ok := classifyCodexPrompt(c.set, allText, strings.ToLower(allText)); ok {
		return false
	}

	hasComposer := false
	hasContextZero := false
	for _, line := range strings.Split(allText, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if c.composerLine(line) {
			hasComposer = true
			continue
		}
		if c.transcriptMarker(line) {
			return false
		}
		if matchesLine(c.contextZeroRE, line) {
			hasContextZero = true
		}
	}
	return hasComposer && hasContextZero
}

func (c codexClauses) textHasContextZero(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if matchesLine(c.contextZeroRE, line) {
			return true
		}
	}
	return false
}

func (c codexClauses) textHasTranscriptMarkers(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if c.transcriptMarker(line) {
			return true
		}
	}
	return false
}

// transcriptMarker reports whether a line is evidence of transcript content.
//
// The prefix glyph sets below stay in Go on purpose: they are the SHAPE of
// Codex's transcript decoration — bullets, quote bars and tree branches — rather
// than words Codex prints, and this is a negative test, so a user override that
// dropped one would make a transcript-bearing screen look cleared. Only the
// labelled-turn clause and the composer, which are copy, come from the pattern
// file.
func (c codexClauses) transcriptMarker(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	switch {
	case trimmed == "":
		return false
	case c.composerLine(line):
		return false
	case codexLineLooksChrome(trimmed):
		return false
	case matchesLine(c.bannerRE, trimmed):
		// The session header, which codex-cli 0.162.x draws unboxed.
		return false
	case strings.HasPrefix(trimmed, "›") || strings.HasPrefix(trimmed, ">"):
		return true
	case strings.HasPrefix(trimmed, "•") || strings.HasPrefix(trimmed, "◦"):
		return true
	case strings.HasPrefix(trimmed, "▌"):
		return true
	case strings.HasPrefix(trimmed, "└") || strings.HasPrefix(trimmed, "├"):
		return true
	case strings.HasPrefix(trimmed, "│"):
		return true
	case matchesLine(c.labeledRE, trimmed):
		return true
	case strings.HasPrefix(lower, "ran ") || strings.HasPrefix(lower, "explored"):
		return true
	default:
		return false
	}
}

// codexLineLooksChrome recognizes Codex's box frame. The glyph set is
// structural — the frame Codex draws around its composer, not copy it writes —
// so it stays in Go alongside codexLineIsHorizontalRule.
func codexLineLooksChrome(trimmed string) bool {
	if strings.Trim(trimmed, "\u2500") == "" {
		return true
	}
	if strings.Trim(trimmed, "─│╭╮╰╯") == "" {
		return true
	}
	return strings.HasPrefix(trimmed, "│") && strings.HasSuffix(trimmed, "│")
}
