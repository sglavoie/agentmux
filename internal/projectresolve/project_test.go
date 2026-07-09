package projectresolve

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

type fakeGitMetadataProvider struct {
	metadata map[string]gitMetadata
	err      error
}

func (p fakeGitMetadataProvider) GitMetadata(dir string) (gitMetadata, error) {
	if p.err != nil {
		return gitMetadata{}, p.err
	}
	meta, ok := p.metadata[dir]
	if !ok {
		return gitMetadata{}, errors.New("git metadata not found")
	}
	return meta, nil
}

func fakeGitMetadata(root string, worktreeRoots ...string) gitMetadata {
	return gitMetadata{
		worktreeRoot:  root,
		worktreeRoots: worktreeRoots,
	}
}

func TestResolverMatchesLinkedWorktreeRoot(t *testing.T) {
	repo := filepath.Join(string(filepath.Separator), "repos", "app")
	linked := filepath.Join(string(filepath.Separator), "worktrees", "app-security")
	provider := fakeGitMetadataProvider{metadata: map[string]gitMetadata{
		repo: fakeGitMetadata(repo, repo, linked),
	}}
	resolver := newWithGitProvider(
		[]config.Session{{Title: "sis", Directory: repo, Profile: "vta-claude"}},
		provider,
	)

	got, ok := resolver.Resolve(linked)
	if !ok {
		t.Fatal("Resolve(linked worktree) returned no match")
	}
	if got.Title != "sis" || got.Profile != "vta-claude" {
		t.Fatalf("Resolve(linked worktree) = %#v, want sis/vta-claude", got)
	}
}

func TestResolverMatchesLinkedWorktreeSubproject(t *testing.T) {
	repo := filepath.Join(string(filepath.Separator), "repos", "mono")
	api := filepath.Join(repo, "services", "api")
	linked := filepath.Join(string(filepath.Separator), "worktrees", "mono-feature")
	provider := fakeGitMetadataProvider{metadata: map[string]gitMetadata{
		api: fakeGitMetadata(repo, repo, linked),
	}}
	resolver := newWithGitProvider(
		[]config.Session{{Title: "API", Directory: api, Profile: "api-profile"}},
		provider,
	)

	got, ok := resolver.Resolve(filepath.Join(linked, "services", "api", "cmd"))
	if !ok {
		t.Fatal("Resolve(linked subproject) returned no match")
	}
	if got.Title != "API" || got.Profile != "api-profile" {
		t.Fatalf("Resolve(linked subproject) = %#v, want API/api-profile", got)
	}
}

func TestResolverMatchesReturnsAmbiguousBestMatches(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	provider := fakeGitMetadataProvider{metadata: map[string]gitMetadata{
		root: fakeGitMetadata(root, root, filepath.Join(string(filepath.Separator), "wt")),
	}}
	resolver := newWithGitProvider(
		[]config.Session{
			{Title: "A", Directory: root, Profile: "one"},
			{Title: "B", Directory: root, Profile: "two"},
		},
		provider,
	)

	matches := resolver.Matches(filepath.Join(string(filepath.Separator), "wt", "src"))
	if len(matches) != 2 {
		t.Fatalf("Matches(linked worktree) returned %d matches, want 2: %#v", len(matches), matches)
	}
}

func TestResolverGitProviderErrorFallsBack(t *testing.T) {
	resolver := newWithGitProvider(
		[]config.Session{{
			Title:     "Repo",
			Directory: filepath.Join(string(filepath.Separator), "repos", "app"),
			Profile:   "repo-profile",
		}},
		fakeGitMetadataProvider{err: errors.New("git unavailable")},
	)

	got := resolver.ResolveOrFallback(filepath.Join(string(filepath.Separator), "worktrees", "app-security"))
	if got.Title != "app-security" || got.Profile != "" {
		t.Fatalf("ResolveOrFallback without git = %#v, want basename fallback", got)
	}
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

func TestGitMetadataProviderDiscoversRepositoryAndWorktrees(t *testing.T) {
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

	provider := realGitMetadataProvider{timeout: gitMetadataCommandTimeout}
	meta, err := provider.GitMetadata(projectDir)
	if err != nil {
		t.Fatalf("GitMetadata: %v", err)
	}
	if AbsPath(meta.worktreeRoot) != AbsPath(repo) {
		t.Errorf("worktreeRoot = %q, want %q", meta.worktreeRoot, repo)
	}
	if !containsAbsPath(meta.worktreeRoots, repo) {
		t.Errorf("worktreeRoots = %#v, want main repo", meta.worktreeRoots)
	}
	if !containsAbsPath(meta.worktreeRoots, linked) {
		t.Errorf("worktreeRoots = %#v, want linked worktree", meta.worktreeRoots)
	}

	resolver := NewWithGit([]config.Session{{Title: "API", Directory: projectDir, Profile: "api-profile"}})
	got, ok := resolver.Resolve(linkedCWD)
	if !ok || got.Title != "API" || got.Profile != "api-profile" {
		t.Errorf("linked worktree resolve = %#v, %v; want API match", got, ok)
	}
}

func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	return git
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Agent Mux",
		"GIT_AUTHOR_EMAIL=agentmux@example.invalid",
		"GIT_COMMITTER_NAME=Agent Mux",
		"GIT_COMMITTER_EMAIL=agentmux@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func containsAbsPath(paths []string, want string) bool {
	want = AbsPath(want)
	for _, path := range paths {
		if AbsPath(path) == want {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
