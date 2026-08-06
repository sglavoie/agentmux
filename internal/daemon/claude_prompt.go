package daemon

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/sglavoie/agentmux/internal/contextlimit"
)

// This file classifies a Claude Code screen scrape into "the interactive turn
// is genuinely finished" evidence. Native hooks are primary, but Claude
// documents that Stop does not fire after a user interruption, so a stuck ⚡
// can outlive the turn that produced it. The daemon reads Claude's own framed
// input area to settle that stale interactive work.
//
// The classifiers are deliberately conservative — false negatives (failing to
// settle a truly idle screen) are acceptable, false positives (settling a live
// or blocked session) are not. Two independent shapes count as stop evidence:
//
//   - Composer idle: an empty ❯ composer in Claude's framed input area with no
//     active spinner / interrupt hint, no running background-task or agent
//     indicator, no permission/elicitation/other blocking prompt, and no
//     context-limit recovery marker. This is the general screen-probe signal
//     and the probe stabilizes it across two consecutive samples.
//   - Interruption: additionally, Claude's generated interruption marker is
//     present on the finished screen. The marker is strong, self-evident
//     evidence of a terminated turn, so the probe treats it as immediate idle
//     evidence (a single sample) while still requiring the same empty composer
//     and the same safety gates.

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

// ClassifyClaudeIdleScreen reports whether a Claude screen scrape shows a
// finished turn safe to treat as a stop transition: an empty framed composer
// with no active, blocking, or context-limited chrome. It is the general
// screen-probe signal; the probe requires two consecutive agreeing samples
// before acting on it.
func ClassifyClaudeIdleScreen(screen string) bool {
	if !claudeStopScreenIsSafe(screen) {
		return false
	}
	return claudeHasEmptyComposer(screen)
}

// ClassifyClaudeInterruptedScreen reports whether a Claude screen scrape shows a
// user-interrupted turn safe to treat as a stop transition. It shares the idle
// classifier's safety gates and empty-composer requirement, and additionally
// requires Claude's generated interruption marker. The marker is strong,
// self-evident evidence, so the probe treats a positive result as immediate
// idle evidence (a single sample).
func ClassifyClaudeInterruptedScreen(screen string) bool {
	if !ClassifyClaudeIdleScreen(screen) {
		return false
	}
	return claudeScreenHasInterruptionMarker(screen)
}

// claudeStopScreenIsSafe reports whether the screen is free of every condition
// under which a synthetic stop would be wrong: an active spinner / interrupt
// hint, a running background-task or agent indicator, a blocking
// permission/elicitation/choice prompt, or the context-limit recovery marker.
// The context-limit gate is what makes the dedicated context-limit probe win a
// race with idle evidence: a context-limited screen never classifies as idle.
func claudeStopScreenIsSafe(screen string) bool {
	if claudeScreenHasActiveWork(screen) {
		return false
	}
	if claudeScreenHasBlockingPrompt(screen) {
		return false
	}
	if contextlimit.ScreenShowsMarker(screen) {
		return false
	}
	return true
}

// claudeHasEmptyComposer reports whether Claude's empty framed composer is
// present on screen.
func claudeHasEmptyComposer(screen string) bool {
	return claudeComposerLineRE.MatchString(screen)
}

// claudeScreenHasActiveWork reports whether the screen shows Claude actively
// working — the main turn, a subagent, a tool run, or a background task. Claude
// renders the "esc to interrupt" hint whenever a cancellable operation is in
// flight, so its presence anywhere on the visible screen means the session is
// not idle. A detached background-task manager hint keeps the session busy too.
func claudeScreenHasActiveWork(screen string) bool {
	lower := strings.ToLower(screen)
	if strings.Contains(lower, "esc to interrupt") {
		return true
	}
	// Detached background tasks / running agents surface a manager hint even
	// when the main composer looks idle.
	if strings.Contains(lower, "ctrl+b to") {
		return true
	}
	return false
}

// claudeWindowTitleHasActiveWork reports whether Claude's pane title carries
// its out-of-band busy marker. Recent Claude builds can keep an empty composer
// visible while moving the only active-work signal into the terminal title, so
// the screen body alone is not sufficient idle evidence. The star is Claude's
// ordinary work marker; Braille-pattern runes cover its alternate high-effort
// spinner without coupling freshness to the individual animation frame.
func claudeWindowTitleHasActiveWork(title string) bool {
	runes := []rune(strings.TrimLeftFunc(title, unicode.IsSpace))
	if len(runes) < 2 || !unicode.IsSpace(runes[1]) {
		return false
	}
	return runes[0] == '\u2733' || runes[0] >= '\u2800' && runes[0] <= '\u28ff'
}

// claudeScreenHasBlockingPrompt reports whether the screen shows a Claude
// permission, elicitation, or other user-blocking choice prompt. The hook path
// normally paints these as key/question, but the screen read is a second line
// of defense so a stuck bolt with a visible prompt is never settled to stop.
func claudeScreenHasBlockingPrompt(screen string) bool {
	lower := strings.ToLower(screen)
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

// claudeScreenHasInterruptionMarker reports whether Claude's generated
// interruption marker appears as a status line on screen.
func claudeScreenHasInterruptionMarker(screen string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if claudeInterruptedLineRE.MatchString(line) {
			return true
		}
	}
	return false
}
