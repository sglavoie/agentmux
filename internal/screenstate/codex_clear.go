package screenstate

import (
	"strings"
	"unicode"
)

// ClassifyCodexClearScreen reports whether Kitty text extents look like Codex's
// idle UI immediately after `/clear` or `/new`.
//
// False negatives are acceptable here: this probe only exists because Codex
// does not emit an immediate hook for an idle `/clear`. The classifier requires
// both a cleared-looking current screen and a transcript-free scrollback extent
// so a stopped session transcript cannot clear its own stop icon by mentioning
// `/clear` or showing a stale composer prompt.
//
// A narrow footer may truncate `Context 0% used`. In that case both reads must
// match the stricter fresh-session layout, including an empty transcript body;
// a bare composer or a Ready footer on its own is never a reset signal.
func ClassifyCodexClearScreen(screen, allText string) bool {
	clauses := codexClausesFor(codexPatterns())
	if !clauses.screenLooksCleared(screen) {
		return false
	}
	if clauses.textHasContextZero(screen) {
		return clauses.scrollbackLooksCleared(allText)
	}
	return clauses.freshSessionLayout(allText)
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
	if !c.textHasContextZero(screen) && !c.freshSessionLayout(screen) {
		return false
	}
	if _, ok := classifyCodexPrompt(c.set, screen, strings.ToLower(screen)); ok {
		return false
	}
	return !c.textHasTranscriptMarkers(screen)
}

// freshSessionLayout accepts only Codex's empty startup frame: header,
// directory, optional braille logo, empty composer, Ready footer and optional
// shortcut hint. Validate the entire history with the same grammar so old
// answers (including unlabelled prose) cannot be hidden above the viewport.
// Unknown body text fails closed; the absence of a context counter alone says
// nothing about whether a conversation was reset.
func (c codexClauses) freshSessionLayout(text string) bool {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) < 4 || !matchesLine(c.bannerRE, lines[0]) {
		return false
	}
	if !strings.HasPrefix(lines[1], "/") && !strings.HasPrefix(lines[1], "~/") && lines[1] != "~" {
		return false
	}
	i := 2
	for i < len(lines) && codexEmptyStateArt(lines[i]) {
		i++
	}
	if i >= len(lines) || !c.composerLine(lines[i]) {
		return false
	}
	i++
	if i >= len(lines) || !matchesLine(c.freshReadyRE, lines[i]) || c.liveWork(lines[i]) ||
		c.set.Match(codexClauseBackgroundFooter, lines[i], strings.ToLower(lines[i])) {
		return false
	}
	// If a counter is visible it must still positively say zero. This also
	// refuses a partially rendered counter rather than guessing its value.
	if strings.Contains(strings.ToLower(lines[i]), "context") && !matchesLine(c.contextZeroRE, lines[i]) {
		return false
	}
	i++
	if i < len(lines) && matchesLine(c.freshShortcutsRE, lines[i]) {
		i++
	}
	return i == len(lines)
}

func codexEmptyStateArt(line string) bool {
	// Codex's fresh-session logo uses Unicode braille cells, not arbitrary
	// decorative text. Accepting arbitrary non-transcript lines here would
	// let plain assistant prose masquerade as an empty conversation.
	for _, r := range line {
		if !unicode.IsSpace(r) && (r < '\u2800' || r > '\u28ff') {
			return false
		}
	}
	return line != ""
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
