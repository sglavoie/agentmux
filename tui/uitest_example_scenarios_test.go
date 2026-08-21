package tui

// This test loads the committed example scenarios documented under
// docs/tui-testing/ and runs them through the same parser and package-level
// runner the hidden CLI uses. It exists so the documentation examples can never
// drift from the harness: a scenario that stops parsing, references a renamed
// fixture, or regresses a layout guarantee fails here in-package without
// shelling out to the CLI.

import (
	"os"
	"path/filepath"
	"testing"
)

// exampleScenariosDir is the committed scenario directory relative to this
// package (tui/ -> repo root -> docs/tui-testing/scenarios).
const exampleScenariosDir = "../docs/tui-testing/scenarios"

// TestExampleScenariosParseAndPass loads every committed example scenario,
// confirms it parses under the same validation the CLI uses, and runs it to a
// passing result. All committed examples are fixture-backed, so no config
// staging environment is needed.
func TestExampleScenariosParseAndPass(t *testing.T) {
	entries, err := os.ReadDir(exampleScenariosDir)
	if err != nil {
		t.Fatalf("reading example scenarios dir: %v", err)
	}

	var jsonFiles []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		jsonFiles = append(jsonFiles, e.Name())
	}
	if len(jsonFiles) == 0 {
		t.Fatalf("no example scenarios found in %s", exampleScenariosDir)
	}

	// Keep this exact inventory in lock-step with docs/tui-testing/README.md.
	// Every committed file is still executed below; rejecting untracked names
	// prevents the documentation and acceptance recipe from silently diverging.
	want := map[string]bool{
		"actions-popup-fit.json":                    false,
		"asl-abort-confirmation-fit.json":           false,
		"asl-question-pending-narrow-fit.json":      false,
		"asl-question-pending-wide.json":            false,
		"asl-runs-narrow-fit.json":                  false,
		"asl-runs-wide.json":                        false,
		"asl-response-live-wide.json":               false,
		"asl-response-paused-narrow-fit.json":       false,
		"baseline-layout.json":                      false,
		"command-palette-execute-help.json":         false,
		"command-palette-filter.json":               false,
		"command-palette-narrow-scroll.json":        false,
		"command-palette-wide.json":                 false,
		"config-picker-fit.json":                    false,
		"config-picker-selection-transition.json":   false,
		"contextual-help-from-session-view.json":    false,
		"footer-gloss-narrow-fit.json":              false,
		"help-modal-narrow-fit.json":                false,
		"help-modal-scroll-bottom.json":             false,
		"legend-narrow-fit.json":                    false,
		"list-preview-narrow-fit.json":              false,
		"picker-dsl-narrow-fit.json":                false,
		"picker-fit.json":                           false,
		"picker-preview-fit.json":                   false,
		"plan-search-header-narrow-fit.json":        false,
		"plan-search-narrow-fit.json":               false,
		"plan-search-sort-rebind.json":              false,
		"plan-view-fit.json":                        false,
		"plan-view-narrow-fit.json":                 false,
		"resize-stress.json":                        false,
		"search-narrow-fit.json":                    false,
		"session-fork-continuity.json":              false,
		"session-search-chips-fit.json":             false,
		"session-search-header-narrow-fit.json":     false,
		"session-view-hints-narrow-fit.json":        false,
		"session-view-hints-wide.json":              false,
		"worktrees-browse.json":                     false,
		"worktrees-cleanup-active-refusal.json":     false,
		"worktrees-cleanup-dirty-refusal.json":      false,
		"worktrees-cleanup-looping-refusal.json":    false,
		"worktrees-delete-merged-branch.json":       false,
		"worktrees-delete-unmerged-refusal.json":    false,
		"worktrees-forget-invalid.json":             false,
		"worktrees-forget-missing.json":             false,
		"worktrees-partial-recovery.json":           false,
		"worktrees-picker-narrow-fit.json":          false,
		"worktrees-remove-retain-branch.json":       false,
		"worktrees-reopen-ambiguous.json":           false,
		"worktrees-reopen-fresh.json":               false,
		"worktrees-reopen-live.json":                false,
		"worktrees-reopen-replacement-profile.json": false,
		"worktrees-reopen-resume.json":              false,
		"worktrees-short-layout.json":               false,
	}

	for _, name := range jsonFiles {
		path := filepath.Join(exampleScenariosDir, name)
		sc, err := LoadUITestScenario(path)
		if err != nil {
			t.Errorf("%s: load/parse failed: %v", name, err)
			continue
		}
		if sc.Config != "" {
			t.Errorf("%s: committed examples must be fixture-backed, got config %q", name, sc.Config)
			continue
		}
		if _, tracked := want[name]; !tracked {
			t.Errorf("undocumented scenario %s is not in the exact acceptance inventory", name)
		} else {
			want[name] = true
		}
		res, err := RunUITestScenario(sc, UITestScenarioEnv{})
		if err != nil {
			t.Errorf("%s: run failed: %v", name, err)
			continue
		}
		if !res.OK {
			t.Errorf("%s: scenario did not pass: %+v", name, res.Failures)
		}
	}

	for name, seen := range want {
		if !seen {
			t.Errorf("documented acceptance scenario %s is missing", name)
		}
	}
}
