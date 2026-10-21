//go:build !tuitest

package cmd

import (
	"errors"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// A release build links neither the TUI scenario harness nor cmd/tuitest, but
// still registers a hidden `tui-test`: the name stays reserved and registered
// in both builds, so root's registration and completion checks hold for each,
// and whoever types it learns which build has the harness instead of meeting an
// unknown command. The stand-in lives here rather than in cmd/tuitest because
// importing that package at all is what the tag keeps out of the binary.

// errScenarioHarnessNotBuilt is what every tui-test invocation of a release
// build answers.
var errScenarioHarnessNotBuilt = errors.New("tui-test is not in this build of amx: " +
	"the scenario harness builds only with the tuitest tag " +
	"(go build -tags tuitest ., or go run -tags tuitest . tui-test ...)")

// scenarioHarnessCommand returns the stand-in. It accepts whatever it is given,
// so `tui-test run x.json --json` reaches the same explanation as a bare
// `tui-test`, and exits with cli.ExitInvalidInput: the invocation asks this
// binary for something it does not have.
func scenarioHarnessCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "tui-test",
		Short:              "Developer harness for detecting TUI layout regressions (needs -tags tuitest)",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		SilenceErrors:      true,
		SilenceUsage:       true,
		// Scenario files are paths; keep the shell's file completion, as the
		// real `tui-test run` does.
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveDefault
		},
		RunE: func(*cobra.Command, []string) error {
			return &cli.ExitError{Code: cli.ExitInvalidInput, Err: errScenarioHarnessNotBuilt}
		},
	}
}
