//go:build tuitest

package tui

import (
	"os"
	"strings"
	"testing"
)

// TestUITestRunnerCapturesObservationPerStep verifies runUITestModel returns one
// observation for the initial render plus one after each action, with modes and
// sizes reflecting the applied steps.
func TestUITestRunnerCapturesObservationPerStep(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	m = loadSessions(m, makeSessions())

	actions := []UITestAction{
		{Type: UITestActionResize, Width: 90, Height: 28},
		{Type: UITestActionKey, Key: "a"},
	}
	obs, err := runUITestModel(m, UITestOptions{Size: UITestSize{Width: 80, Height: 24}}, actions)
	if err != nil {
		t.Fatalf("runUITestModel error: %v", err)
	}
	if len(obs) != len(actions)+1 {
		t.Fatalf("expected %d observations, got %d", len(actions)+1, len(obs))
	}

	if obs[0].Mode != "list" || obs[0].Size != (UITestSize{Width: 80, Height: 24}) {
		t.Errorf("initial observation wrong: %+v", obs[0])
	}
	if obs[1].Mode != "list" || obs[1].Size != (UITestSize{Width: 90, Height: 28}) {
		t.Errorf("post-resize observation wrong: %+v", obs[1])
	}
	if obs[2].Mode != "actions" {
		t.Errorf("expected actions mode after 'a', got %q", obs[2].Mode)
	}
}

// TestUITestRunnerStopsOnBadAction verifies an invalid action aborts the run,
// returning the observations captured before the failure and an error.
func TestUITestRunnerStopsOnBadAction(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	m = loadSessions(m, makeSessions())

	actions := []UITestAction{
		{Type: UITestActionResize, Width: 90, Height: 28},
		{Type: UITestActionType("bogus")},
		{Type: UITestActionKey, Key: "a"},
	}
	obs, err := runUITestModel(m, UITestOptions{Size: UITestSize{Width: 80, Height: 24}}, actions)
	if err == nil {
		t.Fatal("expected error for unknown action type")
	}
	// Initial + the one action that ran before the failure.
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations before failure, got %d", len(obs))
	}
}

// TestUITestRunnerFromConfigBuildsFreshModel verifies runUITest constructs a
// model from config + client and renders at the requested size with no actions.
func TestUITestRunnerFromConfigBuildsFreshModel(t *testing.T) {
	t.Parallel()

	base := testModelWithSessions()
	obs, err := runUITest(base.cfg, base.client, UITestOptions{Size: UITestSize{Width: 80, Height: 24}}, nil)
	if err != nil {
		t.Fatalf("runUITest error: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("expected 1 observation with no actions, got %d", len(obs))
	}
	if obs[0].Mode != "list" {
		t.Errorf("expected list mode, got %q", obs[0].Mode)
	}
	if obs[0].Screen.Size != (UITestSize{Width: 80, Height: 24}) {
		t.Errorf("expected screen size 80x24, got %+v", obs[0].Screen.Size)
	}
}

// TestRunUITestScenarioConfigModeWithWindows exercises the isolated config path
// end to end with live sessions. Unlike the CLI (which passes empty windows and
// renders an empty dashboard), a package-level scenario can carry fake Kitty
// windows, so this proves config.Load runs against a staged throwaway HOME, the
// session list populates from those windows, and text/mode assertions hold. It
// also confirms the run reads only the staged HOME under the temp root, never a
// real user path.
func TestRunUITestScenarioConfigModeWithWindows(t *testing.T) {
	root := t.TempDir()
	env := UITestScenarioEnv{
		Root: root,
		// t.Setenv restores every mutated variable when the test ends, so the
		// staged HOME/AMX_CONFIG/XDG values never leak into later tests.
		Setenv:  func(k, v string) error { t.Setenv(k, v); return nil },
		Windows: basicFixture().Windows,
	}
	sc := UITestScenario{
		Name:   "config-live",
		Config: "../testdata/uitest/basic_config.toml",
		Size:   UITestSize{Width: 120, Height: 40},
		Assertions: []UITestAssertion{
			{Type: UITestAssertModeEquals, Mode: "list"},
			{Type: UITestAssertNoLayoutIssues},
			{Type: UITestAssertContainsText, Text: "api"},
			{Type: UITestAssertContainsText, Text: "dotfiles"},
		},
	}

	res, err := RunUITestScenario(sc, env)
	if err != nil {
		t.Fatalf("config-backed scenario returned error: %v", err)
	}
	if !res.OK {
		t.Fatalf("config-backed scenario failed: %+v", res.Failures)
	}

	// The staged HOME must live under the temp root, proving the run never read
	// the operator's real home directory.
	if home := os.Getenv("HOME"); !strings.HasPrefix(home, root) {
		t.Errorf("expected staged HOME under %s, got %q", root, home)
	}
}
