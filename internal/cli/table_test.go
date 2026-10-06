package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/layout"
)

// An emoji title is two cells wide where tabwriter counts one, so the columns
// after it must still start at the same cell on every line.
func TestWriteTableAlignsByDisplayWidth(t *testing.T) {
	var out bytes.Buffer
	err := WriteTable(&out, []string{"TITLE", "HARNESS", "DIR"}, [][]string{
		{"🐱amx", "codex", "/a"},
		{"spec", "claude", "/b"},
		{"🗣️commscoach", "codex", "/c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %q", len(lines), out.String())
	}
	want := -1
	for _, line := range lines {
		col := strings.Index(line, "codex")
		if col < 0 {
			col = strings.Index(line, "claude")
		}
		if col < 0 {
			col = strings.Index(line, "HARNESS")
		}
		cell := layout.Width(line[:col])
		if want < 0 {
			want = cell
		}
		if cell != want {
			t.Errorf("column 2 starts at cell %d in %q, want %d", cell, line, want)
		}
		if strings.HasSuffix(line, " ") {
			t.Errorf("trailing padding in %q", line)
		}
	}
}

func TestIsTerminalIsFalseForBuffersAndPipes(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Error("a buffer is not a terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	if IsTerminal(w) {
		t.Error("a pipe is not a terminal")
	}
}

func TestDisplayPathCleansAndAbbreviatesHome(t *testing.T) {
	t.Setenv("HOME", "/Users/me")
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"/Users/me", "~"},
		{"/Users/me/", "~"},
		{"/Users/me/projects/api/", "~/projects/api"},
		{"/Users/meadow/api", "/Users/meadow/api"},
		{"~/projects/api", "~/projects/api"},
		{"/srv/app//x/", "/srv/app/x"},
	} {
		if got := DisplayPath(tc.in); got != tc.want {
			t.Errorf("DisplayPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
