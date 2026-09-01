package contextlimit

import (
	_ "embed"
	"sync"

	"github.com/sglavoie/agentmux/internal/patternset"
)

// The UI copy this package keys on lives in patterns_contextlimit.toml. The
// file sits here rather than beside the other pattern files in
// internal/screenstate because screenstate imports THIS package
// (screenstate/claude.go, for the context-limit veto), so a set decoded there
// could not be read from here without an import cycle.
//
// Override handling still belongs to screenstate.Load, which finds
// <config>/screenstate/contextlimit.toml, decodes it through the same schema
// and hands the result back via InstallPatterns. That keeps one override
// directory, one reload story and one warning vocabulary for every pattern file
// the screen classifier consults.
//
//go:embed patterns_contextlimit.toml
var defaultPatterns []byte

// PatternsName is the name of this package's pattern set: the value its file
// must declare in `harness =`, and the basename of its override file. It is
// exported for screenstate.Load, which carries the set through the same
// override path as the real harnesses.
const PatternsName = "contextlimit"

// Clause names, the contract between patterns_contextlimit.toml and this
// package.
//
// A clause the installed set does not define reports no match. Neither
// direction fabricates state — an absent clause can never invent a banner —
// but the two are not equally safe, and Requirements below is where that
// difference is written down.
//
// For a chrome clause, absence means "this line is not chrome", which leaves
// the line in the tail and can only make the banner harder to find; that
// matches isUIChromeLine's stated preference for false negatives over false
// positives, so the chrome clauses are optional.
//
// For a banner clause, absence means "no context-limit banner on this screen",
// and that is NOT the neutral answer it looks like. This package's only caller
// in the classifier is screenstate/claude.go, where a marker raises
// VetoContextLimit; a context-limited pane is quiet and has an empty composer,
// so with the veto gone it settles as quiescent. Losing detection here is
// losing a refusal, which is why every clause hasMarker's conditional reads is
// mandatory in an override.
const (
	clauseLimitReachedBanner = "limit_reached_banner"
	clauseWindowFullBanner   = "window_full_banner"
	clauseCompactOrClear     = "compact_or_clear_hint"
	clauseCompactCommand     = "compact_command"
	clauseClearCommand       = "clear_command"
	clauseContinueHint       = "continue_hint"

	clauseChromePromptRune     = "chrome_prompt_rune"
	clauseChromeModeHint       = "chrome_mode_hint"
	clauseChromeShortcutPrefix = "chrome_shortcut_prefix"
	clauseChromeShortcutHint   = "chrome_shortcut_hint"
	clauseChromeFooterLabel    = "chrome_footer_label"
)

// clauseNames is every clause the Go side reads, for the coverage guard in
// patterns_test.go.
var clauseNames = []string{
	clauseLimitReachedBanner,
	clauseWindowFullBanner,
	clauseCompactOrClear,
	clauseCompactCommand,
	clauseClearCommand,
	clauseContinueHint,
	clauseChromePromptRune,
	clauseChromeModeHint,
	clauseChromeShortcutPrefix,
	clauseChromeShortcutHint,
	clauseChromeFooterLabel,
}

// Requirements is this pattern set's semantic contract, exported for
// screenstate.Load — the one loader that handles this package's override file.
// It names the six clauses hasMarker's conditional reads, each of which is a
// refusal rather than a claim; see the fail direction note above.
//
// The chrome clauses are deliberately absent from it. They only decide which
// lines screenShowsMarker strips before looking for the banner, and dropping
// one leaves more text in the tail rather than less.
//
// Every entry is PhraseClause because this package resolves its clauses with
// Patterns.Phrases: a name defined under [regex] resolves to nil here and the
// check would silently disappear, which is precisely the mistake the kind
// exists to catch.
func Requirements() patternset.Requirements {
	return patternset.Requirements{
		{
			Name: clauseLimitReachedBanner, Kind: patternset.PhraseClause,
			Reason: "it is one of the two banner headlines; with neither, no context-limit " +
				"screen is ever detected and a context-limited pane settles as quiescent",
		},
		{
			Name: clauseWindowFullBanner, Kind: patternset.PhraseClause,
			Reason: "it is the other banner headline, and the one the current Claude release renders",
		},
		{
			Name: clauseCompactCommand, Kind: patternset.PhraseClause,
			Reason: "the limit-reached headline is only a banner when a recovery command is on " +
				"screen with it, so without this clause that whole branch can never match",
		},
		{
			Name: clauseCompactOrClear, Kind: patternset.PhraseClause,
			Reason: "it is the short-form recovery hint, one of the two ways the window-full " +
				"branch confirms a banner",
		},
		{
			Name: clauseClearCommand, Kind: patternset.PhraseClause,
			Reason: "it is part of the long-form recovery conjunction, the window-full branch's " +
				"other confirmation",
		},
		{
			Name: clauseContinueHint, Kind: patternset.PhraseClause,
			Reason: "it completes the long-form recovery conjunction",
		},
	}
}

// installedMu guards the installed set, which screenstate.Load replaces on the
// daemon's startup goroutine while probe goroutines read it.
var (
	installedMu sync.RWMutex
	installed   = embeddedOrEmpty()
)

// EmbeddedPatterns decodes the pattern file shipped in the binary. screenstate
// calls it to fold this set into the one override pass; the error is a
// build-time bug in this repository rather than anything a user did, which is
// why it is reported rather than panicked on a startup path.
func EmbeddedPatterns() (patternset.Patterns, error) {
	set, err := patternset.Decode(defaultPatterns, PatternsName)
	if err != nil {
		return patternset.Patterns{}, err
	}
	return set.WithSource(patternset.SourceEmbedded), nil
}

// InstallPatterns replaces the installed set. It is a startup call: like
// screenstate.Load, whose install path is its only caller, it must run before
// any probe goroutine classifies a screen.
func InstallPatterns(set patternset.Patterns) {
	installedMu.Lock()
	defer installedMu.Unlock()
	installed = set
}

// InstalledPatterns returns the set in force. Diagnostics and tests only; the
// detection functions resolve it themselves.
func InstalledPatterns() patternset.Patterns {
	installedMu.RLock()
	defer installedMu.RUnlock()
	return installed
}

// embeddedOrEmpty installs the embedded default at package init so HasMarker
// works in a binary that never links screenstate — internal/hookevent is one.
// A file that fails to decode degrades to the empty set, matching how
// screenstate skips an invalid embedded file rather than starting half-built;
// TestEmbeddedPatternsDecode is what stops that from shipping.
func embeddedOrEmpty() patternset.Patterns {
	set, err := EmbeddedPatterns()
	if err != nil {
		return patternset.Patterns{}
	}
	return set
}

// clauses is one screen's worth of resolved phrase lists.
//
// It exists so a screen scan resolves the pattern set once instead of once per
// line: ScreenShowsMarker calls the chrome test per line and the marker test
// per candidate window, and both would otherwise take the RWMutex and walk the
// clause maps every time. This mirrors codexClauses in screenstate/codex.go.
type clauses struct {
	limitReachedBanner []string
	windowFullBanner   []string
	compactOrClear     []string
	compactCommand     []string
	clearCommand       []string
	continueHint       []string

	chromePromptRune     []string
	chromeModeHint       []string
	chromeShortcutPrefix []string
	chromeShortcutHint   []string
	chromeFooterLabel    []string
}

func clausesFor(set patternset.Patterns) clauses {
	return clauses{
		limitReachedBanner:   set.Phrases(clauseLimitReachedBanner),
		windowFullBanner:     set.Phrases(clauseWindowFullBanner),
		compactOrClear:       set.Phrases(clauseCompactOrClear),
		compactCommand:       set.Phrases(clauseCompactCommand),
		clearCommand:         set.Phrases(clauseClearCommand),
		continueHint:         set.Phrases(clauseContinueHint),
		chromePromptRune:     set.Phrases(clauseChromePromptRune),
		chromeModeHint:       set.Phrases(clauseChromeModeHint),
		chromeShortcutPrefix: set.Phrases(clauseChromeShortcutPrefix),
		chromeShortcutHint:   set.Phrases(clauseChromeShortcutHint),
		chromeFooterLabel:    set.Phrases(clauseChromeFooterLabel),
	}
}

// installedClauses resolves the clauses of the set currently in force.
func installedClauses() clauses { return clausesFor(InstalledPatterns()) }
