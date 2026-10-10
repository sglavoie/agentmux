package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestBackendMigrationMatrixAssignsEveryWorkflow guards the workflow
// acceptance matrix in docs/backend-migration.md. Every row must name a
// dependency area from the inventory, a baseline status of pass or pending, an
// owning implementation session, and a non-empty acceptance check; every
// inventoried area must own at least one workflow. It pins the assignment, not
// the existence of the later checks themselves.
func TestBackendMigrationMatrixAssignsEveryWorkflow(t *testing.T) {
	data, err := os.ReadFile("docs/backend-migration.md")
	if err != nil {
		t.Fatalf("ReadFile(docs/backend-migration.md): %v", err)
	}

	areas := []string{"launch", "hooks", "state", "persistence", "capture", "tui", "companion", "diagnostics", "asl"}
	known := map[string]bool{}
	for _, a := range areas {
		known[a] = true
	}

	inventory := markdownTable(t, string(data), "| Area | Kitty dependency today |")
	for _, a := range areas {
		found := false
		for _, row := range inventory {
			if row[0] == a {
				found = true
			}
		}
		if !found {
			t.Errorf("dependency inventory has no %q row", a)
		}
	}

	rows := markdownTable(t, string(data), "| ID | Workflow | Area |")
	if len(rows) == 0 {
		t.Fatal("workflow acceptance matrix has no rows")
	}
	covered := map[string]bool{}
	ids := map[string]bool{}
	for _, row := range rows {
		if len(row) != 8 {
			t.Fatalf("matrix row has %d cells, want 8: %q", len(row), row)
		}
		id, workflow, area, evidence, baseline, owner, check := row[0], row[1], row[2], row[3], row[4], row[5], row[7]
		if ids[id] {
			t.Errorf("duplicate workflow ID %s", id)
		}
		ids[id] = true
		if workflow == "" || evidence == "" || check == "" {
			t.Errorf("%s: workflow, baseline evidence, and acceptance check must all be filled: %q", id, row)
		}
		if !known[area] {
			t.Errorf("%s: area %q is not an inventoried dependency area", id, area)
		}
		covered[area] = true
		if baseline != "pass" && baseline != "pending" {
			t.Errorf("%s: baseline %q must be pass or pending", id, baseline)
		}
		n, err := strconv.Atoi(owner)
		if err != nil || n < 3 || n > 20 {
			t.Errorf("%s: owner %q must be an implementation session between 3 and 20", id, owner)
		}
	}
	for _, a := range areas {
		if !covered[a] {
			t.Errorf("no workflow in the matrix belongs to area %q", a)
		}
	}
}

// markdownTable returns the trimmed body cells of the first table whose header
// line starts with headerPrefix.
func markdownTable(t *testing.T, doc, headerPrefix string) [][]string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, headerPrefix) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("table with header %q not found", headerPrefix)
	}
	var rows [][]string
	for _, line := range lines[start+2:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), " | ")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}
	return rows
}

// TestBackendMigrationSession19RecordCoversEveryWorkflow guards the Session 19
// acceptance record in docs/backend-migration.md. Every workflow of the matrix
// has exactly one row in it, marked passed or pending; a pending row says what
// is missing; and what a row cites exists: every test it names is a test in
// this repository and every file it names is a file. It does not prove a cited
// test passed, which is what the gates and the logs beside the record are for.
func TestBackendMigrationSession19RecordCoversEveryWorkflow(t *testing.T) {
	data, err := os.ReadFile("docs/backend-migration.md")
	if err != nil {
		t.Fatalf("ReadFile(docs/backend-migration.md): %v", err)
	}
	doc := string(data)

	want := map[string]bool{}
	for _, row := range markdownTable(t, doc, "| ID | Workflow | Area |") {
		want[row[0]] = true
	}
	tests := repositoryTestNames(t)

	seen := map[string]bool{}
	for _, row := range markdownTable(t, doc, "| ID | Session 19 | Evidence |") {
		if len(row) != 3 {
			t.Fatalf("record row has %d cells, want 3: %q", len(row), row)
		}
		id, status, evidence := row[0], row[1], row[2]
		if !want[id] {
			t.Errorf("%s: the record has a row for a workflow the matrix does not have", id)
		}
		if seen[id] {
			t.Errorf("%s: the record has two rows", id)
		}
		seen[id] = true
		switch status {
		case "passed":
		case "pending":
			if !strings.Contains(evidence, "Pending: ") {
				t.Errorf("%s: a pending row must say what is missing, after %q", id, "Pending: ")
			}
		default:
			t.Errorf("%s: status %q must be passed or pending", id, status)
		}
		if evidence == "" {
			t.Errorf("%s: no evidence", id)
		}
		for _, cited := range backticked(evidence) {
			switch {
			case strings.HasPrefix(cited, "Test") || strings.HasPrefix(cited, "test"):
				if !tests[cited] {
					t.Errorf("%s: cites %s, which is not a test in this repository", id, cited)
				}
			case strings.HasPrefix(cited, "docs/"):
				if _, err := os.Stat(cited); err != nil {
					t.Errorf("%s: cites %s: %v", id, cited, err)
				}
			}
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s: the record has no row", id)
		}
	}
}

// backticked returns the code spans of a table cell.
func backticked(cell string) []string {
	var spans []string
	parts := strings.Split(cell, "`")
	for i := 1; i < len(parts); i += 2 {
		spans = append(spans, parts[i])
	}
	return spans
}

// repositoryTestNames collects every Go test function and every hosted Swift
// test method in the tree, by name.
func repositoryTestNames(t *testing.T) map[string]bool {
	t.Helper()
	goTest := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	swiftTest := regexp.MustCompile(`func (test\w+)\(`)
	names := map[string]bool{}
	skip := map[string]bool{".git": true, ".claude": true, "Vendor": true, "DerivedData": true, "build": true, ".cache": true, "node_modules": true}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		var re *regexp.Regexp
		switch {
		case strings.HasSuffix(path, "_test.go"):
			re = goTest
		case strings.HasSuffix(path, ".swift") && strings.Contains(path, "AgentMuxTests"):
			re = swiftTest
		default:
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range re.FindAllStringSubmatch(string(source), -1) {
			names[match[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("collecting test names: %v", err)
	}
	return names
}
