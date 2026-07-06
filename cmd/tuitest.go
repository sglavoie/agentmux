package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/sglavoie/agentmux/tui"
	"github.com/spf13/cobra"
)

// This file exposes the in-process TUI test harness through a hidden Cobra
// command so automation and agents can drive layout-regression scenarios
// without a live Kitty instance. The command stays thin: it parses flags and
// scenario files, delegates every run to the package-level runner in tui/, and
// translates outcomes into stable JSON plus harness-specific process exit codes
// (see exit_error.go). It is hidden from root help and never touches the
// operator's real config or terminal.

var (
	tuiTestJSON   bool
	tuiTestWidth  int
	tuiTestHeight int
	tuiTestKeys   []string
)

var tuiTestCmd = &cobra.Command{
	Use:   "tui-test",
	Short: "Developer harness for detecting TUI layout regressions",
	Long: "tui-test drives the dashboard model in-process against built-in " +
		"fixtures or isolated configs to detect layout regressions. It is a " +
		"hidden developer/automation tool and never talks to a live Kitty " +
		"instance or the operator's real data.",
	Hidden: true,
	// The harness prints structured results (often JSON) itself and maps
	// failures to explicit exit codes via Execute, so Cobra must not append its
	// own "Error:"/usage lines on top of that output.
	SilenceErrors: true,
	SilenceUsage:  true,
}

var tuiTestListFixturesCmd = &cobra.Command{
	Use:   "list-fixtures",
	Short: "List the built-in harness fixtures",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		names := tui.UITestFixtureNames()
		if tuiTestJSON {
			return writeTUITestJSON(cmd, names)
		}
		out := cmd.OutOrStdout()
		for _, name := range names {
			_, _ = fmt.Fprintln(out, name)
		}
		return nil
	},
}

var tuiTestRunCmd = &cobra.Command{
	Use:   "run SCENARIO...",
	Short: "Run one or more scenario files",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUITestScenarios(cmd, args)
	},
}

var tuiTestFixtureCmd = &cobra.Command{
	Use:   "fixture NAME",
	Short: "Run an ad hoc key sequence against a built-in fixture",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUITestFixture(cmd, args[0])
	},
}

// runTUITestScenarios loads and runs each scenario file in order, collecting a
// structured result per file. A load, validation, staging, or action error is a
// bad invocation (exitInvalidInput); a clean run with a failing assertion is a
// scenario regression (exitScenarioFailure). Output is emitted before the
// failure exit so the results are always visible.
func runTUITestScenarios(cmd *cobra.Command, paths []string) error {
	results := make([]tui.UITestScenarioResult, 0, len(paths))
	for _, path := range paths {
		sc, err := tui.LoadUITestScenario(path)
		if err != nil {
			return &ExitError{Code: exitInvalidInput, Err: err}
		}
		env, cleanup, err := tuiTestScenarioEnv(sc)
		if err != nil {
			return &ExitError{Code: exitInvalidInput, Err: err}
		}
		res, err := tui.RunUITestScenario(sc, env)
		cleanup()
		if err != nil {
			return &ExitError{Code: exitInvalidInput, Err: err}
		}
		results = append(results, res)
	}

	if tuiTestJSON {
		if err := writeTUITestJSON(cmd, results); err != nil {
			return err
		}
	} else {
		printTUITestScenarioResults(cmd, results)
	}

	if tuiTestScenariosFailed(results) {
		return &ExitError{Code: exitScenarioFailure, Quiet: true}
	}
	return nil
}

// tuiTestScenariosFailed reports whether any scenario had an unmet assertion.
func tuiTestScenariosFailed(results []tui.UITestScenarioResult) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}

// runTUITestFixture applies the ad hoc key sequence to the named fixture and
// reports the per-step observations. An unknown fixture or unparseable key is a
// bad invocation; a layout issue on the final screen is a regression.
func runTUITestFixture(cmd *cobra.Command, name string) error {
	actions := make([]tui.UITestAction, 0, len(tuiTestKeys))
	for _, key := range tuiTestKeys {
		actions = append(actions, tui.UITestAction{Type: tui.UITestActionKey, Key: key})
	}
	opts := tui.UITestOptions{Size: tui.UITestSize{Width: tuiTestWidth, Height: tuiTestHeight}}

	obs, err := tui.RunUITestFixture(name, opts, actions)
	if err != nil {
		return &ExitError{Code: exitInvalidInput, Err: err}
	}

	if tuiTestJSON {
		if err := writeTUITestJSON(cmd, obs); err != nil {
			return err
		}
	} else {
		printTUITestObservations(cmd, obs)
	}

	if tuiTestObservationsHaveIssues(obs) {
		return &ExitError{Code: exitScenarioFailure, Quiet: true}
	}
	return nil
}

// tuiTestObservationsHaveIssues reports whether the final observation carries
// any detected layout issue.
func tuiTestObservationsHaveIssues(obs []tui.UITestObservation) bool {
	if len(obs) == 0 {
		return false
	}
	return len(obs[len(obs)-1].Screen.Issues) > 0
}

// tuiTestScenarioEnv builds the isolation seam a scenario needs. Fixture
// scenarios need nothing; config scenarios stage a throwaway root and apply the
// isolation env vars through a restoring setter, returning a cleanup that both
// rolls back every env var the staging changed and removes the staging
// directory once the run completes. The real binary exits right after a run, so
// the rollback is defensive: it keeps repeated in-process runs (and the
// harness's own integration tests) from leaking the staged HOME/AMX_CONFIG/XDG
// values into later work.
func tuiTestScenarioEnv(sc tui.UITestScenario) (tui.UITestScenarioEnv, func(), error) {
	if sc.Config == "" {
		return tui.UITestScenarioEnv{}, func() {}, nil
	}
	root, err := os.MkdirTemp("", "amx-tui-test-")
	if err != nil {
		return tui.UITestScenarioEnv{}, func() {}, fmt.Errorf("tui-test: staging config scenario: %w", err)
	}
	restorer := newEnvRestorer()
	env := tui.UITestScenarioEnv{Root: root, Setenv: restorer.setenv}
	cleanup := func() {
		restorer.restore()
		_ = os.RemoveAll(root)
	}
	return env, cleanup, nil
}

// envRestorer wraps os.Setenv so a config scenario's staged environment can be
// rolled back after the run. It records the first prior value it sees for each
// key (or its absence) so restore returns the process environment to exactly the
// state it had before staging.
type envRestorer struct {
	prior map[string]*string // first prior value per key; nil means the key was unset
}

func newEnvRestorer() *envRestorer {
	return &envRestorer{prior: make(map[string]*string)}
}

// setenv records the pre-existing value for key the first time it is seen, then
// applies the staged value. It matches the tui.UITestScenarioEnv.Setenv signature.
func (r *envRestorer) setenv(key, value string) error {
	if _, seen := r.prior[key]; !seen {
		if old, ok := os.LookupEnv(key); ok {
			r.prior[key] = &old
		} else {
			r.prior[key] = nil
		}
	}
	return os.Setenv(key, value)
}

// restore returns every recorded key to its pre-staging value, unsetting keys
// that were absent before staging.
func (r *envRestorer) restore() {
	for key, old := range r.prior {
		if old == nil {
			_ = os.Unsetenv(key)
		} else {
			_ = os.Setenv(key, *old)
		}
	}
}

// writeTUITestJSON marshals v as indented JSON to the command's stdout, followed
// by a trailing newline so piped output ends cleanly.
func writeTUITestJSON(cmd *cobra.Command, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("tui-test: encoding result: %w", err)
	}
	if _, err := cmd.OutOrStdout().Write(data); err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout())
	return err
}

// printTUITestScenarioResults writes a compact human-readable summary, one line
// per scenario plus a nested line per failure.
func printTUITestScenarioResults(cmd *cobra.Command, results []tui.UITestScenarioResult) {
	out := cmd.OutOrStdout()
	for _, r := range results {
		status := "PASS"
		if !r.OK {
			status = "FAIL"
		}
		name := r.Scenario
		if name == "" {
			name = "(unnamed)"
		}
		_, _ = fmt.Fprintf(out, "%s %s\n", status, name)
		for _, f := range r.Failures {
			_, _ = fmt.Fprintf(out, "  - %s: %s\n", f.Kind, f.Message)
		}
	}
}

// printTUITestObservations writes a compact per-step summary of an ad hoc run,
// naming the mode, size, and any layout issues at each step.
func printTUITestObservations(cmd *cobra.Command, obs []tui.UITestObservation) {
	out := cmd.OutOrStdout()
	for i, o := range obs {
		_, _ = fmt.Fprintf(out, "step %d: mode=%s size=%dx%d issues=%d\n",
			i, o.Mode, o.Size.Width, o.Size.Height, len(o.Screen.Issues))
		for _, issue := range o.Screen.Issues {
			_, _ = fmt.Fprintf(out, "  - %s (actual=%d limit=%d)\n", issue.Type, issue.Actual, issue.Limit)
		}
	}
}

func init() {
	tuiTestCmd.PersistentFlags().BoolVar(&tuiTestJSON, "json", false, "emit machine-readable JSON output")
	tuiTestFixtureCmd.Flags().IntVar(&tuiTestWidth, "width", 0, "terminal width override (0 uses the fixture default)")
	tuiTestFixtureCmd.Flags().IntVar(&tuiTestHeight, "height", 0, "terminal height override (0 uses the fixture default)")
	tuiTestFixtureCmd.Flags().StringArrayVar(&tuiTestKeys, "key", nil, "a key to press (repeatable, applied in order)")
	// Cobra checks the executed leaf command (not the parent) before printing an
	// error/usage line, so silence each subcommand too. Execute owns all
	// harness error reporting and exit-code mapping via ExitError.
	for _, sub := range []*cobra.Command{tuiTestListFixturesCmd, tuiTestRunCmd, tuiTestFixtureCmd} {
		sub.SilenceErrors = true
		sub.SilenceUsage = true
	}
	tuiTestCmd.AddCommand(tuiTestListFixturesCmd, tuiTestRunCmd, tuiTestFixtureCmd)
}
