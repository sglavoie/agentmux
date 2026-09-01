package screenstate

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

// The Claude status-line regexes moved into patterns_claude.toml, but
// TestClaudeCompletedStatusLineRejectsWorkingDurations and
// TestClaudeWorkingStatusLineCorpus assert the pattern SHAPES directly rather
// than through Classify — they are the record of the exact lines each shape was
// tuned to accept and reject, and asserting them through the adapter would let a
// veto hide a regex that stopped matching.
//
// Resolving them from the embedded set here keeps those tests byte-identical
// and, more usefully, points them at the shipped pattern file: a typo in the
// TOML now fails the corpus rather than only the fixtures.
var (
	claudeWorkingStatusLineRE   = embeddedClaudeRegexp(claudeClauseWorking)
	claudeCompletedStatusLineRE = embeddedClaudeRegexp(claudeClauseCompleted)
)

// TestClaudeMissingWorkingClauseCannotSettleLiveScreen is the regression for
// the whole point of claudeRequirements, driven through the adapter rather than
// through the loader.
//
// working_status_line is the only source of VetoLiveStatusLine, so a set
// missing it observes a live turn with an EMPTY veto set. That is the trap: an
// empty veto set is what the settle path is looking for, and every other signal
// on the screen — a framed empty composer, no interrupt hint, no prompt —
// agrees that the pane is quiet. Before the guard this classified Terminal /
// TierQuiescent and stopped a running turn.
//
// Patterns.Without is used rather than a hand-written set because it is the one
// thing a real override does that a synthetic set cannot: ship every other
// clause exactly as the release intended and lose only this one.
func TestClaudeMissingWorkingClauseCannotSettleLiveScreen(t *testing.T) {
	embedded, ok := PatternsFor(harness.Claude)
	if !ok {
		t.Fatal("PatternsFor(claude) returned no embedded set")
	}
	live := readScreenFixture(t, "claude_working_pouncing_t0.txt")

	if got := (claudeAdapter{Patterns: embedded}).Classify(live); got.State != Working {
		t.Fatalf("the embedded set reads the live fixture as %s, want working; the fixture "+
			"no longer exercises what this test is about", got.State)
	}

	got := claudeAdapter{Patterns: embedded.Without(claudeClauseWorking)}.Classify(live)
	if got.Vetoes != 0 {
		t.Fatalf("vetoes = %s, want none; some other clause is now carrying the refusal and "+
			"this test would pass without the guard it exists to pin", got.Vetoes)
	}
	if got.SettleableTerminal() {
		t.Fatal("a live Claude turn settled because the override dropped working_status_line")
	}
	if got.State != Unknown || got.Tier != TierNone {
		t.Fatalf("State = %s, Tier = %s; want unknown/none — a veto that cannot be computed is "+
			"silence, not evidence of a finished turn", got.State, got.Tier)
	}

	// Every OTHER required clause, on a screen that genuinely does settle: each
	// one must withhold the settle on its own, so no single requirement is
	// carried by the presence of the others.
	idle := readScreenFixture(t, "claude_idle_completed.txt")
	if !(claudeAdapter{Patterns: embedded}).Classify(idle).SettleableTerminal() {
		t.Fatal("the embedded set no longer settles the idle fixture")
	}
	for _, requirement := range claudeRequirements {
		t.Run(requirement.Name, func(t *testing.T) {
			got := claudeAdapter{Patterns: embedded.Without(requirement.Name)}.Classify(idle)
			if got.SettleableTerminal() {
				t.Fatalf("an idle screen settled against a set with no %q", requirement.Name)
			}
			if got.State != Unknown {
				t.Errorf("State = %s, want unknown", got.State)
			}
		})
	}
}

// TestClaudeOptionalClausesStillClassify is the other half of the contract: the
// requirement list is a SUBSET on purpose, so an override that predates a
// status-line clause must still load and still classify. Losing one of these
// only weakens the TIER, which makes the probe demand more stabilization
// samples rather than fewer — the safe direction, and the reason they are not
// required.
func TestClaudeOptionalClausesStillClassify(t *testing.T) {
	embedded, ok := PatternsFor(harness.Claude)
	if !ok {
		t.Fatal("PatternsFor(claude) returned no embedded set")
	}
	idle := readScreenFixture(t, "claude_idle_completed.txt")

	got := claudeAdapter{Patterns: embedded.Without(claudeClauseCompleted)}.Classify(idle)
	if !got.SettleableTerminal() {
		t.Fatalf("dropping an optional clause blocked the settle: state %s, vetoes %s",
			got.State, got.Vetoes)
	}
	if got.Tier != TierQuiescent {
		t.Fatalf("Tier = %s, want quiescent; the completed shape is what graded it higher",
			got.Tier)
	}
}

// embeddedClaudeRegexp panics on a missing clause, deliberately: this runs at
// test-binary init over the EMBEDDED patterns, where a missing clause is a
// build-time bug. The probe path's tolerance for an absent clause is for user
// overrides, not for the file this repository ships.
func embeddedClaudeRegexp(name string) *regexp.Regexp {
	set, ok := PatternsFor(harness.Claude)
	if !ok {
		panic("screenstate: no embedded pattern set for " + harness.Claude)
	}
	re := set.Regexp(name)
	if re == nil {
		panic(fmt.Sprintf("screenstate: embedded %s patterns have no regex %q", harness.Claude, name))
	}
	return re
}
