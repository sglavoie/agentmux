package screenstate

import (
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/contextlimit"
)

// The context-limit copy is Claude's, but it lives in internal/contextlimit
// because this package imports that one. These tests pin the seam that keeps
// the split invisible to a user: one override directory, one Load, one warning
// vocabulary.

func TestLoadEmbeddedCarriesTheContextLimitSet(t *testing.T) {
	sets, warnings := loadEmbedded()
	if len(warnings) != 0 {
		t.Fatalf("embedded patterns must validate, got warnings: %v", warnings)
	}
	set, ok := sets[contextlimit.PatternsName]
	if !ok {
		t.Fatalf("loadEmbedded dropped %q; its override would never be looked for",
			contextlimit.PatternsName)
	}
	if set.Source() != SourceEmbedded {
		t.Fatalf("source = %q, want %q", set.Source(), SourceEmbedded)
	}
}

func TestLoadAppliesTheContextLimitOverride(t *testing.T) {
	restorePatterns(t)

	const banner = "Context limit reached · /compact or /clear to continue"
	const renamed = "Conversation budget exhausted — /squash to keep going"

	dir := writeOverride(t, contextlimit.PatternsName, `
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
`)
	expectedPath := filepath.Join(dir, patternsDirName, contextlimit.PatternsName+".toml")

	warnings := Load(dir)
	applied := warningsOfKind(warnings, WarnOverrideApplied)
	if len(applied) != 1 || applied[0].Harness != contextlimit.PatternsName {
		t.Fatalf("warnings = %v, want one %s for %s",
			warnings, WarnOverrideApplied, contextlimit.PatternsName)
	}

	// Installed in the package that owns the detection, not merely in this
	// package's map: contextlimit is what the daemon and the hook path call.
	if got := contextlimit.InstalledPatterns().Source(); got != expectedPath {
		t.Fatalf("contextlimit source = %q, want the override path", got)
	}
	if !contextlimit.HasMarker(renamed) || contextlimit.HasMarker(banner) {
		t.Fatal("the override did not reach contextlimit.HasMarker")
	}
}

// TestLoadLeavesContextLimitAloneOnAnInvalidOverride pins the degrade
// direction across the package boundary: a bad override must leave the
// embedded copy in force in BOTH packages, never install an empty set.
func TestLoadLeavesContextLimitAloneOnAnInvalidOverride(t *testing.T) {
	restorePatterns(t)

	dir := writeOverride(t, contextlimit.PatternsName, "harness = \"contextlimit\"\nnot_a_section = 1\n")

	warnings := Load(dir)
	if len(warningsOfKind(warnings, WarnOverrideInvalid)) != 1 {
		t.Fatalf("warnings = %v, want one %s", warnings, WarnOverrideInvalid)
	}
	if got := contextlimit.InstalledPatterns().Source(); got != SourceEmbedded {
		t.Fatalf("contextlimit source = %q, want the embedded default", got)
	}
	if !contextlimit.HasMarker("Context limit reached · /compact or /clear to continue") {
		t.Fatal("a bad override blinded the embedded context-limit copy")
	}
}
