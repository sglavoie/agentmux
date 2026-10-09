package main

import (
	"os"
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
