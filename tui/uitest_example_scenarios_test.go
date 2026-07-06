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

	// The four documented layout classes must all be present so the examples
	// stay in lock-step with the docs.
	want := map[string]bool{
		"baseline-layout.json":   false,
		"actions-popup-fit.json": false,
		"config-picker-fit.json": false,
		"resize-stress.json":     false,
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
		if _, tracked := want[name]; tracked {
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
			t.Errorf("documented example scenario %s is missing", name)
		}
	}
}
