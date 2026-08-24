package screenstate

import (
	"regexp"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// This file classifies a Codex screen scrape. It is the Codex half of what
// internal/screenstate/claude.go does for Claude Code, and it moved here from
// internal/daemon for the same reason: a screen scrape is authoritative
// evidence about a running turn, and the evidence rules belong next to the
// normalization and fingerprint primitives they share a failure mode with.
//
// Codex was already the stronger of the two harnesses — it has always demanded
// a POSITIVE terminal marker rather than settling on the absence of a scary
// one — so the port preserves its shape rather than rewriting it. Two
// word-independent vetoes are added on top; see codexInterruptHintRE and
// codexLiveElapsedStatusLineRE.

// Codex renders the empty composer with a dim placeholder. Kitty's get-text
// output intentionally drops styling, so the placeholder is indistinguishable
// from ordinary text and must be recognized explicitly. Keep this list narrow:
// accepting arbitrary text after the chevron would let an old transcript prompt
// satisfy the terminal-screen classifiers.
//
// ACCEPTED RESIDUAL BRITTLENESS. A Codex build that ships a third placeholder
// makes the composer unmatchable and withholds settling — a stuck ⚡, never a
// false 🛑 — which is the direction this package fails in by design. That is
// not hypothetical: codex-cli 0.149.1 says "Ask Codex to do anything", which is
// not in this list, so the session-2 captures do not settle today.
// TestCodexComposerPlaceholderIsNarrowAndFailsClosed pins both halves of that
// statement so the cost stays visible instead of being rediscovered.
var codexComposerLineRE = regexp.MustCompile(`(?m)^\s*(?:\x{203a}|>)\s*(?:(?:Use /skills to list available skills)|(?:Find and fix a bug in @filename))?\s*$`)
var codexContextZeroLineRE = regexp.MustCompile(`(?i)\bcontext\b.*(?:^|[^0-9])0\s*%`)
var codexLabeledTranscriptLineRE = regexp.MustCompile(`(?i)^\s*(?:user|codex|assistant|system)\s*:`)

// codexWorkedForLineRE matches Codex's completed-turn status line ("Worked for
// 1m 30s"). The trailing digit requirement anchors on a real duration so
// transcript prose such as "worked for the previous release" cannot match.
var codexWorkedForLineRE = regexp.MustCompile(`(?i)^[^\p{L}\p{N}]*worked for\s+\d`)

// codexWorkingStatusLineRE matches Codex's in-progress status line ("Working
// (5s • Esc to interrupt)"). A later match than the Worked-for line means the
// turn is still active, so the completed-turn classifier bails.
var codexWorkingStatusLineRE = regexp.MustCompile(`(?i)^[^\p{L}\p{N}]*working\b`)

// Generic in-flight interrupt hint, whatever key it names. Codex's
// request_user_input footer also contains it, but that screen is rejected by
// the prompt classifier before this veto is consulted, so the overlap is free.
var codexInterruptHintRE = regexp.MustCompile(`(?i)\bto (?:interrupt|cancel)\b`)

// Word-independent twin of codexWorkingStatusLineRE: a renamed spinner still
// ticks a timer, and a ticking timer is by definition not a finished turn.
// "Worked for 1m 30s" has no parentheses and cannot match.
//
// Do NOT promote this to a shared helper: it also matches a parenthesised
// elapsed time on a Claude working line, which is harmless only because this
// file is Codex-only.
var codexLiveElapsedStatusLineRE = regexp.MustCompile(
	`^[^\p{L}\p{N}]*\p{L}[\p{L}\p{M}]*\s*\(\s*\d+[hms]\b`)

// codexInterruptedLineRE matches Codex's TUI-generated interrupted-turn marker
// ("⏹ Interrupted", "• Interrupted by user", "⚠ Conversation interrupted …").
// Like codexWorkedForLineRE it anchors the keyword to the start of the status
// line (after any bullet/warning-glyph decoration) so transcript prose that
// merely narrates an interruption — or Codex's own "Working (… Esc to
// interrupt)" spinner, which begins with "Working" and never with "interrupted"
// — cannot match.
var codexInterruptedLineRE = regexp.MustCompile(`(?i)^[^\p{L}\p{N}]*(?:conversation\s+)?interrupted\b`)

// codexStatusKind is which of Codex's recognized status-line shapes a single
// line is, if any. Unlike Claude's three kinds the two terminal markers collapse
// into one: both are literal, word-anchored and harness-authored, so both grade
// the same (see codexAdapter.Classify).
type codexStatusKind uint8

const (
	codexStatusNone codexStatusKind = iota
	codexStatusWorking
	codexStatusTerminal
)

// codexAdapter is the Codex screen adapter.
type codexAdapter struct{}

// Harness returns Codex's canonical harness name.
func (codexAdapter) Harness() string { return harness.Codex }

// Classify reads a Codex screen capture.
//
// The Adapter contract holds here exactly as it does for Claude: every veto is
// computed first and unconditionally, and positive terminal evidence is
// consulted only on a veto-free screen.
//
// TIERS. Both Codex terminal markers — codexWorkedForLineRE and
// codexInterruptedLineRE — are literal, word-anchored and written by the
// harness itself, so both are TierTerminalExplicit. TierTerminalShape and
// TierQuiescent are UNREACHABLE for Codex today, for two separate reasons:
//
//   - the Codex screen-stop probe settles on exactly those two markers
//     (internal/daemon/codex_prompt_probe_clear.go), and Codex has no
//     bare-composer-means-idle path like Claude's, so there is no quiescent
//     settle to grade; and
//   - ClassifyCodexClearScreen feeds a different probe entirely
//     (internal/daemon/codex_prompt_probe_observation.go) and never reaches the
//     sample gate, so a cleared screen is not a Codex tier either.
//
// A settleable screen must therefore NEVER come back TierNone. A tier the
// probe's policy table has no row for falls to Go's zero value, which
// applyAuthoritativeIdle floors to one sample — silently below the two samples
// Layer B needs to be anything at all.
func (codexAdapter) Classify(screen string) Observation {
	kind, statusLine := codexLatestStatusLine(screen)

	var vetoes Veto
	if kind == codexStatusWorking {
		vetoes |= VetoLiveStatusLine
	}
	// Unlike Claude, Codex still renders this hint on every live turn, so it is
	// genuinely useful here rather than a legacy backstop. It remains a one-way
	// veto: its absence is not evidence a turn ended.
	if codexInterruptHintRE.MatchString(screen) {
		vetoes |= VetoInterruptHint
	}
	if _, ok := ClassifyCodexPrompt(screen); ok {
		vetoes |= VetoBlockingPrompt
	}
	if !codexComposerLineRE.MatchString(screen) {
		vetoes |= VetoComposerBusy
	}

	observation := Observation{
		Vetoes:      vetoes,
		Fingerprint: Fingerprint(screen),
		StatusLine:  boundStatusLine(statusLine),
	}

	switch {
	case vetoes&VetoBlockingPrompt != 0:
		// A screen waiting on the user is reported as such even when it also
		// carries a working hint; "blocked" is the more actionable reading and
		// neither one settles.
		observation.State = Blocked
	case vetoes&(VetoLiveStatusLine|VetoInterruptHint) != 0:
		observation.State = Working
	case vetoes != 0:
		// Only VetoComposerBusy remains: queued input, a screen scrolled past
		// its composer, or a placeholder this file does not know. None of them
		// says anything about the turn, so the fail-closed default stands.
		observation.State = Unknown
	case ClassifyCodexCompletedTurnScreen(screen) || ClassifyCodexInterruptedTurnScreen(screen):
		observation.State = Terminal
		observation.Tier = TierTerminalExplicit
	default:
		// Veto-free but with no positive terminal marker. Claude would call this
		// quiescent and settle it slowly; Codex will not settle it at all,
		// because Codex always prints a marker when a turn ends.
		observation.State = Unknown
	}
	return observation
}

// codexLatestStatusLine scans every line and returns the LAST recognized status
// line's kind and text, mirroring the classifiers' latest-status-line-wins scan
// below. It exists for the veto and for Observation.StatusLine; the settle
// decision stays with the two classifiers so the positional composer
// requirement has exactly one implementation.
func codexLatestStatusLine(screen string) (codexStatusKind, string) {
	kind := codexStatusNone
	text := ""
	for _, line := range strings.Split(screen, "\n") {
		switch {
		case codexWorkedForLineRE.MatchString(line), codexInterruptedLineRE.MatchString(line):
			kind, text = codexStatusTerminal, line
		case codexLineLooksLiveWork(line):
			kind, text = codexStatusWorking, line
		}
	}
	return kind, text
}

// codexLineLooksLiveWork reports whether one line is positive evidence of an
// in-flight Codex turn.
//
// The two word-independent patterns sit alongside the word-dependent
// codexWorkingStatusLineRE rather than replacing it, so renaming the spinner
// cannot silently disable the classifier: a renamed spinner still ticks a timer
// and still names a key to interrupt, and either one is enough.
func codexLineLooksLiveWork(line string) bool {
	return codexWorkingStatusLineRE.MatchString(line) ||
		codexLiveElapsedStatusLineRE.MatchString(line) ||
		codexInterruptHintRE.MatchString(line)
}

// ClassifyCodexCompletedTurnScreen reports whether a Codex screen scrape shows a
// finished assistant turn that is safe to treat as a stop transition.
//
// It is deliberately conservative: false negatives are acceptable, false
// positives are not. It returns true only when the screen is not a visible
// prompt, the latest visible Codex status line is a completed "Worked for …"
// line (with no later live-work line), and an empty composer prompt appears
// after that status line. This mirrors the completed-turn state Codex leaves on
// screen when the assistant finishes but no Stop hook arrives.
func ClassifyCodexCompletedTurnScreen(screen string) bool {
	return codexTerminalTurnScreen(screen, codexWorkedForLineRE)
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
	return codexTerminalTurnScreen(screen, codexInterruptedLineRE)
}

// codexTerminalTurnScreen is the shared body of the two classifiers above: the
// only difference between them was which terminal marker they scanned for.
//
// THE POSITIONAL REQUIREMENT IS LOAD-BEARING. The composer scan starts at
// markerIdx+1, not at zero: "an empty composer somewhere on screen" is
// satisfied by a composer rendered ABOVE a status line that is still running,
// which is precisely the screen this classifier must refuse. Do not relax the
// starting index to a whole-screen match.
//
// The prompt classification is checked first, before any status-line work. That
// ordering is what makes codexInterruptHintRE's overlap with Codex's
// request_user_input footer free.
func codexTerminalTurnScreen(screen string, markerRE *regexp.Regexp) bool {
	if _, ok := ClassifyCodexPrompt(screen); ok {
		return false
	}

	lines := strings.Split(screen, "\n")
	markerIdx := -1
	latestStatusIsMarker := false
	for i, line := range lines {
		switch {
		case markerRE.MatchString(line):
			markerIdx = i
			latestStatusIsMarker = true
		case codexLineLooksLiveWork(line):
			latestStatusIsMarker = false
		}
	}
	if markerIdx < 0 || !latestStatusIsMarker {
		return false
	}

	for i := markerIdx + 1; i < len(lines); i++ {
		if codexComposerLineRE.MatchString(lines[i]) {
			return true
		}
	}
	return false
}

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
	if !CodexScreenLooksCleared(screen) {
		return false
	}
	return codexScrollbackLooksCleared(allText)
}

// CodexScreenLooksCleared reports whether the CURRENT screen alone looks
// cleared. It is exported for the clear probe's cheap first pass, which uses it
// to decide whether the second, more expensive `--extent=all` read is worth
// making at all.
func CodexScreenLooksCleared(screen string) bool {
	if !codexComposerLineRE.MatchString(screen) {
		return false
	}
	if !codexTextHasContextZero(screen) {
		return false
	}
	if _, ok := ClassifyCodexPrompt(screen); ok {
		return false
	}
	return !codexTextHasTranscriptMarkers(screen)
}

func codexScrollbackLooksCleared(allText string) bool {
	if strings.TrimSpace(allText) == "" {
		return false
	}
	if _, ok := ClassifyCodexPrompt(allText); ok {
		return false
	}

	hasComposer := false
	hasContextZero := false
	for _, line := range strings.Split(allText, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if codexComposerLineRE.MatchString(line) {
			hasComposer = true
			continue
		}
		if codexLineLooksTranscriptMarker(line) {
			return false
		}
		if codexContextZeroLineRE.MatchString(line) {
			hasContextZero = true
		}
	}
	return hasComposer && hasContextZero
}

func codexTextHasContextZero(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if codexContextZeroLineRE.MatchString(line) {
			return true
		}
	}
	return false
}

func codexTextHasTranscriptMarkers(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if codexLineLooksTranscriptMarker(line) {
			return true
		}
	}
	return false
}

func codexLineLooksTranscriptMarker(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	switch {
	case trimmed == "":
		return false
	case codexComposerLineRE.MatchString(line):
		return false
	case codexLineLooksChrome(trimmed):
		return false
	case strings.HasPrefix(trimmed, "\u203a") || strings.HasPrefix(trimmed, ">"):
		return true
	case strings.HasPrefix(trimmed, "\u2022") || strings.HasPrefix(trimmed, "\u25e6"):
		return true
	case strings.HasPrefix(trimmed, "\u258c"):
		return true
	case strings.HasPrefix(trimmed, "\u2514") || strings.HasPrefix(trimmed, "\u251c"):
		return true
	case strings.HasPrefix(trimmed, "\u2502"):
		return true
	case codexLabeledTranscriptLineRE.MatchString(trimmed):
		return true
	case strings.HasPrefix(lower, "ran ") || strings.HasPrefix(lower, "explored"):
		return true
	default:
		return false
	}
}

func codexLineLooksChrome(trimmed string) bool {
	if strings.Trim(trimmed, "\u2500") == "" {
		return true
	}
	if strings.Trim(trimmed, "\u2500\u2502\u256d\u256e\u2570\u256f") == "" {
		return true
	}
	return strings.HasPrefix(trimmed, "\u2502") && strings.HasSuffix(trimmed, "\u2502")
}
