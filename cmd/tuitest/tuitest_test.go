package tuitest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/tui"
	"github.com/spf13/cobra"
)

// runRootArgs drives the tui-test tree from a root command shaped like the real
// binary's (so subcommand and flag resolution match production), captures
// stdout/stderr, and returns the error Execute produced. The tui-test globals
// are reset first because Cobra does not clear flag-bound package variables
// between runs.
func runRootArgs(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	tuiTestJSON = false
	tuiTestWidth = 0
	tuiTestHeight = 0
	tuiTestKeys = nil

	var stdout, stderr bytes.Buffer
	rootCmd := &cobra.Command{Use: "amx"}
	rootCmd.AddCommand(Command())
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

// asExitError extracts an *cli.ExitError from a Cobra error chain, failing the test
// when the error is missing or of the wrong type.
func asExitError(t *testing.T, err error) *cli.ExitError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var ee *cli.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *cli.ExitError, got %T: %v", err, err)
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
	if ee.Code != cli.ExitScenarioFailure {
		t.Errorf("scenario failure should exit %d, got %d", cli.ExitScenarioFailure, ee.Code)
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
	if ee.Code != cli.ExitInvalidInput {
		t.Errorf("missing scenario file should exit %d, got %d", cli.ExitInvalidInput, ee.Code)
	}
	if ee.Quiet {
		t.Errorf("an invalid-input error should not be Quiet so Execute prints the reason")
	}
}

func TestExitCodeUnknownFixture(t *testing.T) {
	_, _, err := runRootArgs(t, "tui-test", "fixture", "no-such-fixture", "--json")
	ee := asExitError(t, err)
	if ee.Code != cli.ExitInvalidInput {
		t.Errorf("unknown fixture should exit %d, got %d", cli.ExitInvalidInput, ee.Code)
	}
}

func TestExitCodeDistinguishesFailureFromInvalid(t *testing.T) {
	if cli.ExitScenarioFailure == cli.ExitInvalidInput {
		t.Fatal("scenario failure and invalid input must use distinct exit codes")
	}
}

// committedScenariosDir is the repo's example scenario directory relative to the
// cmd/tuitest package (cmd/tuitest/ -> repo root -> docs/tui-testing/scenarios).
const committedScenariosDir = "../../docs/tui-testing/scenarios"

// TestTUITestRunCommittedScenariosEndToEnd drives every committed example
// scenario through the real CLI run path, the way `just tui-scenarios` runs
// them, and asserts the harness emits a stable JSON array of passing results,
// exits zero, and stays silent on stderr. This is the end-to-end complement to
// the in-process tui.TestExampleScenariosParseAndPass drift guard: it proves
// the same files pass through flag parsing, scenario staging, and result
// encoding, not just the package-level runner.
//
// The same runs are the isolation check for the committed set: each happens
// under a sentinel HOME, and every file there must survive byte for byte.
//
// A run stages each scenario's environment in the process environment, so
// two runs cannot share a process. The set is split into shards instead, and
// each shard is one `tui-test run` of many files in a child of this test
// binary (TestTUITestCommittedScenarioShard) with a sentinel HOME of its own;
// together they run every file exactly once.
func TestTUITestRunCommittedScenariosEndToEnd(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(committedScenariosDir)
	if err != nil {
		t.Fatalf("reading committed scenarios dir: %v", err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path, err := filepath.Abs(filepath.Join(committedScenariosDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		t.Fatalf("no committed scenarios found in %s", committedScenariosDir)
	}

	shards := min(runtime.GOMAXPROCS(0), maxScenarioShards, len(paths))
	groups := make([][]string, shards)
	for i, path := range paths {
		groups[i%shards] = append(groups[i%shards], path)
	}

	type shardRun struct {
		home, out, stderr string
		files             map[string]string
		output            []byte
		err               error
	}
	runs := make([]shardRun, shards)
	var wg sync.WaitGroup
	for i, group := range groups {
		home, files := sentinelHomeDir(t)
		out := filepath.Join(t.TempDir(), "results.json")
		runs[i] = shardRun{home: home, files: files, out: out, stderr: out + ".stderr"}
		cmd := exec.Command(os.Args[0], "-test.run=^TestTUITestCommittedScenarioShard$", "-test.count=1")
		// The run must find the home by itself: nothing may point it elsewhere.
		cmd.Env = append(environWithout(os.Environ(), sentinelUnsetKeys...),
			"HOME="+home,
			scenarioShardFilesEnv+"="+strings.Join(group, "\n"),
			scenarioShardOutEnv+"="+out,
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs[i].output, runs[i].err = cmd.CombinedOutput()
		}()
	}
	wg.Wait()

	total := 0
	for i, run := range runs {
		requireUntouched(t, run.home, run.files)
		if run.err != nil {
			// A failing scenario fails its shard; the results below still
			// name it when the shard got as far as writing them.
			t.Errorf("shard %d of committed scenarios failed: %v\n%s", i, run.err, run.output)
		}
		stderr, err := os.ReadFile(run.stderr)
		if err != nil {
			t.Errorf("shard %d left no stderr record: %v\n%s", i, err, run.output)
			continue
		}
		if strings.Contains(string(stderr), "Error:") {
			t.Errorf("committed scenario run should keep stderr quiet, got %q", stderr)
		}
		stdout, err := os.ReadFile(run.out)
		if err != nil {
			t.Errorf("shard %d left no results: %v\n%s", i, err, run.output)
			continue
		}
		var results []tui.UITestScenarioResult
		if err := json.Unmarshal(stdout, &results); err != nil {
			t.Errorf("shard %d did not emit a JSON array: %v\noutput=%q", i, err, stdout)
			continue
		}
		if len(results) != len(groups[i]) {
			t.Errorf("shard %d: expected %d results, got %d", i, len(groups[i]), len(results))
		}
		total += len(results)
		for _, r := range results {
			if !r.OK {
				t.Errorf("committed scenario %q failed: %v", r.Scenario, r.Failures)
			}
		}
	}
	if !t.Failed() && total != len(paths) {
		t.Fatalf("expected %d results across %d shards, got %d", len(paths), shards, total)
	}
}

// maxScenarioShards bounds the child processes the end-to-end run starts.
const maxScenarioShards = 8

const (
	scenarioShardFilesEnv = "AMX_TUITEST_SHARD_FILES"
	scenarioShardOutEnv   = "AMX_TUITEST_SHARD_OUT"
)

// TestTUITestCommittedScenarioShard is one shard of
// TestTUITestRunCommittedScenariosEndToEnd, run in a child process under a
// sentinel HOME. It runs the files it is given through the CLI in a single
// invocation and leaves the JSON results and stderr where the parent reads
// them. Outside a shard it has nothing to do.
func TestTUITestCommittedScenarioShard(t *testing.T) {
	files := os.Getenv(scenarioShardFilesEnv)
	out := os.Getenv(scenarioShardOutEnv)
	if files == "" || out == "" {
		t.Skip("run as a shard by TestTUITestRunCommittedScenariosEndToEnd")
	}
	// TestMain points XDG_STATE_HOME at a scratch root; a shard, like the
	// sentinel home it runs under, leaves every path to be found from HOME.
	for _, key := range sentinelUnsetKeys {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	args := append([]string{"tui-test", "run"}, strings.Split(files, "\n")...)
	args = append(args, "--json")
	stdout, stderr, err := runRootArgs(t, args...)
	if writeErr := os.WriteFile(out, []byte(stdout), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(out+".stderr", []byte(stderr), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil {
		t.Fatalf("committed scenarios returned error: %v (stderr=%q)", err, stderr)
	}
}

// environWithout returns env with every entry for keys removed.
func environWithout(env []string, keys ...string) []string {
	kept := env[:0:0]
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" || slices.Contains(keys, name) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// TestTUITestRunExpandsScenarioGlob pins the unshelled form of the documented
// `tui-test run docs/tui-testing/scenarios/*.json` invocation: when the binary
// is exec'd directly the pattern arrives verbatim, so the command must expand it
// to the same committed scenario set an interactive shell would have passed.
func TestTUITestRunExpandsScenarioGlob(t *testing.T) {
	// Expansion is what is under test, so a directory of two scenarios and a
	// non-matching file is enough; running every committed scenario again is
	// TestTUITestRunCommittedScenariosEndToEnd's job.
	dir := t.TempDir()
	for _, name := range []string{"legend-narrow-fit.json", "footer-gloss-narrow-fit.json"} {
		body, err := os.ReadFile(filepath.Join(committedScenariosDir, name))
		if err != nil {
			t.Fatalf("reading committed scenario %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a scenario"), 0o600); err != nil {
		t.Fatal(err)
	}
	const want = 2

	stdout, stderr, err := runRootArgs(t, "tui-test", "run",
		filepath.Join(dir, "*.json"), "--json")
	if err != nil {
		t.Fatalf("glob run returned error: %v (stderr=%q)", err, stderr)
	}
	var results []tui.UITestScenarioResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("glob run did not emit a JSON array: %v\noutput=%q", err, stdout)
	}
	if len(results) != want {
		t.Fatalf("glob expanded to %d scenarios, want %d", len(results), want)
	}

	// A pattern matching nothing stays a bad invocation rather than silently
	// running zero scenarios and exiting clean.
	_, _, err = runRootArgs(t, "tui-test", "run",
		filepath.Join(t.TempDir(), "*.json"), "--json")
	if ee := asExitError(t, err); ee.Code != cli.ExitInvalidInput {
		t.Errorf("unmatched pattern should exit %d, got %d", cli.ExitInvalidInput, ee.Code)
	}
}

// TestTUITestActionsMenuFitsTargetSizes pins the selectable menu's responsive
// layout through the CLI: at each of the four target sizes the menu opens, the
// harness exits clean, and no modal-fit issue is reported.
//
// It replaces TestTUITestActionsOverlayWidthRegression, which required the
// overlay to FAIL at 80x24 with modal_width_overflow. That assertion pinned the
// old wrapped-description legend, whose widest row could not fit an 80-column
// terminal, and 80x24 is now a size the menu is required to fit — so the old
// test would have made "still clipped" the passing state. The regression it was
// guarding (a modal that clips rather than reflows) is still guarded at the CLI
// layer by TestTUITestConfigPickerWidthRegression below, which drives a box that
// genuinely overflows and checks both the scenario-failure exit code and the
// modal named in the JSON.
func TestTUITestActionsMenuFitsTargetSizes(t *testing.T) {
	for _, size := range []struct{ w, h string }{{"80", "24"}, {"100", "24"}, {"120", "30"}, {"160", "45"}} {
		stdout, stderr, err := runRootArgs(t, "tui-test", "fixture", "actions",
			"--width", size.w, "--height", size.h, "--key", "a", "--json")
		if err != nil {
			t.Errorf("actions menu at %sx%s should exit cleanly, got error %v (stderr=%q)\noutput=%s",
				size.w, size.h, err, stderr, stdout)
			continue
		}
		for _, issue := range []tui.UITestLayoutIssueType{
			tui.UITestIssueModalWidthOverflow,
			tui.UITestIssueModalHeightOverflow,
			tui.UITestIssueModalClippedByRoot,
			tui.UITestIssueLineWidthOverflow,
		} {
			if strings.Contains(stdout, string(issue)) {
				t.Errorf("actions menu at %sx%s reports %s:\n%s", size.w, size.h, issue, stdout)
			}
		}
		// The menu is there rather than merely not-overflowing, and the entry a
		// fresh menu opens on is the one under the cursor.
		if !strings.Contains(stdout, "Actions for") {
			t.Errorf("actions menu at %sx%s did not render its header:\n%s", size.w, size.h, stdout)
		}
		final := finalObservation(t, stdout)
		if final.Mode != "actions" {
			t.Errorf("actions menu at %sx%s left mode %q", size.w, size.h, final.Mode)
		}
		if final.SelectedAction != "session.focus" {
			t.Errorf("actions menu at %sx%s reports %q selected, want session.focus", size.w, size.h, final.SelectedAction)
		}
	}
}

// finalObservation decodes the harness's per-step observation array and returns
// the last one, which is the state the keys left the model in.
func finalObservation(t *testing.T, stdout string) tui.UITestObservation {
	t.Helper()
	var obs []tui.UITestObservation
	if err := json.Unmarshal([]byte(stdout), &obs); err != nil {
		t.Fatalf("output is not a JSON observation array: %v\noutput=%s", err, stdout)
	}
	if len(obs) == 0 {
		t.Fatalf("no observations in output=%s", stdout)
	}
	return obs[len(obs)-1]
}

// TestTUITestActionsMenuSelectionMoves drives the menu through the CLI the way
// an operator does — open it, step down twice, and read back which action is
// selected — so the harness's own report of the selection is exercised end to
// end rather than only in-package.
func TestTUITestActionsMenuSelectionMoves(t *testing.T) {
	stdout, stderr, err := runRootArgs(t, "tui-test", "fixture", "actions",
		"--width", "100", "--height", "30", "--key", "a", "--key", "j", "--key", "j", "--json")
	if err != nil {
		t.Fatalf("stepping through the menu should exit cleanly, got %v (stderr=%q)", err, stderr)
	}
	if got := finalObservation(t, stdout).SelectedAction; got != "session.launch-another" {
		t.Errorf("two steps down from Open session selected %q, want session.launch-another", got)
	}
}

// Long profile homes belong in the selected detail, so both narrow and wide
// pickers must fit. Assert the final mode too: a key that opens nothing must
// not turn this layout regression into a false pass.
func TestTUITestConfigPickerWidthRegression(t *testing.T) {
	for _, width := range []string{"80", "120", "140"} {
		stdout, stderr, err := runRootArgs(t, "tui-test", "fixture", "wide_profiles",
			"--width", width, "--height", "24", "--key", "a", "--key", "C", "--json")
		if err != nil {
			t.Fatalf("config picker at %s columns: %v (stderr=%q)", width, err, stderr)
		}
		if got := finalObservation(t, stdout).Mode; got != "config_picker" {
			t.Errorf("at %s columns got mode %q, want config_picker", width, got)
		}
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
		Config: "../../testdata/uitest/basic_config.toml",
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
