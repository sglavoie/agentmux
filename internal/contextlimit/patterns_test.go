package contextlimit

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/patternset"
)

// restorePatterns snapshots the installed set so a test that replaces it —
// which is the whole point of the override path — cannot leak into the next.
func restorePatterns(t *testing.T) {
	t.Helper()
	saved := InstalledPatterns()
	t.Cleanup(func() { InstallPatterns(saved) })
}

// TestEmbeddedPatternsDecode is the build-time guard on the shipped file. Its
// package-init form degrades to the empty set on a decode failure rather than
// panicking on the daemon's startup path, so without this test a typo in the
// TOML would ship as "no context-limit banner is ever detected" with nothing
// red.
func TestEmbeddedPatternsDecode(t *testing.T) {
	set, err := EmbeddedPatterns()
	if err != nil {
		t.Fatalf("EmbeddedPatterns() = %v", err)
	}
	if set.Harness() != PatternsName {
		t.Fatalf("harness = %q, want %q", set.Harness(), PatternsName)
	}
	if set.Source() != patternset.SourceEmbedded {
		t.Fatalf("source = %q, want %q", set.Source(), patternset.SourceEmbedded)
	}
	if got := InstalledPatterns().Harness(); got != PatternsName {
		t.Fatalf("package init installed %q; a binary that never links screenstate has no patterns",
			got)
	}
}

// TestEmbeddedPatternsDefineEveryClause mirrors screenstate's guard of the same
// name: a clause the Go side reads but the shipped file does not define is a
// silent no-match, and only a fixture would notice.
func TestEmbeddedPatternsDefineEveryClause(t *testing.T) {
	set, err := EmbeddedPatterns()
	if err != nil {
		t.Fatalf("EmbeddedPatterns() = %v", err)
	}
	for _, name := range clauseNames {
		if !set.Has(name) {
			t.Errorf("embedded contextlimit patterns define no clause %q", name)
		}
	}
}

// TestOverrideChangesDetection proves the extraction is real: the banner copy
// is now data, so a user whose Claude release renamed it can fix detection
// without a build. It also pins whole-file replacement — the embedded copy must
// NOT survive alongside the override.
func TestOverrideChangesDetection(t *testing.T) {
	restorePatterns(t)

	const banner = "Context limit reached · /compact or /clear to continue"
	const renamed = "Conversation budget exhausted — /squash to keep going"

	if !HasMarker(banner) || HasMarker(renamed) {
		t.Fatalf("embedded patterns do not match the release they were written for")
	}

	override, err := patternset.Decode([]byte(`
harness = "contextlimit"
[phrases]
limit_reached_banner = ["conversation budget exhausted"]
compact_command = ["/squash"]
window_full_banner = ["unused"]
compact_or_clear_hint = ["unused"]
clear_command = ["unused"]
continue_hint = ["unused"]
chrome_prompt_rune = ["❯"]
chrome_mode_hint = ["unused"]
chrome_shortcut_prefix = ["?"]
chrome_shortcut_hint = ["unused"]
chrome_footer_label = ["unused"]
`), PatternsName)
	if err != nil {
		t.Fatalf("decode override: %v", err)
	}
	InstallPatterns(override)

	if !HasMarker(renamed) {
		t.Fatal("override did not take effect")
	}
	if HasMarker(banner) {
		t.Fatal("embedded copy survived the override; replacement must not merge")
	}
}

// TestMissingClausesReportNoMarker pins the fail direction. Unlike the codex
// prompt clauses, nothing here needs a required-clause guard: an override that
// drops a clause loses detections, and losing a veto is the same answer this
// package gave before the copy was tunable. What must never happen is the
// opposite — a set with no clauses inventing a banner.
func TestMissingClausesReportNoMarker(t *testing.T) {
	restorePatterns(t)

	InstallPatterns(patternset.Patterns{})
	if HasMarker("Context limit reached · /compact or /clear to continue") {
		t.Fatal("an empty pattern set reported a banner")
	}
	if ScreenShowsMarker("Context limit reached · /compact or /clear to continue\n") {
		t.Fatal("an empty pattern set reported a banner on a screen")
	}
	if isUIChromeLine("❯") {
		t.Fatal("an empty pattern set called a line chrome")
	}
}

// TestLimitReachedShortCircuitSurvivesExtraction pins the nested conditional
// hasMarker kept in Go: the headline without a recovery hint is an immediate
// NO, and it stays NO even when window-full copy is also on screen. A flat
// all/any group over the same clauses cannot express that early return.
//
// With the SHIPPED copy the two forms happen to agree — every window-full
// recovery disjunct contains "/compact", so the branch the early return skips
// could not have matched anyway. They stop agreeing the moment an override
// edits compact_command, which is exactly why the shape is Go and not data.
func TestLimitReachedShortCircuitSurvivesExtraction(t *testing.T) {
	if HasMarker("Context limit reached") {
		t.Fatal("the headline alone must not be a banner")
	}
	if HasMarker("Context limit reached. The context window is full. Please start a new session.") {
		t.Fatal("the headline without a recovery hint must stay NO even beside window-full copy")
	}
	if !HasMarker("Context limit reached. Run /compact to continue.") {
		t.Fatal("the headline with a /compact hint is a banner")
	}
}
