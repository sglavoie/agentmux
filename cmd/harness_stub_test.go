//go:build !tuitest

package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
)

// A release build still answers to tui-test, and every form of it — a bare
// call, a subcommand, harness flags — explains which build has the harness
// instead of running anything or failing on a flag it does not know.
func TestReleaseBuildTUITestNamesTheTaggedBuild(t *testing.T) {
	for _, args := range [][]string{
		{"tui-test"},
		{"tui-test", "run", "docs/tui-testing/scenarios/*.json", "--json"},
		{"tui-test", "fixture", "basic", "--width", "80"},
	} {
		stdout, stderr, err := runRootArgs(t, args...)
		var exitErr *cli.ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != cli.ExitInvalidInput {
			t.Fatalf("%v: err = %v, want an ExitInvalidInput exit", args, err)
		}
		if !strings.Contains(err.Error(), "-tags tuitest") {
			t.Fatalf("%v: %q does not say how to build the harness", args, err)
		}
		if stdout != "" || stderr != "" {
			t.Fatalf("%v: the stand-in wrote stdout %q stderr %q; root prints its error once", args, stdout, stderr)
		}
	}
}
