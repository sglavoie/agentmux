package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui"
)

// runRootArgs drives rootCmd exactly like the real binary (so subcommand and
// flag resolution match production), captures stdout/stderr, and returns the
// error rootCmd.Execute produced. The tui-test globals are reset first because
// Cobra does not clear flag-bound package variables between runs.
func runRootArgs(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	tuiTestJSON = false
	tuiTestWidth = 0
	tuiTestHeight = 0
	tuiTestKeys = nil

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

// writeScenario writes a scenario JSON file into a temp dir and returns its path.
func writeScenario(t *testing.T, sc tui.UITestScenario) string {
	t.Helper()
	data, err := json.Marshal(sc)
	if err != nil {
		t.Fatalf("marshal scenario: %v", err)
	}
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
	return path
}

func TestRootHelp(t *testing.T) {
	stdout, _, err := runRootArgs(t, "--help")
	if err != nil {
		t.Fatalf("help returned error: %v", err)
	}
	if strings.Contains(stdout, "tui-test") {
		t.Errorf("root help must not list the hidden tui-test command, got:\n%s", stdout)
	}
	// A public command must still be visible so we know help itself rendered.
	if !strings.Contains(stdout, "resort") {
		t.Errorf("root help should list public commands like resort, got:\n%s", stdout)
	}
	for _, removed := range []string{"index", "plan", "search", "workspace"} {
		if strings.Contains(stdout, "  "+removed+" ") {
			t.Errorf("root help must not list removed command %q, got:\n%s", removed, stdout)
		}
	}
	if !strings.Contains(stdout, "multi-harness session manager") {
		t.Errorf("root help has stale product description:\n%s", stdout)
	}
}

func TestRemovedCommandsAreNotRegistered(t *testing.T) {
	rootNames := make(map[string]bool)
	for _, command := range rootCmd.Commands() {
		rootNames[command.Name()] = true
	}
	for _, removed := range []string{"index", "plan", "search", "workspace"} {
		if rootNames[removed] {
			t.Errorf("root command %q remains registered", removed)
		}
	}
	sessionNames := make(map[string]bool)
	for _, command := range sessionCmd.Commands() {
		sessionNames[command.Name()] = true
	}
	for _, removed := range []string{"launch", "focus"} {
		if sessionNames[removed] {
			t.Errorf("session command %q remains registered", removed)
		}
	}
}

func TestTUITestListFixtures(t *testing.T) {
	stdout, _, err := runRootArgs(t, "tui-test", "list-fixtures", "--json")
	if err != nil {
		t.Fatalf("list-fixtures returned error: %v", err)
	}
	var names []string
	if err := json.Unmarshal([]byte(stdout), &names); err != nil {
		t.Fatalf("list-fixtures did not emit a JSON array: %v\noutput=%q", err, stdout)
	}
	for _, want := range tui.UITestFixtureNames() {
		found := false
		for _, got := range names {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("list-fixtures JSON missing fixture %q; got %v", want, names)
		}
	}
}

func TestTUITestRunScenarioPass(t *testing.T) {
	path := writeScenario(t, tui.UITestScenario{
		Name:    "pass",
		Fixture: "basic",
		Size:    tui.UITestSize{Width: 120, Height: 40},
		Assertions: []tui.UITestAssertion{
			{Type: tui.UITestAssertModeEquals, Mode: "list"},
		},
	})

	stdout, stderr, err := runRootArgs(t, "tui-test", "run", path, "--json")
	if err != nil {
		t.Fatalf("passing scenario returned error: %v (stderr=%q)", err, stderr)
	}

	var results []tui.UITestScenarioResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("run did not emit a JSON array: %v\noutput=%q", err, stdout)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 scenario result, got %d", len(results))
	}
	if !results[0].OK {
		t.Errorf("expected scenario to pass, got failures: %v", results[0].Failures)
	}
}

func TestTUITestFixtureAdHoc(t *testing.T) {
	stdout, stderr, err := runRootArgs(t, "tui-test", "fixture", "basic", "--width", "120", "--height", "40", "--json")
	if err != nil {
		t.Fatalf("ad hoc fixture run returned error: %v (stderr=%q)", err, stderr)
	}
	var obs []tui.UITestObservation
	if err := json.Unmarshal([]byte(stdout), &obs); err != nil {
		t.Fatalf("fixture run did not emit JSON observations: %v\noutput=%q", err, stdout)
	}
	if len(obs) != 1 {
		t.Fatalf("expected 1 observation (initial only, no actions), got %d", len(obs))
	}
	if obs[0].Mode != "list" {
		t.Errorf("expected initial mode list, got %q", obs[0].Mode)
	}
}

// TestTUITestFixturePartialSizeUsesFixtureDefault pins the fix for a partial
// size override at the CLI layer: passing only --width must report the fixture's
// default height in the JSON observation rather than a literal 0 for the omitted
// dimension. The wide_profiles fixture defaults to 140x36.
func TestTUITestFixturePartialSizeUsesFixtureDefault(t *testing.T) {
	stdout, stderr, err := runRootArgs(t, "tui-test", "fixture", "wide_profiles", "--width", "140", "--json")
	if err != nil {
		t.Fatalf("partial-size fixture run returned error: %v (stderr=%q)", err, stderr)
	}
	var obs []tui.UITestObservation
	if err := json.Unmarshal([]byte(stdout), &obs); err != nil {
		t.Fatalf("fixture run did not emit JSON observations: %v\noutput=%q", err, stdout)
	}
	if len(obs) != 1 {
		t.Fatalf("expected 1 observation (initial only, no actions), got %d", len(obs))
	}
	if obs[0].Size.Width != 140 {
		t.Errorf("expected supplied width 140 to be preserved, got %d", obs[0].Size.Width)
	}
	if obs[0].Size.Height != 36 {
		t.Errorf("expected omitted height to fall back to fixture default 36, got %d", obs[0].Size.Height)
	}
}

// asExitError extracts an *ExitError from a Cobra error chain, failing the test
// when the error is missing or of the wrong type.
func asExitError(t *testing.T, err error) *ExitError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *ExitError, got %T: %v", err, err)
	}
	return ee
}

func TestExitCodeScenarioFailure(t *testing.T) {
	path := writeScenario(t, tui.UITestScenario{
		Name:    "fail",
		Fixture: "basic",
		Size:    tui.UITestSize{Width: 120, Height: 40},
		Assertions: []tui.UITestAssertion{
			{Type: tui.UITestAssertModeEquals, Mode: "search"},
		},
	})

	stdout, stderr, err := runRootArgs(t, "tui-test", "run", path, "--json")
	ee := asExitError(t, err)
	if ee.Code != exitScenarioFailure {
		t.Errorf("scenario failure should exit %d, got %d", exitScenarioFailure, ee.Code)
	}
	if !ee.Quiet {
		t.Errorf("scenario failure that already printed JSON should be Quiet")
	}
	// The JSON result is still written to stdout even on a failing run.
	if !strings.Contains(stdout, "\"ok\": false") {
		t.Errorf("expected failing JSON result on stdout, got %q", stdout)
	}
	// Quiet failures must not add a redundant stderr line (Execute enforces this;
	// here we confirm Cobra itself stayed silent).
	if strings.Contains(stderr, "Error:") {
		t.Errorf("Cobra should stay silent for the harness, got stderr=%q", stderr)
	}
}

func TestExitCodeInvalidScenarioFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	_, _, err := runRootArgs(t, "tui-test", "run", missing, "--json")
	ee := asExitError(t, err)
	if ee.Code != exitInvalidInput {
		t.Errorf("missing scenario file should exit %d, got %d", exitInvalidInput, ee.Code)
	}
	if ee.Quiet {
		t.Errorf("an invalid-input error should not be Quiet so Execute prints the reason")
	}
}

func TestExitCodeUnknownFixture(t *testing.T) {
	_, _, err := runRootArgs(t, "tui-test", "fixture", "no-such-fixture", "--json")
	ee := asExitError(t, err)
	if ee.Code != exitInvalidInput {
		t.Errorf("unknown fixture should exit %d, got %d", exitInvalidInput, ee.Code)
	}
}

func TestExitCodeDistinguishesFailureFromInvalid(t *testing.T) {
	if exitScenarioFailure == exitInvalidInput {
		t.Fatal("scenario failure and invalid input must use distinct exit codes")
	}
}

// committedScenariosDir is the repo's example scenario directory relative to the
// cmd package (cmd/ -> repo root -> docs/tui-testing/scenarios).
const committedScenariosDir = "../docs/tui-testing/scenarios"

// TestTUITestRunCommittedScenariosEndToEnd drives every committed example
// scenario through the real CLI run path in one invocation and asserts the
// harness emits a stable JSON array of passing results, exits zero, and stays
// silent on stderr. This is the end-to-end complement to the in-process
// tui.TestExampleScenariosParseAndPass drift guard: it proves the same files
// pass through flag parsing, scenario staging, and result encoding, not just the
// package-level runner.
func TestTUITestRunCommittedScenariosEndToEnd(t *testing.T) {
	entries, err := os.ReadDir(committedScenariosDir)
	if err != nil {
		t.Fatalf("reading committed scenarios dir: %v", err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(committedScenariosDir, e.Name()))
	}
	if len(paths) == 0 {
		t.Fatalf("no committed scenarios found in %s", committedScenariosDir)
	}

	args := append([]string{"tui-test", "run"}, paths...)
	args = append(args, "--json")
	stdout, stderr, err := runRootArgs(t, args...)
	if err != nil {
		t.Fatalf("committed scenarios returned error: %v (stderr=%q)", err, stderr)
	}
	if strings.Contains(stderr, "Error:") {
		t.Errorf("committed scenario run should keep stderr quiet, got %q", stderr)
	}

	var results []tui.UITestScenarioResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("run did not emit a JSON array: %v\noutput=%q", err, stdout)
	}
	if len(results) != len(paths) {
		t.Fatalf("expected %d results, got %d", len(paths), len(results))
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("committed scenario %q failed: %v", r.Scenario, r.Failures)
		}
	}
}

// TestTUITestActionsOverlayWidthRegression pins the actions-popup sizing fix
// (commit 26d9545) through the CLI. A terminal too narrow for the overlay's
// widest row exits with the scenario-failure code and reports modal_width_overflow;
// a terminal wide enough for the box exits clean. This keeps the regression
// guarded at the CLI layer, not only the package-level renderer tests.
func TestTUITestActionsOverlayWidthRegression(t *testing.T) {
	stdout, _, err := runRootArgs(t, "tui-test", "fixture", "actions",
		"--width", "80", "--height", "24", "--key", "a", "--json")
	ee := asExitError(t, err)
	if ee.Code != exitScenarioFailure {
		t.Errorf("narrow actions overlay should exit %d, got %d", exitScenarioFailure, ee.Code)
	}
	if !strings.Contains(stdout, string(tui.UITestIssueModalWidthOverflow)) {
		t.Errorf("expected %s in narrow-run JSON, got %q", tui.UITestIssueModalWidthOverflow, stdout)
	}

	if _, stderr, err := runRootArgs(t, "tui-test", "fixture", "actions",
		"--width", "120", "--height", "40", "--key", "a", "--json"); err != nil {
		t.Errorf("wide actions overlay should exit cleanly, got error %v (stderr=%q)", err, stderr)
	}
}

// TestTUITestConfigPickerWidthRegression pins the profile-picker sizing fix
// (commit 1f08e24) through the CLI. The wide_profiles fixture's long home-dir
// rows overflow the picker box at 120 cols (scenario-failure exit) but fit at
// 140 cols (clean exit).
func TestTUITestConfigPickerWidthRegression(t *testing.T) {
	_, _, err := runRootArgs(t, "tui-test", "fixture", "wide_profiles",
		"--width", "120", "--height", "40", "--key", "C", "--json")
	ee := asExitError(t, err)
	if ee.Code != exitScenarioFailure {
		t.Errorf("narrow config picker should exit %d, got %d", exitScenarioFailure, ee.Code)
	}

	if _, stderr, err := runRootArgs(t, "tui-test", "fixture", "wide_profiles",
		"--width", "140", "--height", "40", "--key", "C", "--json"); err != nil {
		t.Errorf("wide config picker should exit cleanly, got error %v (stderr=%q)", err, stderr)
	}
}

func TestTUITestWrappedFooterLayoutRegressions(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "dashboard legend wraps at 80 cols",
			args: []string{"tui-test", "fixture", "basic", "--width", "80", "--height", "24", "--json"},
		},
		{
			name: "picker footer wraps at 110 cols",
			args: []string{"tui-test", "fixture", "basic", "--width", "110", "--height", "32", "--key", "l", "--json"},
		},
		{
			name: "session search footer wraps at 64 cols",
			args: []string{"tui-test", "fixture", "basic", "--width", "64", "--height", "18", "--key", "S", "--json"},
		},
		{
			name: "plan search footer wraps at 60 cols",
			args: []string{"tui-test", "fixture", "basic", "--width", "60", "--height", "18", "--key", "P", "--json"},
		},
		{
			name: "wide profile fixture default fits config picker",
			args: []string{"tui-test", "fixture", "wide_profiles", "--key", "C", "--json"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runRootArgs(t, tc.args...)
			if err != nil {
				t.Fatalf("fixture run returned error: %v (stderr=%q, stdout=%q)", err, stderr, stdout)
			}
			var obs []tui.UITestObservation
			if err := json.Unmarshal([]byte(stdout), &obs); err != nil {
				t.Fatalf("fixture run did not emit JSON observations: %v\noutput=%q", err, stdout)
			}
			if len(obs) == 0 {
				t.Fatal("expected at least one observation")
			}
			if issues := obs[len(obs)-1].Screen.Issues; len(issues) > 0 {
				t.Fatalf("final observation has layout issues: %+v", issues)
			}
		})
	}
}

// TestTUITestConfigModeIsolatedRun drives a config-backed scenario through the
// CLI run path, which stages a throwaway HOME/AMX_CONFIG/XDG environment, loads
// the config from inside it, and tears the staging down afterward. It asserts
// the run succeeds against an isolated config and, crucially, that the process
// environment is restored so the staged paths never leak into later work — the
// property that makes running config scenarios in-process safe.
func TestTUITestConfigModeIsolatedRun(t *testing.T) {
	const homeKey = "HOME"
	beforeHome, beforeSet := os.LookupEnv(homeKey)

	scenario := tui.UITestScenario{
		Name:   "isolated-config",
		Config: "../testdata/uitest/basic_config.toml",
		Size:   tui.UITestSize{Width: 120, Height: 40},
		Assertions: []tui.UITestAssertion{
			{Type: tui.UITestAssertModeEquals, Mode: "list"},
			{Type: tui.UITestAssertNoLayoutIssues},
		},
	}
	path := writeScenario(t, scenario)

	stdout, stderr, err := runRootArgs(t, "tui-test", "run", path, "--json")
	if err != nil {
		t.Fatalf("config scenario returned error: %v (stderr=%q)", err, stderr)
	}
	var results []tui.UITestScenarioResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("run did not emit a JSON array: %v\noutput=%q", err, stdout)
	}
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("expected 1 passing config-scenario result, got %+v", results)
	}

	// The staging cleanup must restore HOME to exactly its pre-run state.
	afterHome, afterSet := os.LookupEnv(homeKey)
	if beforeSet != afterSet || beforeHome != afterHome {
		t.Errorf("config scenario leaked HOME: before=(%q,%v) after=(%q,%v)",
			beforeHome, beforeSet, afterHome, afterSet)
	}
}
