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
	"regexp"
	"testing"

	"github.com/sglavoie/agentmux/internal/testutil"
)

// exampleScenariosDir is the committed scenario directory relative to this
// package (tui/ -> repo root -> docs/tui-testing/scenarios).
const exampleScenariosDir = "../docs/tui-testing/scenarios"

// scenarioReadme documents the committed scenarios, one table row each.
const scenarioReadme = "../docs/tui-testing/README.md"

// scenarioLink is a File cell of the inventory table: the file named twice,
// once as text and once as the link target.
var scenarioLink = regexp.MustCompile("^\\[`([^`/]+\\.json)`\\]\\(scenarios/([^)/]+\\.json)\\)$")

// documentedScenarioInventory reads the README's inventory table into a set
// of file names, each marked unseen.
func documentedScenarioInventory(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(scenarioReadme)
	if err != nil {
		t.Fatal(err)
	}
	_, rows, err := testutil.MarkdownTable(string(data), "| File | Fixture | Covers |")
	if err != nil {
		t.Fatalf("%s: %v", scenarioReadme, err)
	}
	inventory := make(map[string]bool, len(rows))
	for _, row := range rows {
		m := scenarioLink.FindStringSubmatch(row[0])
		if m == nil || m[1] != m[2] {
			t.Errorf("%s: inventory row must open with [`NAME.json`](scenarios/NAME.json), got %q", scenarioReadme, row[0])
			continue
		}
		if len(row) != 3 || row[1] == "" || row[2] == "" {
			t.Errorf("%s: %s must name its fixture and what it covers: %q", scenarioReadme, m[1], row)
		}
		if _, dup := inventory[m[1]]; dup {
			t.Errorf("%s: %s is listed twice", scenarioReadme, m[1])
		}
		inventory[m[1]] = false
	}
	if len(inventory) == 0 {
		t.Fatalf("%s: the inventory table lists no scenarios", scenarioReadme)
	}
	return inventory
}

// TestExampleScenariosParseAndPass loads every committed example scenario,
// confirms it parses under the same validation the CLI uses, and runs it to a
// passing result. All committed examples are fixture-backed, so no config
// staging environment is needed, and nothing a fixture scenario does touches
// the process environment or a file another scenario reads, so each runs as
// a parallel subtest.
func TestExampleScenariosParseAndPass(t *testing.T) {
	t.Parallel()
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

	// The README's "Committed scenarios" table is the exact inventory. Every
	// committed file is still executed below; rejecting names the table does
	// not list, and listed names with no file, keeps the documentation and the
	// acceptance run from silently diverging.
	want := documentedScenarioInventory(t)
	for _, name := range jsonFiles {
		if _, tracked := want[name]; !tracked {
			t.Errorf("undocumented scenario %s is not in the inventory table of %s", name, scenarioReadme)
		} else {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("%s lists scenario %s, which has no file", scenarioReadme, name)
		}
	}

	for _, name := range jsonFiles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sc, err := LoadUITestScenario(filepath.Join(exampleScenariosDir, name))
			if err != nil {
				t.Fatalf("load/parse failed: %v", err)
			}
			// A config scenario stages its environment with Setenv, which a
			// parallel subtest may not do, and the committed set is fixtures.
			if sc.Config != "" {
				t.Fatalf("committed examples must be fixture-backed, got config %q", sc.Config)
			}
			res, err := RunUITestScenario(sc, UITestScenarioEnv{})
			if err != nil {
				t.Fatalf("run failed: %v", err)
			}
			if !res.OK {
				t.Fatalf("scenario did not pass: %+v", res.Failures)
			}
		})
	}
}
