package cmd

import (
	"testing"

	"github.com/sglavoie/agentmux/cmd/release"
)

// The release gate lives in cmd/release; what root assembly owns is that the
// command is registered here at all, and that it stays hidden. The flag names
// are asserted alongside it because a rename silently turns the documented
// `just paired-release-gate` invocation into an unknown-flag error.

func TestReleaseGateIsRegisteredAndHidden(t *testing.T) {
	releaseGateCmd := release.Command()
	var found bool
	for _, command := range rootCmd.Commands() {
		if command.Name() == "release-gate" {
			found = true
			if !command.Hidden {
				t.Error("release-gate is visible in root help; it is an operator procedure with real side effects, not a user command")
			}
		}
	}
	if !found {
		t.Fatal("root command has no release-gate command")
	}

	// Every flag the justfile recipe types. A rename here silently turns the
	// documented invocation into an unknown-flag error.
	for _, name := range []string{"asl-root", "amx-root", "signing-key", "evidence-dir", "tag", "force", "json"} {
		if releaseGateCmd.Flags().Lookup(name) == nil {
			t.Errorf("release-gate does not declare --%s", name)
		}
	}
	if releaseGateCmd.Flag("asl-root").Annotations[cobraRequiredAnnotation] == nil {
		t.Error("--asl-root is not marked required; the gate must never guess which ASL checkout it is releasing")
	}
	if !releaseGateCmd.SilenceErrors || !releaseGateCmd.SilenceUsage {
		t.Error("release-gate must silence Cobra's own error and usage output; it maps outcomes to exit codes itself")
	}
}

// cobraRequiredAnnotation is Cobra's internal key for a required flag. It is
// spelled out rather than imported because Cobra does not export it.
const cobraRequiredAnnotation = "cobra_annotation_bash_completion_one_required_flag"
