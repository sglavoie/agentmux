package main

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/testutil"
)

// TestDaemonStateAcceptanceDoc_CodexUsesNativeHookSpellings guards the exact
// expected-state table in docs/acceptance/daemon-state.md against a regression
// where the Codex cells adopt Copilot's hook dialect. Codex fires native
// capitalized `Stop`/`UserPromptSubmit` hooks (see internal/hookevent/events.go
// and the installed live evidence in this document); Copilot's lowercase
// `agentStop`/`userPromptSubmitted` spellings must never reappear in the Codex
// column.
func TestDaemonStateAcceptanceDoc_CodexUsesNativeHookSpellings(t *testing.T) {
	data, err := os.ReadFile("docs/acceptance/daemon-state.md")
	if err != nil {
		t.Fatalf("ReadFile(docs/acceptance/daemon-state.md): %v", err)
	}

	cells := codexExactStateCells(t, string(data))

	cases := []struct {
		scenario string
		want     string
		forbid   []string
	}{
		{
			scenario: "Normal turn completion",
			want:     "Stop",
			forbid:   []string{"agentStop", "userPromptSubmitted"},
		},
		{
			scenario: "New prompt after stop",
			want:     "UserPromptSubmit",
			forbid:   []string{"agentStop", "userPromptSubmitted"},
		},
	}

	for _, tc := range cases {
		cell, ok := cells[tc.scenario]
		if !ok {
			t.Fatalf("exact-state table missing Codex cell for scenario %q", tc.scenario)
		}
		if !strings.Contains(cell, tc.want) {
			t.Fatalf("Codex cell for %q must document native %q, got: %q", tc.scenario, tc.want, cell)
		}
		for _, bad := range tc.forbid {
			if strings.Contains(cell, bad) {
				t.Fatalf("Codex cell for %q leaks Copilot hook spelling %q: %q", tc.scenario, bad, cell)
			}
		}
	}
}

// codexExactStateCells returns the Codex column cell text keyed by scenario for
// the "Exact expected states" table (header: Scenario | Codex | Claude |
// Copilot). Only rows inside that table are parsed.
func codexExactStateCells(t *testing.T, text string) map[string]string {
	t.Helper()

	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	inTable := false
	seenHeader := false
	cells := map[string]string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "### Exact expected states") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			if seenHeader {
				break // table ended
			}
			continue
		}

		fields := testutil.SplitTableRow(line)
		if !seenHeader {
			if len(fields) >= 4 && fields[0] == "Scenario" && fields[1] == "Codex" {
				seenHeader = true
			}
			continue
		}
		if testutil.IsDelimiterRow(fields) {
			continue
		}
		if len(fields) >= 2 {
			cells[fields[0]] = fields[1]
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning acceptance doc: %v", err)
	}
	if !seenHeader {
		t.Fatalf("could not find the Codex exact-state table header in the acceptance doc")
	}
	return cells
}
