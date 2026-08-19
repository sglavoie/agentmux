package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

type fakeGitMetadataProvider struct {
	metadata map[string]gitProjectMetadata
	err      error
}

func (p fakeGitMetadataProvider) GitMetadata(dir string) (gitProjectMetadata, error) {
	if p.err != nil {
		return gitProjectMetadata{}, p.err
	}
	meta, ok := p.metadata[dir]
	if !ok {
		return gitProjectMetadata{}, errors.New("git metadata not found")
	}
	return meta, nil
}

func fakeGitMetadata(root string, worktreeRoots ...string) gitProjectMetadata {
	return gitProjectMetadata{
		worktreeRoot:  root,
		worktreeRoots: worktreeRoots,
	}
}

type blockingGitMetadataProvider struct {
	metadata map[string]gitProjectMetadata
	blockDir string
	release  <-chan struct{}
	started  chan string
}

func (p blockingGitMetadataProvider) GitMetadata(dir string) (gitProjectMetadata, error) {
	p.started <- dir
	if dir == p.blockDir {
		<-p.release
	}
	meta, ok := p.metadata[dir]
	if !ok {
		return gitProjectMetadata{}, errors.New("git metadata not found")
	}
	return meta, nil
}

func TestGitWorktreeListPorcelainZParsesPathsWithSpaces(t *testing.T) {
	roots, err := parseGitWorktreeListPorcelainZ([]byte(
		"worktree /tmp/repo with space\x00HEAD abc\x00branch refs/heads/main\x00\x00" +
			"worktree /tmp/linked worktree\x00HEAD def\x00branch refs/heads/feature\x00\x00",
	))
	if err != nil {
		t.Fatalf("parseGitWorktreeListPorcelainZ: %v", err)
	}
	want := []string{
		filepath.Clean("/tmp/repo with space"),
		filepath.Clean("/tmp/linked worktree"),
	}
	if !equalStrings(roots, want) {
		t.Fatalf("worktree roots = %#v, want %#v", roots, want)
	}
}

func TestGitWorktreeListPorcelainZRejectsMalformedOutput(t *testing.T) {
	tests := map[string][]byte{
		"empty":             []byte(""),
		"missing worktree":  []byte("HEAD abc\x00\x00"),
		"empty path":        []byte("worktree \x00\x00"),
		"missing separator": []byte("worktree /tmp/repo\x00worktree /tmp/linked\x00\x00"),
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if got, err := parseGitWorktreeListPorcelainZ(input); err == nil {
				t.Fatalf("parseGitWorktreeListPorcelainZ() = %#v, want error", got)
			}
		})
	}
}

func TestGitRevParseRootParsesPath(t *testing.T) {
	root, err := parseGitRevParseRoot([]byte("/tmp/repo with space\n"))
	if err != nil {
		t.Fatalf("parseGitRevParseRoot: %v", err)
	}
	if want := filepath.Clean("/tmp/repo with space"); root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
}

func TestGitRevParseRootRejectsMalformedOutput(t *testing.T) {
	tests := map[string][]byte{
		"empty":      []byte(""),
		"two lines":  []byte("/tmp/repo\n/tmp/repo/.git\n"),
		"empty root": []byte("\n"),
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if root, err := parseGitRevParseRoot(input); err == nil {
				t.Fatalf("parseGitRevParseRoot() = %q, want error", root)
			}
		})
	}
}

func TestGitProjectMetadataProviderFailsClosedForMissingOrFile(t *testing.T) {
	provider := newGitProjectMetadataProvider()
	if got, err := provider.GitMetadata(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatalf("GitMetadata(missing) = %#v, want error", got)
	}

	file := filepath.Join(t.TempDir(), "project.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got, err := provider.GitMetadata(file); err == nil {
		t.Fatalf("GitMetadata(file) = %#v, want error", got)
	}
}

func TestGitProjectMetadataProviderDiscoversRepositoryAndWorktrees(t *testing.T) {
	git := requireGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo with space")
	linked := filepath.Join(root, "linked worktree")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	runGit(t, git, repo, "init")
	runGit(t, git, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	projectDir := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	runGit(t, git, repo, "worktree", "add", "-b", "feature/test", linked)
	linkedCWD := filepath.Join(linked, "services", "api", "cmd")
	if err := os.MkdirAll(linkedCWD, 0o755); err != nil {
		t.Fatalf("MkdirAll linked cwd: %v", err)
	}

	provider := newGitProjectMetadataProvider()
	meta, err := provider.GitMetadata(projectDir)
	if err != nil {
		t.Fatalf("GitMetadata: %v", err)
	}
	if absPath(meta.worktreeRoot) != absPath(repo) {
		t.Errorf("worktreeRoot = %q, want %q", meta.worktreeRoot, repo)
	}
	if !containsAbsPath(meta.worktreeRoots, repo) {
		t.Errorf("worktreeRoots = %#v, want main repo", meta.worktreeRoots)
	}
	if !containsAbsPath(meta.worktreeRoots, linked) {
		t.Errorf("worktreeRoots = %#v, want linked worktree", meta.worktreeRoots)
	}

	resolver := newProjectResolverWithGit(
		[]config.Session{{Title: "API", Directory: projectDir, Profile: "api-profile"}},
		provider,
	)
	if got := resolver.resolve(linkedCWD); got != (projectMatch{title: "API", profile: "api-profile"}) {
		t.Errorf("linked worktree resolve = %#v, want API match", got)
	}
}

func TestResolveProjectTitle(t *testing.T) {
	projects := []config.Session{
		{Title: "API", Directory: "/abs/projects/api"},
		{Title: "Web", Directory: "/abs/projects/web"},
	}
	if got := resolveProjectTitle("/abs/projects/api", projects); got != "API" {
		t.Errorf("exact match = %q, want API", got)
	}
	// Path with trailing slash and "." segments should still match.
	if got := resolveProjectTitle("/abs/projects/./api/", projects); got != "API" {
		t.Errorf("normalized match = %q, want API", got)
	}
	// Unconfigured cwd falls back to basename.
	if got := resolveProjectTitle("/abs/projects/other", projects); got != "other" {
		t.Errorf("fallback basename = %q, want other", got)
	}
	if got := resolveProjectTitle("", projects); got != "" {
		t.Errorf("empty cwd should yield empty title, got %q", got)
	}
}

func TestResolveProjectTitleMatchesSubdirectories(t *testing.T) {
	projects := []config.Session{
		{Title: "API", Directory: "/abs/projects/api"},
		{Title: "Web", Directory: "/abs/projects/web"},
	}
	// A session run in a subdirectory groups under the parent project.
	if got := resolveProjectTitle("/abs/projects/api/java-tts", projects); got != "API" {
		t.Errorf("subdir match = %q, want API", got)
	}
	// Deeper nesting still resolves to the ancestor project.
	if got := resolveProjectTitle("/abs/projects/api/a/b/c", projects); got != "API" {
		t.Errorf("deep subdir match = %q, want API", got)
	}
	// Path-boundary: a sibling with the project title as a name prefix must
	// NOT match ("/abs/projects/api" must not match "/abs/projects/api-v2").
	if got := resolveProjectTitle("/abs/projects/api-v2", projects); got != "api-v2" {
		t.Errorf("boundary non-match = %q, want api-v2 (basename)", got)
	}
}

func TestResolveProjectTitleLongestPrefixWins(t *testing.T) {
	// The more specific (longer) ancestor directory wins when one project
	// directory is nested inside another.
	projects := []config.Session{
		{Title: "Outer", Directory: "/abs/projects/mono"},
		{Title: "Inner", Directory: "/abs/projects/mono/service"},
	}
	if got := resolveProjectTitle("/abs/projects/mono/service/pkg", projects); got != "Inner" {
		t.Errorf("longest-prefix match = %q, want Inner", got)
	}
	if got := resolveProjectTitle("/abs/projects/mono/other", projects); got != "Outer" {
		t.Errorf("outer subdir match = %q, want Outer", got)
	}
	// Exact match on the outer directory still prefers the exact project.
	if got := resolveProjectTitle("/abs/projects/mono", projects); got != "Outer" {
		t.Errorf("exact outer match = %q, want Outer", got)
	}
}

func TestResolveProjectResolverPreservesConfiguredProjectMatching(t *testing.T) {
	resolver := newProjectResolver([]config.Session{
		{Title: "API", Directory: "/abs/projects/api", Account: "api-account"},
		{Title: "Outer", Directory: "/abs/projects/mono", Profile: "outer-profile"},
		{Title: "Inner", Directory: "/abs/projects/mono/service", Profile: "inner-profile"},
		{Title: "Ignored", Directory: ""},
	})

	tests := []struct {
		name string
		cwd  string
		want projectMatch
	}{
		{
			name: "exact match",
			cwd:  "/abs/projects/api",
			want: projectMatch{title: "API", profile: "api-account"},
		},
		{
			name: "normalized exact match",
			cwd:  "/abs/projects/./api/",
			want: projectMatch{title: "API", profile: "api-account"},
		},
		{
			name: "subdirectory match",
			cwd:  "/abs/projects/api/java-tts",
			want: projectMatch{title: "API", profile: "api-account"},
		},
		{
			name: "longest prefix match",
			cwd:  "/abs/projects/mono/service/pkg",
			want: projectMatch{title: "Inner", profile: "inner-profile"},
		},
		{
			name: "shorter ancestor match",
			cwd:  "/abs/projects/mono/other",
			want: projectMatch{title: "Outer", profile: "outer-profile"},
		},
		{
			name: "path boundary fallback",
			cwd:  "/abs/projects/api-v2",
			want: projectMatch{title: "api-v2"},
		},
		{
			name: "unconfigured cwd fallback",
			cwd:  "/abs/projects/other",
			want: projectMatch{title: "other"},
		},
		{
			name: "empty cwd",
			cwd:  "",
			want: projectMatch{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolver.resolve(tt.cwd); got != tt.want {
				t.Errorf("resolve(%q) = %#v, want %#v", tt.cwd, got, tt.want)
			}
		})
	}
}
