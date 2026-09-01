package screenstate

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/patternset"
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
// word-independent vetoes are added on top; see the live_elapsed_status_line
// and interrupt_hint clauses.

// The UI copy this adapter keys on lives in patterns_codex.toml, loaded by
// patterns.go and overridable per user. The clause names below are the contract
// between that file and this one; each is documented at its definition site
// there, where the failures it was tuned against are recorded.
//
// FAIL-CLOSED ORIENTATION, checked clause by clause. A clause the installed set
// does not define reports no match, and for Codex that lands as follows:
//
//   - composer_line / composer_placeholder missing: the composer is unmatchable,
//     so Classify raises VetoComposerBusy and both turn classifiers refuse.
//     Withholds settling. A composer_line that is PRESENT but has no capture
//     group is the opposite and is not absence at all — see the submatch
//     contract in codexRequirements.
//   - worked_for_line, completed_marker_line, interrupted_line missing: no
//     positive terminal marker, so nothing settles. Withholds settling.
//   - context_zero_line missing: CodexScreenLooksCleared cannot find its
//     positive evidence and returns false. Withholds the clear transition.
//   - working_status_line, live_elapsed_status_line, interrupt_hint,
//     background_terminal_footer missing: these are ONE-WAY vetoes, and a
//     missing one weakens the refusal. They sit alongside each other so that no
//     single clause is load-bearing for a LIVE screen — but liveWork is also
//     what invalidates a completed marker scrolled above a resumed turn, so
//     losing all of them settles that turn. codexRequirements therefore makes
//     each of the four mandatory in an override rather than leaving the
//     redundancy to carry them.
//   - labeled_transcript_line missing: this one is a NEGATIVE test, and dropping
//     it would make a transcript-bearing scrollback look cleared.
//     CodexScreenLooksCleared refuses outright when the clause is absent rather
//     than classifying with a weakened test, and it is required besides.
const (
	codexClauseComposerLine        = "composer_line"
	codexClauseComposerPlaceholder = "composer_placeholder"
	codexClauseContextZeroLine     = "context_zero_line"
	codexClauseLabeledTranscript   = "labeled_transcript_line"
	codexClauseWorkedForLine       = "worked_for_line"
	codexClauseCompletedMarker     = "completed_marker_line"
	codexClauseWorkingStatusLine   = "working_status_line"
	codexClauseInterruptHint       = "interrupt_hint"
	codexClauseLiveElapsed         = "live_elapsed_status_line"
	codexClauseBackgroundFooter    = "background_terminal_footer"
	codexClauseInterruptedLine     = "interrupted_line"
)

// codexRequirements is the subset of the clauses above and in codex_prompt.go
// whose absence removes a refusal rather than weakening a claim. The loader
// refuses to install an override that does not satisfy it; see
// claudeRequirements for the shape and patterns.go for the enforcement.
//
// It supersedes nothing: the two hand-written guards in this file
// (labeledRE == nil, codexPromptClausesDefined) stay, because they defend the
// same clauses against a set that never went through Load.
//
// The clauses left OUT are the positive-evidence ones — worked_for_line,
// completed_marker_line, interrupted_line, context_zero_line,
// composer_placeholder. Codex settles only on a positive marker, so losing one
// costs a settle it would otherwise have made; that is the safe direction and
// an override that predates a new marker shape must keep working.
var codexRequirements = append(patternset.Requirements{
	{
		Name: codexClauseComposerLine, Kind: patternset.RegexClause, MinSubmatches: 1,
		Reason: "the classifier reads submatch 1 as the composer's remainder and requires it to " +
			"be empty or a known placeholder; a regex with no capture group hands it an empty " +
			"remainder for EVERY matching line, so any chevron-prefixed transcript line reads " +
			"as an idle composer",
	},
	{
		Name: codexClauseLabeledTranscript, Kind: patternset.RegexClause,
		Reason: "it is a NEGATIVE test — the evidence that a screen still holds transcript " +
			"content — so without it a transcript-bearing scrollback looks cleared",
	},
	{
		Name: codexClauseWorkingStatusLine, Kind: patternset.RegexClause,
		Reason: "it is one of the three live-work discriminators that invalidate a completed " +
			"marker scrolled above a resumed turn",
	},
	{
		Name: codexClauseLiveElapsed, Kind: patternset.RegexClause,
		Reason: "it is the word-independent live-work discriminator, the one that survives Codex " +
			"renaming its spinner",
	},
	{
		Name: codexClauseInterruptHint, Kind: patternset.RegexClause,
		Reason: "it is the third live-work discriminator and Codex renders it on every live turn",
	},
	{
		Name: codexClauseBackgroundFooter, Kind: patternset.AnyClause,
		Reason: "it is the only evidence that a pane whose model turn genuinely ended is still " +
			"running a background terminal, which is the one case where the terminal markers " +
			"are honest and settling is still wrong",
	},
}, codexPromptRequirements...)

// codexClauses is one pattern set resolved to the compiled clauses this file
// scans with.
//
// Every classifier here walks a screen line by line, so the clause lookups are
// hoisted out of those loops exactly once per screen: externalizing the
// patterns must not turn a per-screen scan into a per-line map lookup. It holds
// no policy — which clause is consulted when, and in what combination, stays in
// the functions below.
type codexClauses struct {
	// set is the pattern set these clauses came from, retained for the
	// whole-screen clauses that are evaluated through Patterns.Match rather
	// than as a compiled regex — the prompt clauses in codex_prompt.go, whose
	// groups have no single compiled form to hoist.
	set               Patterns
	composerLineRE    *regexp.Regexp
	placeholders      []string
	contextZeroRE     *regexp.Regexp
	labeledRE         *regexp.Regexp
	workedForRE       *regexp.Regexp
	completedMarkerRE *regexp.Regexp
	workingRE         *regexp.Regexp
	interruptHintRE   *regexp.Regexp
	liveElapsedRE     *regexp.Regexp
	interruptedRE     *regexp.Regexp
}

func codexClausesFor(patterns Patterns) codexClauses {
	return codexClauses{
		set:               patterns,
		composerLineRE:    patterns.Regexp(codexClauseComposerLine),
		placeholders:      patterns.Phrases(codexClauseComposerPlaceholder),
		contextZeroRE:     patterns.Regexp(codexClauseContextZeroLine),
		labeledRE:         patterns.Regexp(codexClauseLabeledTranscript),
		workedForRE:       patterns.Regexp(codexClauseWorkedForLine),
		completedMarkerRE: patterns.Regexp(codexClauseCompletedMarker),
		workingRE:         patterns.Regexp(codexClauseWorkingStatusLine),
		interruptHintRE:   patterns.Regexp(codexClauseInterruptHint),
		liveElapsedRE:     patterns.Regexp(codexClauseLiveElapsed),
		interruptedRE:     patterns.Regexp(codexClauseInterruptedLine),
	}
}

// codexPatterns is the installed Codex pattern set, for the package-level
// classifiers that are called without an adapter.
func codexPatterns() Patterns {
	set, _ := PatternsFor(harness.Codex)
	return set
}

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
type codexAdapter struct {
	// Patterns pins this adapter to one pattern set. The registered adapter
	// leaves it zero and resolves the installed set per Classify instead:
	// the adapters map is built at package init and read lock-free by For(),
	// so a set captured into it would be the embedded default forever and
	// Load's override would have no effect. Tests set it to classify against
	// a pattern set without installing one globally.
	Patterns Patterns
}

// Harness returns Codex's canonical harness name.
func (codexAdapter) Harness() string { return harness.Codex }

// patterns resolves the pattern set this Classify call reads.
func (a codexAdapter) patterns() Patterns {
	if a.Patterns.Harness() != "" {
		return a.Patterns
	}
	return codexPatterns()
}

// Classify reads a Codex screen capture.
//
// The Adapter contract holds here exactly as it does for Claude: every veto is
// computed first and unconditionally, and positive terminal evidence is
// consulted only on a veto-free screen.
//
// TIERS. Both Codex terminal markers — the worked-for line and the
// interrupted-turn marker — are literal, word-anchored and written by the
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
func (a codexAdapter) Classify(screen string) Observation {
	patterns := a.patterns()
	clauses := codexClausesFor(patterns)
	lower := strings.ToLower(screen)
	kind, statusLine := clauses.latestStatusLine(screen)

	var vetoes Veto
	if kind == codexStatusWorking {
		vetoes |= VetoLiveStatusLine
	}
	// Unlike Claude, Codex still renders this hint on every live turn, so it is
	// genuinely useful here rather than a legacy backstop. It remains a one-way
	// veto: its absence is not evidence a turn ended.
	if patterns.Match(codexClauseInterruptHint, screen, lower) {
		vetoes |= VetoInterruptHint
	}
	// Independent of every other signal on this screen, and deliberately so. A
	// running background terminal is the one case where Codex's terminal markers
	// are HONEST and settling is still wrong: the model's turn genuinely ended,
	// so the marker, the absence of a live status line and the empty composer all
	// agree, and nothing else here would refuse. The footer is the only evidence
	// that work the pane started is still going.
	if patterns.Match(codexClauseBackgroundFooter, screen, lower) {
		vetoes |= VetoBackgroundTask
	}
	// The adapter's own pattern set and its already-folded screen, rather than
	// the package-level ClassifyCodexPrompt: an adapter pinned to a set must
	// classify prompts against that set too, and the fold is not worth making
	// twice.
	if _, ok := classifyCodexPrompt(patterns, screen, lower); ok {
		vetoes |= VetoBlockingPrompt
	}
	if !clauses.screenHasComposerLine(screen) {
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
	case codexTerminalTurnScreen(clauses, screen, codexClauses.completedTurnMarkerAt) ||
		codexTerminalTurnScreen(clauses, screen, codexClauses.interruptedMarkerAt):
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
