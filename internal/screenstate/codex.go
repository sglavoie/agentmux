package screenstate

import (
	"regexp"
	"strings"
	"unicode/utf8"

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
// satisfy the terminal-screen classifiers. The whole line must be the chevron
// plus one KNOWN placeholder and nothing else — that end anchor is what stops
// "› Run the shell command: sleep 8" (a real transcript line in
// testdata/codex_idle_completed_t0.txt) from reading as an empty composer.
//
// ACCEPTED RESIDUAL BRITTLENESS. A Codex build that ships an unknown
// placeholder makes the composer unmatchable and withholds settling — a stuck
// ⚡, never a false 🛑 — which is the direction this package fails in by design.
// TestCodexCurrentScreenContract pins the currently installed placeholder, and
// TestCodexComposerPlaceholderIsNarrow pins the narrowness that costs.
var codexComposerLineRE = regexp.MustCompile(`(?m)^\s*(?:\x{203a}|>)\s*(?:` +
	`(?:Use /skills to list available skills)|` +
	`(?:Find and fix a bug in @filename)|` +
	`(?:Ask Codex to do anything)` +
	`)?\s*$`)
var codexContextZeroLineRE = regexp.MustCompile(`(?i)\bcontext\b.*(?:^|[^0-9])0\s*%`)
var codexLabeledTranscriptLineRE = regexp.MustCompile(`(?i)^\s*(?:user|codex|assistant|system)\s*:`)

// codexWorkedForLineRE matches Codex's completed-turn status line ("Worked for
// 1m 30s"). The trailing digit requirement anchors on a real duration so
// transcript prose such as "worked for the previous release" cannot match.
//
// It matches nothing on a codex-cli 0.149.x screen: that build dropped the
// line entirely in favour of the framed marker below. It is retained for older
// Codex builds, which a user may still be running.
var codexWorkedForLineRE = regexp.MustCompile(`(?i)^[^\p{L}\p{N}]*worked for\s+\d`)

// codexCompletedMarkerLineRE matches codex-cli 0.149.x's end-of-turn marker
// line. That build prints no duration at all: the whole line is a bullet glyph
// and the single word "Completed", optionally with a period.
//
// A one-word marker is far weaker evidence than "Worked for 1m 30s" — an
// assistant message reading "Completed." would satisfy it on its own — so it is
// NEVER honoured by itself. codexCompletedMarkerAt additionally requires the
// horizontal rules Codex draws above and below it, and that framing is what
// separates the harness-authored marker from transcript prose.
var codexCompletedMarkerLineRE = regexp.MustCompile(`(?i)^[^\p{L}\p{N}]*completed\.?\s*$`)

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

// codexBackgroundTerminalFooterRE matches Codex's running-background-work
// footer ("1 background terminal running · /ps to view · /stop to close"). Codex
// renders it both appended to a live "Working (…)" status line and on its own
// line under a finished or interrupted turn — the second shape is the one that
// matters, because there the model's turn really is over while a detached
// process it started is not.
//
// THE COUNT IS THE ANCHOR, and it must be non-zero. Codex's own transcript prose
// says things like "• Stopping all background terminals." (a real line in
// testdata/codex_idle_completed_t0.txt); requiring "<n> background <noun>
// running" is what keeps narration from pinning a pane at ⚡ forever.
//
// The noun is matched loosely on purpose. This is a ONE-WAY veto, so a rename
// from "terminal" to "task" costing a false 🛑 over live background work is the
// expensive failure; matching a synonym Codex does not use today costs nothing.
// "task" is not speculative — it is what Claude's own background manager calls
// these (see claude.go's "ctrl+b to" veto).
var codexBackgroundTerminalFooterRE = regexp.MustCompile(
	`(?i)\b[1-9]\d*\s+background\s+(?:terminal|task)s?\s+running\b`)

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
	// Independent of every other signal on this screen, and deliberately so. A
	// running background terminal is the one case where Codex's terminal markers
	// are HONEST and settling is still wrong: the model's turn genuinely ended,
	// so the marker, the absence of a live status line and the empty composer all
	// agree, and nothing else here would refuse. The footer is the only evidence
	// that work the pane started is still going.
	if codexBackgroundTerminalFooterRE.MatchString(screen) {
		vetoes |= VetoBackgroundTask
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
	case vetoes&(VetoLiveStatusLine|VetoInterruptHint|VetoBackgroundTask) != 0:
		// Working, not Unknown, and the background footer is why the distinction
		// is worth stating: a pane whose model turn ended but whose background
		// terminal is still running reads to an operator as ⚡ rather than 🛑,
		// which is exactly the truth. Same grading as Claude's twin veto.
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
	lines := strings.Split(screen, "\n")
	kind := codexStatusNone
	text := ""
	for i, line := range lines {
		switch {
		case codexTerminalMarkerAt(lines, i):
			kind, text = codexStatusTerminal, line
		case codexLineLooksLiveWork(line):
			kind, text = codexStatusWorking, line
		}
	}
	return kind, text
}

// codexMarkerFunc reports whether line i of a screen is a particular
// terminal-turn marker.
//
// The two turn classifiers take one of these rather than a bare *regexp
// because codex-cli 0.149.x's "Completed" marker is not decidable from its own
// line: it is one common word, and only the horizontal rules Codex draws around
// it make it harness-authored evidence rather than assistant prose.
type codexMarkerFunc func(lines []string, i int) bool

// codexTerminalMarkerAt is the union of every terminal marker this file knows,
// used by the diagnostic status-line scan. The settle decision still goes
// through the two classifiers, which keep completed and interrupted apart.
func codexTerminalMarkerAt(lines []string, i int) bool {
	return codexCompletedTurnMarkerAt(lines, i) || codexInterruptedMarkerAt(lines, i)
}

// codexCompletedTurnMarkerAt accepts either completed-turn shape: the older
// "Worked for N" status line or 0.149.x's framed "Completed" marker.
func codexCompletedTurnMarkerAt(lines []string, i int) bool {
	return codexWorkedForLineRE.MatchString(lines[i]) || codexCompletedMarkerAt(lines, i)
}

func codexInterruptedMarkerAt(lines []string, i int) bool {
	return codexInterruptedLineRE.MatchString(lines[i])
}

// codexCompletedMarkerAt reports whether line i is 0.149.x's framed end-of-turn
// marker: the bare "Completed" line WITH a horizontal rule above and below it.
//
// THE FRAMING IS THE EVIDENCE. Without it this marker is one ordinary English
// word at the start of a bullet line, which any assistant turn can print. Do
// not relax this to the line match alone.
func codexCompletedMarkerAt(lines []string, i int) bool {
	if !codexCompletedMarkerLineRE.MatchString(lines[i]) {
		return false
	}
	return codexRuleBefore(lines, i, -1) && codexRuleBefore(lines, i, +1)
}

// codexRuleBefore walks away from line i in one direction and reports whether
// the first non-blank line it meets is a horizontal rule. Codex separates the
// marker from its rules with blank lines, so blanks are skipped; anything else
// ends the walk and is the answer.
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
func codexLineIsHorizontalRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.Trim(trimmed, "─") != "" {
		return false
	}
	return utf8.RuneCountInString(trimmed) >= codexHorizontalRuleMinimumWidth
}

const codexHorizontalRuleMinimumWidth = 8

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
// prompt, the latest visible Codex status line is a completed-turn marker (with
// no later live-work line), and an empty composer prompt appears after that
// marker. This mirrors the completed-turn state Codex leaves on screen when the
// assistant finishes but no Stop hook arrives.
//
// Two marker shapes are accepted, because Codex changed the one it prints: the
// older "Worked for 1m 30s" status line, and 0.149.x's framed "Completed"
// marker. Every other gate is identical for both.
func ClassifyCodexCompletedTurnScreen(screen string) bool {
	return codexTerminalTurnScreen(screen, codexCompletedTurnMarkerAt)
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
	return codexTerminalTurnScreen(screen, codexInterruptedMarkerAt)
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
func codexTerminalTurnScreen(screen string, marker codexMarkerFunc) bool {
	if _, ok := ClassifyCodexPrompt(screen); ok {
		return false
	}

	lines := strings.Split(screen, "\n")
	markerIdx := -1
	latestStatusIsMarker := false
	for i, line := range lines {
		switch {
		case marker(lines, i):
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
