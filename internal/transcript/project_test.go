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

func TestResolveProjectMatchesSubdirectories(t *testing.T) {
	projects := []config.Session{
		{Title: "API", Directory: "/abs/projects/api"},
		{Title: "Web", Directory: "/abs/projects/web"},
	}
	// A session run in a subdirectory groups under the parent project.
	if got := ResolveProject("/abs/projects/api/java-tts", projects); got != "API" {
		t.Errorf("subdir match = %q, want API", got)
	}
	// Deeper nesting still resolves to the ancestor project.
	if got := ResolveProject("/abs/projects/api/a/b/c", projects); got != "API" {
		t.Errorf("deep subdir match = %q, want API", got)
	}
	// Path-boundary: a sibling with the project title as a name prefix must
	// NOT match ("/abs/projects/api" must not match "/abs/projects/api-v2").
	if got := ResolveProject("/abs/projects/api-v2", projects); got != "api-v2" {
		t.Errorf("boundary non-match = %q, want api-v2 (basename)", got)
	}
}

func TestResolveProjectLongestPrefixWins(t *testing.T) {
	// The more specific (longer) ancestor directory wins when one project
	// directory is nested inside another.
	projects := []config.Session{
		{Title: "Outer", Directory: "/abs/projects/mono"},
		{Title: "Inner", Directory: "/abs/projects/mono/service"},
	}
	if got := ResolveProject("/abs/projects/mono/service/pkg", projects); got != "Inner" {
		t.Errorf("longest-prefix match = %q, want Inner", got)
	}
	if got := ResolveProject("/abs/projects/mono/other", projects); got != "Outer" {
		t.Errorf("outer subdir match = %q, want Outer", got)
	}
	// Exact match on the outer directory still prefers the exact project.
	if got := ResolveProject("/abs/projects/mono", projects); got != "Outer" {
		t.Errorf("exact outer match = %q, want Outer", got)
	}
}

func TestResolveProjectMatchesSymlinkedSubdirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	projects := []config.Session{{Title: "API", Directory: linkDir}}
	if got := ResolveProject(filepath.Join(realDir, "sub"), projects); got != "API" {
		t.Errorf("symlinked subdir match = %q, want API", got)
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
