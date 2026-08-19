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
