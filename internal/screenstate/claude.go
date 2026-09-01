package screenstate

import (
	"regexp"
	"strings"

	"github.com/sglavoie/agentmux/internal/contextlimit"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/patternset"
)

// This file classifies a Claude Code screen scrape. Native hooks are primary,
// but Claude documents that Stop does not fire after a user interruption, so a
// stuck ⚡ can outlive the turn that produced it. The daemon reads Claude's own
// framed input area to settle that stale interactive work.
//
// The classifier is deliberately conservative — false negatives (failing to
// settle a truly idle screen) are acceptable, false positives (settling a live
// or blocked session) are not.

// The UI copy this adapter keys on lives in patterns_claude.toml, loaded by
// patterns.go and overridable per user. The clause names below are the contract
// between that file and this one; each is documented at its definition site,
// where the failures it was tuned against are recorded.
//
// FAIL-CLOSED ORIENTATION, and where it does NOT hold. A clause the installed
// set does not define reports no match. For the composer clause that direction
// is safe on its own — a missing one vetoes with VetoComposerBusy — but for
// every clause that FEEDS a veto it is the wrong way round: no match means no
// veto, and a screen with no vetoes is exactly the screen this adapter settles.
// working_status_line is the sharpest case, being the only source of
// VetoLiveStatusLine: drop it and a live turn on a framed empty composer
// classifies Terminal/TierQuiescent and stops a running pane.
//
// So absence is not left to degrade gracefully here. claudeRequirements below
// names every such clause, and both the loader and the settle path in Classify
// refuse rather than classify with a veto they cannot compute.
const (
	claudeClauseComposerLine   = "composer_line"
	claudeClauseChoiceLine     = "choice_line"
	claudeClauseInterrupted    = "interrupted_status_line"
	claudeClauseWorking        = "working_status_line"
	claudeClauseCompleted      = "completed_status_line"
	claudeClauseInterruptHint  = "interrupt_hint"
	claudeClauseBackgroundTask = "background_task"
	claudeClauseBlockingPrompt = "blocking_prompt"
)

// claudeRequirements is the subset of the clauses above whose absence is not
// merely a lost check but a lost REFUSAL. Every one of them is the sole source
// of a veto, so a set missing one classifies a screen that should never settle
// as a quiet one — the failure mode a pattern override can otherwise introduce
// silently, because the file still parses and the daemon still starts.
//
// The three clauses NOT listed are deliberate. completed_status_line and
// interrupted_status_line only GRADE a settle that has already survived every
// veto: losing one drops the observation to TierQuiescent, the weakest tier,
// which makes the probe spend more stabilization samples rather than fewer.
// composer_placeholder has no Claude analog. An override written against an
// older release therefore still loads as long as it kept the refusals.
//
// Two things consume this list. patterns.go refuses to INSTALL an override that
// does not satisfy it, which is where a real user's mistake is caught; Classify
// refuses to SETTLE against a set that does not satisfy it, which covers every
// other way a set can reach an adapter — a pinned test set, a future caller, a
// contract that drifts from the loader.
var claudeRequirements = patternset.Requirements{
	{
		Name: claudeClauseWorking, Kind: patternset.RegexClause,
		Reason: "it is the only source of the live-status veto, so without it a running turn " +
			"on an empty composer reads as quiescent and settles to a false stop",
	},
	{
		Name: claudeClauseComposerLine, Kind: patternset.AnyClause,
		Reason: "it is the only evidence a pane is sitting at a ready prompt; without it every " +
			"screen is composer_busy and the pane strands on its current icon forever",
	},
	{
		Name: claudeClauseChoiceLine, Kind: patternset.AnyClause,
		Reason: "it is half the blocking-prompt veto, and the half that survives a reworded prompt",
	},
	{
		Name: claudeClauseBlockingPrompt, Kind: patternset.AnyClause,
		Reason: "it is the other half of the blocking-prompt veto; without both, a pane waiting " +
			"on a permission answer can settle",
	},
	{
		Name: claudeClauseInterruptHint, Kind: patternset.AnyClause,
		Reason: "it is the only source of the interrupt-hint veto, the legacy backstop for builds " +
			"whose live turn has no status line at all",
	},
	{
		Name: claudeClauseBackgroundTask, Kind: patternset.AnyClause,
		Reason: "it is the only source of the background-task veto, which is the sole evidence " +
			"that an idle-looking pane still owns running work",
	},
}

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
type claudeAdapter struct {
	// Patterns pins this adapter to one pattern set. The registered adapter
	// leaves it zero and resolves the installed set per Classify instead:
	// the adapters map is built at package init and read lock-free by For(),
	// so a set captured into it would be the embedded default forever and
	// Load's override would have no effect. Tests set it to classify against
	// a pattern set without installing one globally.
	Patterns Patterns
}

// Harness returns Claude's canonical harness name.
func (claudeAdapter) Harness() string { return harness.Claude }

// patterns resolves the pattern set this Classify call reads.
func (a claudeAdapter) patterns() Patterns {
	if a.Patterns.Harness() != "" {
		return a.Patterns
	}
	set, _ := PatternsFor(harness.Claude)
	return set
}

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
func (a claudeAdapter) Classify(screen string) Observation {
	patterns := a.patterns()
	kind, statusLine := claudeLatestStatusLine(patterns, screen)

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
	if patterns.Match(claudeClauseInterruptHint, screen, lower) {
		vetoes |= VetoInterruptHint
	}
	if patterns.Match(claudeClauseBackgroundTask, screen, lower) {
		vetoes |= VetoBackgroundTask
	}
	if claudeScreenHasBlockingPrompt(patterns, lower, screen) {
		vetoes |= VetoBlockingPrompt
	}
	if contextlimit.ScreenShowsMarker(screen) {
		vetoes |= VetoContextLimit
	}
	if !patterns.Match(claudeClauseComposerLine, screen, lower) {
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
	case !claudeRequirements.Satisfied(patterns):
		// Veto-free, but only because this pattern set cannot compute some of
		// the vetoes. That is not the same fact and must not be reported as one:
		// an empty veto set from an incomplete set is silence, not evidence.
		//
		// Refusing here rather than in the veto block is deliberate, and matches
		// codexTerminalTurnScreen's prompt-clause guard: Classify may not invent
		// a Working or Blocked pane out of a clause it cannot read — that would
		// be a visible lie about the screen — but it may decline to conclude,
		// and declining is what stops the degradation from settling a live turn.
		//
		// The loader normally makes this unreachable in production by refusing
		// the override outright. It stays because an Adapter is a public seam:
		// nothing forces a pattern set to have come through Load.
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
//
// The three clauses are resolved once, outside the loop, because they are the
// same regexes for every line — externalizing them must not turn a per-screen
// scan into a per-line map lookup.
func claudeLatestStatusLine(patterns Patterns, screen string) (claudeStatusKind, string) {
	working := patterns.Regexp(claudeClauseWorking)
	completed := patterns.Regexp(claudeClauseCompleted)
	interrupted := patterns.Regexp(claudeClauseInterrupted)

	kind := claudeStatusNone
	text := ""
	for _, line := range strings.Split(screen, "\n") {
		switch {
		// Working is tested first so that a screen shape not yet seen in a live
		// capture — a working line that somehow also satisfied one of the
		// terminal shapes — resolves to the safe answer.
		case matchesLine(working, line):
			kind, text = claudeStatusWorking, line
		case matchesLine(completed, line):
			kind, text = claudeStatusCompleted, line
		case matchesLine(interrupted, line):
			kind, text = claudeStatusInterrupted, line
		}
	}
	return kind, text
}

// matchesLine is MatchString for a clause that may be absent from the installed
// pattern set. A missing clause matches nothing rather than panicking on the
// probe path.
func matchesLine(re *regexp.Regexp, line string) bool {
	return re != nil && re.MatchString(line)
}

// claudeScreenHasBlockingPrompt reports whether the screen shows a Claude
// permission, elicitation, or other user-blocking choice prompt. The hook path
// normally paints these as key/question, but the screen read is a second line
// of defense so a stuck bolt with a visible prompt is never settled to stop.
//
// The phrase list is English-only and exhaustive by construction; it lives in
// patterns_claude.toml so that limitation has a single home a user can widen
// without a build. The disjunction with the choice-line shape stays here: which
// clauses combine, and how, is Go's. lower is passed in because the caller
// already folded the screen once.
func claudeScreenHasBlockingPrompt(patterns Patterns, lower, screen string) bool {
	return patterns.Match(claudeClauseBlockingPrompt, screen, lower) ||
		patterns.Match(claudeClauseChoiceLine, screen, lower)
}
