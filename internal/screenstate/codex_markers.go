package screenstate

import (
	"strings"
	"unicode/utf8"
)

// codexMarkerFunc reports whether line i of a screen is a particular
// terminal-turn marker.
//
// The two turn classifiers take one of these rather than a bare clause name
// because codex-cli 0.149.x's "Completed" marker is not decidable from its own
// line: it is one common word, and only the horizontal rules Codex draws around
// it make it harness-authored evidence rather than assistant prose.
type codexMarkerFunc func(c codexClauses, lines []string, i int) bool

// terminalMarkerAt is the union of every terminal marker this file knows,
// used by the diagnostic status-line scan. The settle decision still goes
// through the two classifiers, which keep completed and interrupted apart.
func (c codexClauses) terminalMarkerAt(lines []string, i int) bool {
	return c.completedTurnMarkerAt(lines, i) || c.interruptedMarkerAt(lines, i)
}

// completedTurnMarkerAt accepts either completed-turn shape: the older
// "Worked for N" status line or 0.149.x's framed "Completed" marker.
func (c codexClauses) completedTurnMarkerAt(lines []string, i int) bool {
	return matchesLine(c.workedForRE, lines[i]) || c.completedMarkerAt(lines, i)
}

func (c codexClauses) interruptedMarkerAt(lines []string, i int) bool {
	return matchesLine(c.interruptedRE, lines[i])
}

// completedMarkerAt reports whether line i is 0.149.x's framed end-of-turn
// marker: the bare "Completed" line WITH a horizontal rule above and below it.
//
// THE FRAMING IS THE EVIDENCE, and it is structure rather than UI copy, so it
// stays in Go where a pattern override cannot reach it. Without the framing this
// marker is one ordinary English word at the start of a bullet line, which any
// assistant turn can print. Do not relax this to the line match alone.
func (c codexClauses) completedMarkerAt(lines []string, i int) bool {
	if !matchesLine(c.completedMarkerRE, lines[i]) {
		return false
	}
	return codexRuleBefore(lines, i, -1) && codexRuleBefore(lines, i, +1)
}

// codexRuleBefore walks away from line i in one direction and reports whether
// the first non-blank line it meets is a horizontal rule. Codex separates the
// marker from its rules with blank lines, so blanks are skipped; anything else
// ends the walk and is the answer.
//
// Structure, not UI copy: this is the framing half of completedMarkerAt's
// evidence and is deliberately not externalized.
func codexRuleBefore(lines []string, i, step int) bool {
	for j := i + step; j >= 0 && j < len(lines); j += step {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		return codexLineIsHorizontalRule(lines[j])
	}
	return false
}

// codexLineIsHorizontalRule reports whether a line is nothing but a run of box
// drawing horizontals. The length floor keeps a stray glyph from framing
// anything; Codex draws these the full width of the pane.
//
// The glyph and the floor stay in Go: they are the shape of the frame Codex
// draws, not words it prints, and a user override that widened either would
// weaken completedMarkerAt's only defence against transcript prose.
func codexLineIsHorizontalRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.Trim(trimmed, "─") != "" {
		return false
	}
	return utf8.RuneCountInString(trimmed) >= codexHorizontalRuleMinimumWidth
}

// codexHorizontalRuleMinimumWidth is a structural tunable, not UI copy: it
// bounds a glyph run rather than naming anything Codex says, so it stays in Go.
const codexHorizontalRuleMinimumWidth = 8

// ClassifyCodexCompletedTurnScreen reports whether a Codex screen scrape shows a
// finished assistant turn that is safe to treat as a stop transition.
//
// It is deliberately conservative: false negatives are acceptable, false
// positives are not. It returns true only when the screen is not a visible
// prompt, the latest visible Codex status line is a completed-turn marker (with
// no later live-work line), and an empty composer prompt appears after that
// marker. This mirrors the completed-turn state Codex leaves on screen when the
// assistant finishes but no Stop hook arrives.
//
// Two marker shapes are accepted, because Codex changed the one it prints: the
// older "Worked for 1m 30s" status line, and 0.149.x's framed "Completed"
// marker. Every other gate is identical for both.
func ClassifyCodexCompletedTurnScreen(screen string) bool {
	return codexTerminalTurnScreen(
		codexClausesFor(codexPatterns()), screen, codexClauses.completedTurnMarkerAt)
}

// ClassifyCodexInterruptedTurnScreen reports whether a Codex screen scrape shows
// a turn the user interrupted (Esc/Ctrl-C) that is safe to treat as a stop
// transition.
//
// It shares the completed-turn classifier's conservatism and evidence shape:
// the screen must not be a visible prompt, the latest Codex status line must be
// the TUI-generated interrupted-turn marker (with no later live-work line that
// would mean the turn resumed), and an empty composer prompt must follow that
// marker. Codex leaves this state on screen after an interruption, and —
// exactly the case native hooks miss — no Stop hook arrives.
func ClassifyCodexInterruptedTurnScreen(screen string) bool {
	return codexTerminalTurnScreen(
		codexClausesFor(codexPatterns()), screen, codexClauses.interruptedMarkerAt)
}

// codexTerminalTurnScreen is the shared body of the two classifiers above: the
// only difference between them was which terminal marker they scanned for.
//
// THE POSITIONAL REQUIREMENT IS LOAD-BEARING, and it is structure rather than UI
// copy, so no pattern file can express or relax it. The composer scan starts at
// markerIdx+1, not at zero: "an empty composer somewhere on screen" is satisfied
// by a composer rendered ABOVE a status line that is still running, which is
// precisely the screen this classifier must refuse. Do not relax the starting
// index to a whole-screen match.
//
// The prompt classification is checked first, before any status-line work. That
// ordering is what makes interrupt_hint's overlap with Codex's
// request_user_input footer free.
func codexTerminalTurnScreen(c codexClauses, screen string, marker codexMarkerFunc) bool {
	// The prompt test is NEGATIVE here — it is the refusal that keeps a pane
	// waiting on the user from being reported as a finished turn — so an
	// incomplete prompt clause set is refused outright rather than settling
	// with a blinded test. Same reasoning as screenLooksCleared's
	// labeled_transcript_line guard.
	if !codexPromptClausesDefined(c.set) {
		return false
	}
	if _, ok := classifyCodexPrompt(c.set, screen, strings.ToLower(screen)); ok {
		return false
	}

	lines := strings.Split(screen, "\n")
	markerIdx := -1
	latestStatusIsMarker := false
	for i, line := range lines {
		switch {
		case marker(c, lines, i):
			markerIdx = i
			latestStatusIsMarker = true
		case c.liveWork(line):
			latestStatusIsMarker = false
		}
	}
	if markerIdx < 0 || !latestStatusIsMarker {
		return false
	}

	for i := markerIdx + 1; i < len(lines); i++ {
		if c.composerLine(lines[i]) {
			return true
		}
	}
	return false
}
