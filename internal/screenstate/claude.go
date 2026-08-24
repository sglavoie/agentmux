package screenstate

import (
	"regexp"
	"strings"

	"github.com/sglavoie/agentmux/internal/contextlimit"
	"github.com/sglavoie/agentmux/internal/harness"
)

// This file classifies a Claude Code screen scrape. Native hooks are primary,
// but Claude documents that Stop does not fire after a user interruption, so a
// stuck ⚡ can outlive the turn that produced it. The daemon reads Claude's own
// framed input area to settle that stale interactive work.
//
// The classifier is deliberately conservative — false negatives (failing to
// settle a truly idle screen) are acceptable, false positives (settling a live
// or blocked session) are not.

// claudeComposerLineRE matches Claude's framed empty composer: the ❯ prompt
// indicator followed by only horizontal whitespace, tolerating the input box's
// leading and trailing vertical edges (│/┃). Claude 2.1.195 emits a non-breaking
// space after ❯; Kitty preserves it in get-text output, so it is included
// explicitly. A composer holding queued input has text after ❯ and cannot match,
// so a user still typing is never read as idle.
var claudeComposerLineRE = regexp.MustCompile(`(?m)^[\t \x{00a0}]*(?:[│┃][\t \x{00a0}]*)?\x{276f}[\t \x{00a0}]*(?:[│┃][\t \x{00a0}]*)?$`)

// claudeChoiceLineRE matches a selected menu option in a Claude prompt
// ("❯ 1. Yes, proceed"). It requires the ❯ selection indicator so a plain
// transcript numbered list ("1. First point") cannot be mistaken for an open
// choice prompt.
var claudeChoiceLineRE = regexp.MustCompile(`(?m)^\s*(?:[│┃]\s*)?\x{276f}\s*\d+\.\s`)

// claudeInterruptedLineRE matches Claude's generated interruption marker
// ("⎿ Interrupted by user", "[Request interrupted by user]", "Interrupted ·
// …"). Like the Codex analog it anchors the keyword to the start of the status
// line (after any bracket/box/glyph decoration) so transcript prose that merely
// narrates an interruption cannot match.
var claudeInterruptedLineRE = regexp.MustCompile(`(?i)^[^\p{L}\p{N}]*(?:request\s+)?interrupted\b`)

// The leading glyph is deliberately NOT matched: live captures show it cycling
// ✽ U+273D, ● U+25CF, ✳ U+2733 and a bare · U+00B7 within a SINGLE turn, and ·
// is also Claude's separator and footer glyph. [^\p{L}\p{N}]* skips whatever
// decoration is present and cannot skip past a letter or digit, so the match
// still anchors on the line's first word. The gerund is not matched (Claude
// randomizes it) and "esc to interrupt" is not required (2.1.x dropped it —
// that removal is this whole bug). The ellipsis plus the parenthesized LIVE
// elapsed timer, on the same line, is the discriminator.
var claudeWorkingStatusLineRE = regexp.MustCompile(
	`^[^\p{L}\p{N}]*\p{L}[\p{L}\p{M}]*\x{2026}\s*\(\s*\d+[hms]\b`)

// Past-tense analog of codexWorkedForLineRE. Claude randomizes the verb, so
// only the SHAPE is pinned: one leading word, " for ", a duration, END OF LINE.
// The end anchor is what makes this safe as positive evidence — without it,
// "Waited for 3s (see above)" matches, and so does the "thought for 5s" segment
// inside a working line's parentheses.
var claudeCompletedStatusLineRE = regexp.MustCompile(
	`^[^\p{L}\p{N}]*\p{L}[\p{L}\p{M}]*\s+for\s+(?:\d+[hms]\s*)+$`)

// claudeStatusKind is which of Claude's three recognized status-line shapes a
// single line is, if any.
type claudeStatusKind uint8

const (
	claudeStatusNone claudeStatusKind = iota
	claudeStatusWorking
	claudeStatusCompleted
	claudeStatusInterrupted
)

// claudeAdapter is the Claude Code screen adapter.
type claudeAdapter struct{}

// Harness returns Claude's canonical harness name.
func (claudeAdapter) Harness() string { return harness.Claude }

// Classify reads a Claude screen capture.
//
// The order below is the contract, not an implementation detail:
//
//  1. Every veto is computed, unconditionally. Nothing short-circuits, because
//     the Observation names all of them for diagnostics.
//  2. Only a veto-free screen is examined for positive terminal evidence.
//
// Reversing those two steps settles a working pane, because Claude's live
// status line embeds a past-tense duration fragment ("· thought for 5s") that
// the finished-line shape would otherwise match.
func (claudeAdapter) Classify(screen string) Observation {
	kind, statusLine := claudeLatestStatusLine(screen)

	var vetoes Veto
	lower := strings.ToLower(screen)
	if kind == claudeStatusWorking {
		vetoes |= VetoLiveStatusLine
	}
	// Legacy backstop only. Claude Code 2.1.x no longer renders "esc to
	// interrupt" during a tool-free turn, so this can no longer be the primary
	// working signal — but it is still emitted by older builds and, in the
	// ctrl+b form, by a live Bash tool run, and a one-way veto costs nothing to
	// keep.
	if strings.Contains(lower, "esc to interrupt") {
		vetoes |= VetoInterruptHint
	}
	if strings.Contains(lower, "ctrl+b to") {
		vetoes |= VetoBackgroundTask
	}
	if claudeScreenHasBlockingPrompt(lower, screen) {
		vetoes |= VetoBlockingPrompt
	}
	if contextlimit.ScreenShowsMarker(screen) {
		vetoes |= VetoContextLimit
	}
	if !claudeComposerLineRE.MatchString(screen) {
		vetoes |= VetoComposerBusy
	}

	observation := Observation{
		Vetoes:      vetoes,
		Fingerprint: Fingerprint(screen),
		StatusLine:  boundStatusLine(statusLine),
	}

	switch {
	case vetoes&(VetoBlockingPrompt|VetoContextLimit) != 0:
		// A screen waiting on the user is reported as such even when it also
		// carries a working hint; "blocked" is the more actionable reading and
		// neither one settles.
		observation.State = Blocked
	case vetoes&(VetoLiveStatusLine|VetoInterruptHint|VetoBackgroundTask) != 0:
		observation.State = Working
	case vetoes != 0:
		// Only VetoComposerBusy remains: queued input, or a screen scrolled
		// past its composer entirely. Neither says anything about the turn, so
		// the fail-closed default stands.
		observation.State = Unknown
	default:
		observation.State = Terminal
		switch kind {
		case claudeStatusInterrupted:
			observation.Tier = TierTerminalExplicit
		case claudeStatusCompleted:
			observation.Tier = TierTerminalShape
		default:
			observation.Tier = TierQuiescent
		}
	}
	return observation
}

// claudeLatestStatusLine scans every line and returns the LAST recognized status
// line's kind and text, mirroring ClassifyCodexCompletedTurnScreen's
// latest-status-line-wins scan (internal/daemon/codex_prompt.go).
//
// Latest-wins is what makes a resumed turn readable: Claude leaves the previous
// turn's completion and interruption markers scrolled above the current one, so
// an "anywhere on screen" test would let a marker from minutes ago outvote the
// live status line directly below it.
//
// The three regexes all use a bare ^ rather than (?m), so this must feed them
// one line at a time.
func claudeLatestStatusLine(screen string) (claudeStatusKind, string) {
	kind := claudeStatusNone
	text := ""
	for _, line := range strings.Split(screen, "\n") {
		switch {
		// Working is tested first so that a screen shape not yet seen in a live
		// capture — a working line that somehow also satisfied one of the
		// terminal shapes — resolves to the safe answer.
		case claudeWorkingStatusLineRE.MatchString(line):
			kind, text = claudeStatusWorking, line
		case claudeCompletedStatusLineRE.MatchString(line):
			kind, text = claudeStatusCompleted, line
		case claudeInterruptedLineRE.MatchString(line):
			kind, text = claudeStatusInterrupted, line
		}
	}
	return kind, text
}

// claudeScreenHasBlockingPrompt reports whether the screen shows a Claude
// permission, elicitation, or other user-blocking choice prompt. The hook path
// normally paints these as key/question, but the screen read is a second line
// of defense so a stuck bolt with a visible prompt is never settled to stop.
//
// The six phrases are English-only and exhaustive by construction; they are
// kept in this one place so that limitation has a single home. lower is passed
// in because the caller already folded the screen once.
func claudeScreenHasBlockingPrompt(lower, screen string) bool {
	switch {
	case strings.Contains(lower, "do you want to proceed"),
		strings.Contains(lower, "do you want to make this edit"),
		strings.Contains(lower, "do you want to create"),
		strings.Contains(lower, "do you want to allow"),
		strings.Contains(lower, "do you trust"),
		strings.Contains(lower, "would you like to"):
		return true
	}
	return claudeChoiceLineRE.MatchString(screen)
}
