package screenstate

import (
	"regexp"
	"strings"

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
