package worktree

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestWorktreeListOnATerminalIsATableWithAHeader(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	worktreeListIsTerminal = func(io.Writer) bool { return true }

	var out bytes.Buffer
	worktreeListCmd.SetOut(&out)
	if err := worktreeListCmd.RunE(worktreeListCmd, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 1+len(manager.entries) || !strings.HasPrefix(lines[0], "ID") {
		t.Fatalf("table = %q, want a header and one row per worktree", out.String())
	}
	if strings.Contains(out.String(), "\t") {
		t.Errorf("table = %q, want aligned columns rather than tabs", out.String())
	}
	if !strings.Contains(lines[1], manager.entries[0].ID) || !strings.HasSuffix(lines[1], manager.entries[0].Path) {
		t.Errorf("row = %q", lines[1])
	}
}

func TestAnEmptyWorktreeListSaysSoOnlyOnATerminal(t *testing.T) {
	manager, _ := resetWorktreeCommandsTestState(t)
	manager.entries = nil

	var out bytes.Buffer
	worktreeListCmd.SetOut(&out)
	if err := worktreeListCmd.RunE(worktreeListCmd, nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("piped empty list = %q, want nothing for scripts", out.String())
	}

	worktreeListIsTerminal = func(io.Writer) bool { return true }
	if err := worktreeListCmd.RunE(worktreeListCmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "No managed worktrees.") {
		t.Fatalf("terminal empty list = %q", out.String())
	}
}
