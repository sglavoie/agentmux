package transcript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestResolveProject(t *testing.T) {
	projects := []config.Session{
		{Title: "API", Directory: "/abs/projects/api"},
		{Title: "Web", Directory: "/abs/projects/web"},
	}
	if got := ResolveProject("/abs/projects/api", projects); got != "API" {
		t.Errorf("exact match = %q, want API", got)
	}
	// Path with trailing slash and "." segments should still match.
	if got := ResolveProject("/abs/projects/./api/", projects); got != "API" {
		t.Errorf("normalized match = %q, want API", got)
	}
	// Unconfigured cwd falls back to basename.
	if got := ResolveProject("/abs/projects/other", projects); got != "other" {
		t.Errorf("fallback basename = %q, want other", got)
	}
	if got := ResolveProject("", projects); got != "" {
		t.Errorf("empty cwd should yield empty title, got %q", got)
	}
}

func TestResolveProjectMatchesSymlinkEquivalentPaths(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	projects := []config.Session{{Title: "API", Directory: linkDir}}
	if got := ResolveProject(realDir, projects); got != "API" {
		t.Errorf("symlink-equivalent match = %q, want API", got)
	}
}

func TestAbsPathExpandsTilde(t *testing.T) {
	// Just verify the call does not panic and produces a sensible path —
	// the exact value depends on the test environment's $HOME.
	got := absPath("~/transcript-test")
	if got == "" || got[0] != filepath.Separator {
		t.Errorf("expected absolute path from ~, got %q", got)
	}
}

func TestAbsPathDoesNotExpandNamedTilde(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	input := "~definitely-not-current-user-agentmux/transcript-test"
	want := filepath.Clean(filepath.Join(cwd, input))
	if got := absPath(input); got != want {
		t.Errorf("named tilde path = %q, want %q", got, want)
	}
}
