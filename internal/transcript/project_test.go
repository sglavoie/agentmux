package transcript

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

func TestProjectResolverGitWorktreeEquivalentMatching(t *testing.T) {
	repo := filepath.Join(string(filepath.Separator), "repos", "main")
	linked := filepath.Join(string(filepath.Separator), "worktrees", "feature")
	api := filepath.Join(repo, "services", "api")
	payments := filepath.Join(api, "payments")
	provider := fakeGitMetadataProvider{metadata: map[string]gitProjectMetadata{
		repo:     fakeGitMetadata(repo, repo, linked),
		api:      fakeGitMetadata(repo, repo, linked),
		payments: fakeGitMetadata(repo, repo, linked),
	}}

	tests := []struct {
		name     string
		projects []config.Session
		cwd      string
		want     projectMatch
	}{
		{
			name:     "sibling linked worktree root",
			projects: []config.Session{{Title: "Repo", Directory: repo, Profile: "repo-profile"}},
			cwd:      linked,
			want:     projectMatch{title: "Repo", profile: "repo-profile"},
		},
		{
			name:     "deep cwd inside linked worktree",
			projects: []config.Session{{Title: "Repo", Directory: repo, Profile: "repo-profile"}},
			cwd:      filepath.Join(linked, "pkg", "sub"),
			want:     projectMatch{title: "Repo", profile: "repo-profile"},
		},
		{
			name:     "monorepo subproject relative matching",
			projects: []config.Session{{Title: "API", Directory: api, Profile: "api-profile"}},
			cwd:      filepath.Join(linked, "services", "api", "cmd"),
			want:     projectMatch{title: "API", profile: "api-profile"},
		},
		{
			name: "longest relative subproject wins",
			projects: []config.Session{
				{Title: "Repo", Directory: repo, Profile: "repo-profile"},
				{Title: "API", Directory: api, Profile: "api-profile"},
				{Title: "Payments", Directory: payments, Profile: "payments-profile"},
			},
			cwd:  filepath.Join(linked, "services", "api", "payments", "internal"),
			want: projectMatch{title: "Payments", profile: "payments-profile"},
		},
		{
			name:     "unrelated repo non-match",
			projects: []config.Session{{Title: "API", Directory: api, Profile: "api-profile"}},
			cwd:      filepath.Join(string(filepath.Separator), "other", "repo", "services", "api", "cmd"),
			want:     projectMatch{title: "cmd"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := newProjectResolverWithGit(tt.projects, provider)
			if got := resolver.resolve(tt.cwd); got != tt.want {
				t.Errorf("resolve(%q) = %#v, want %#v", tt.cwd, got, tt.want)
			}
		})
	}
}

func TestProjectResolverProbesGitMetadataConcurrentlyAndPreservesOrder(t *testing.T) {
	rootA := filepath.Join(string(filepath.Separator), "repos", "a")
	rootB := filepath.Join(string(filepath.Separator), "repos", "b")
	dirA := filepath.Join(rootA, "service")
	dirB := filepath.Join(rootB, "service")
	linked := filepath.Join(string(filepath.Separator), "worktrees", "linked")
	release := make(chan struct{})
	provider := blockingGitMetadataProvider{
		metadata: map[string]gitProjectMetadata{
			dirA: fakeGitMetadata(rootA, linked),
			dirB: fakeGitMetadata(rootB, linked),
		},
		blockDir: dirA,
		release:  release,
		started:  make(chan string, 2),
	}

	done := make(chan projectResolver, 1)
	go func() {
		done <- newProjectResolverWithGit([]config.Session{
			{Title: "First", Directory: dirA, Profile: "first-profile"},
			{Title: "Second", Directory: dirB, Profile: "second-profile"},
		}, provider)
	}()

	started := map[string]bool{}
	for len(started) < 2 {
		select {
		case dir := <-provider.started:
			started[dir] = true
		case <-time.After(time.Second):
			close(release)
			t.Fatalf("timed out waiting for concurrent metadata probes; started=%#v", started)
		}
	}
	if !started[dirA] || !started[dirB] {
		close(release)
		t.Fatalf("started probes = %#v, want both configured project dirs", started)
	}

	close(release)
	var resolver projectResolver
	select {
	case resolver = <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resolver construction")
	}

	if got, want := resolver.projects[0].title, "First"; got != want {
		t.Fatalf("first resolver project = %q, want %q", got, want)
	}
	cwd := filepath.Join(linked, "service", "cmd")
	if got := resolver.resolve(cwd); got != (projectMatch{title: "First", profile: "first-profile"}) {
		t.Errorf("resolve(%q) = %#v, want First match by configured order", cwd, got)
	}
}

func TestProjectResolverGitProviderErrorFallsBack(t *testing.T) {
	repo := filepath.Join(string(filepath.Separator), "repos", "main")
	linked := filepath.Join(string(filepath.Separator), "worktrees", "feature")
	resolver := newProjectResolverWithGit(
		[]config.Session{{Title: "Repo", Directory: repo, Profile: "repo-profile"}},
		fakeGitMetadataProvider{err: errors.New("git unavailable")},
	)

	if got := resolver.resolve(filepath.Join(repo, "pkg")); got != (projectMatch{title: "Repo", profile: "repo-profile"}) {
		t.Errorf("configured ancestor fallback = %#v, want Repo match", got)
	}
	if got := resolver.resolve(filepath.Join(linked, "pkg")); got != (projectMatch{title: "pkg"}) {
		t.Errorf("linked worktree fallback = %#v, want basename fallback", got)
	}
}

func TestResolveProjectResolverExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, "workspace", "api")
	if err := os.MkdirAll(filepath.Join(projectDir, "pkg"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	resolver := newProjectResolver([]config.Session{
		{Title: "API", Directory: "~/workspace/api"},
	})
	if got := resolver.resolve(filepath.Join(projectDir, "pkg")); got.title != "API" {
		t.Errorf("tilde project match = %#v, want title API", got)
	}
}

func TestResolveProjectResolverMatchesSymlinkedSubdirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	resolver := newProjectResolver([]config.Session{{Title: "API", Directory: linkDir}})
	if got := resolver.resolve(filepath.Join(realDir, "sub")); got.title != "API" {
		t.Errorf("symlinked subdir match = %#v, want title API", got)
	}
}

func TestResolveProjectTitleMatchesSymlinkedSubdirectory(t *testing.T) {
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
	if got := resolveProjectTitle(filepath.Join(realDir, "sub"), projects); got != "API" {
		t.Errorf("symlinked subdir match = %q, want API", got)
	}
}

func TestResolveProjectTitleMatchesSymlinkEquivalentPaths(t *testing.T) {
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
	if got := resolveProjectTitle(realDir, projects); got != "API" {
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

func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	return git
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Agentmux Test",
		"GIT_AUTHOR_EMAIL=agentmux-test@example.invalid",
		"GIT_COMMITTER_NAME=Agentmux Test",
		"GIT_COMMITTER_EMAIL=agentmux-test@example.invalid",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func containsAbsPath(paths []string, want string) bool {
	want = absPath(want)
	for _, path := range paths {
		if absPath(path) == want {
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
