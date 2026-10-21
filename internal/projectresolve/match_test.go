package projectresolve

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

// blockingGitMetadataProvider holds the probe for blockDir until release is
// closed and reports every probe it starts, so a test can prove the probes run
// concurrently rather than one after another.
type blockingGitMetadataProvider struct {
	metadata map[string]gitMetadata
	blockDir string
	release  <-chan struct{}
	started  chan string
}

func (p blockingGitMetadataProvider) GitMetadata(dir string) (gitMetadata, error) {
	p.started <- dir
	if dir == p.blockDir {
		<-p.release
	}
	meta, ok := p.metadata[dir]
	if !ok {
		return gitMetadata{}, errors.New("git metadata not found")
	}
	return meta, nil
}

func TestGitWorktreeListPorcelainZRejectsMalformedOutput(t *testing.T) {
	t.Parallel()

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

func TestGitMetadataProviderFailsClosedForMissingOrFile(t *testing.T) {
	t.Parallel()

	provider := realGitMetadataProvider{timeout: gitMetadataCommandTimeout}
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

func TestResolverPreservesConfiguredProjectMatching(t *testing.T) {
	t.Parallel()

	resolver := New([]config.Session{
		{Title: "API", Directory: "/abs/projects/api", Account: "api-account"},
		{Title: "Outer", Directory: "/abs/projects/mono", Profile: "outer-profile"},
		{Title: "Inner", Directory: "/abs/projects/mono/service", Profile: "inner-profile"},
		{Title: "Ignored", Directory: ""},
	})

	tests := []struct {
		name        string
		cwd         string
		wantTitle   string
		wantProfile string
	}{
		{name: "exact match", cwd: "/abs/projects/api", wantTitle: "API", wantProfile: "api-account"},
		{name: "normalized exact match", cwd: "/abs/projects/./api/", wantTitle: "API", wantProfile: "api-account"},
		{name: "subdirectory match", cwd: "/abs/projects/api/java-tts", wantTitle: "API", wantProfile: "api-account"},
		{name: "deep subdirectory match", cwd: "/abs/projects/api/a/b/c", wantTitle: "API", wantProfile: "api-account"},
		{name: "longest prefix match", cwd: "/abs/projects/mono/service/pkg", wantTitle: "Inner", wantProfile: "inner-profile"},
		{name: "shorter ancestor match", cwd: "/abs/projects/mono/other", wantTitle: "Outer", wantProfile: "outer-profile"},
		{name: "exact outer match", cwd: "/abs/projects/mono", wantTitle: "Outer", wantProfile: "outer-profile"},
		// "/abs/projects/api" must not match the name-prefixed sibling.
		{name: "path boundary fallback", cwd: "/abs/projects/api-v2", wantTitle: "api-v2"},
		{name: "unconfigured cwd fallback", cwd: "/abs/projects/other", wantTitle: "other"},
		{name: "empty cwd", cwd: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolver.ResolveOrFallback(tt.cwd)
			if got.Title != tt.wantTitle || got.Profile != tt.wantProfile {
				t.Errorf("ResolveOrFallback(%q) = %#v, want %s/%s", tt.cwd, got, tt.wantTitle, tt.wantProfile)
			}
		})
	}
}

func TestResolverGitWorktreeEquivalentMatching(t *testing.T) {
	t.Parallel()

	repo := filepath.Join(string(filepath.Separator), "repos", "main")
	linked := filepath.Join(string(filepath.Separator), "worktrees", "feature")
	api := filepath.Join(repo, "services", "api")
	payments := filepath.Join(api, "payments")
	provider := fakeGitMetadataProvider{metadata: map[string]gitMetadata{
		repo:     fakeGitMetadata(repo, repo, linked),
		api:      fakeGitMetadata(repo, repo, linked),
		payments: fakeGitMetadata(repo, repo, linked),
	}}

	tests := []struct {
		name        string
		projects    []config.Session
		cwd         string
		wantTitle   string
		wantProfile string
	}{
		{
			name:        "deep cwd inside linked worktree",
			projects:    []config.Session{{Title: "Repo", Directory: repo, Profile: "repo-profile"}},
			cwd:         filepath.Join(linked, "pkg", "sub"),
			wantTitle:   "Repo",
			wantProfile: "repo-profile",
		},
		{
			name: "longest relative subproject wins",
			projects: []config.Session{
				{Title: "Repo", Directory: repo, Profile: "repo-profile"},
				{Title: "API", Directory: api, Profile: "api-profile"},
				{Title: "Payments", Directory: payments, Profile: "payments-profile"},
			},
			cwd:         filepath.Join(linked, "services", "api", "payments", "internal"),
			wantTitle:   "Payments",
			wantProfile: "payments-profile",
		},
		{
			name:      "unrelated repo non-match",
			projects:  []config.Session{{Title: "API", Directory: api, Profile: "api-profile"}},
			cwd:       filepath.Join(string(filepath.Separator), "other", "repo", "services", "api", "cmd"),
			wantTitle: "cmd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := newWithGitProvider(tt.projects, provider)
			got := resolver.ResolveOrFallback(tt.cwd)
			if got.Title != tt.wantTitle || got.Profile != tt.wantProfile {
				t.Errorf("ResolveOrFallback(%q) = %#v, want %s/%s", tt.cwd, got, tt.wantTitle, tt.wantProfile)
			}
		})
	}
}

func TestResolverProbesGitMetadataConcurrentlyAndPreservesOrder(t *testing.T) {
	t.Parallel()

	rootA := filepath.Join(string(filepath.Separator), "repos", "a")
	rootB := filepath.Join(string(filepath.Separator), "repos", "b")
	dirA := filepath.Join(rootA, "service")
	dirB := filepath.Join(rootB, "service")
	linked := filepath.Join(string(filepath.Separator), "worktrees", "linked")
	release := make(chan struct{})
	provider := blockingGitMetadataProvider{
		metadata: map[string]gitMetadata{
			dirA: fakeGitMetadata(rootA, linked),
			dirB: fakeGitMetadata(rootB, linked),
		},
		blockDir: dirA,
		release:  release,
		started:  make(chan string, 2),
	}

	done := make(chan Resolver, 1)
	go func() {
		done <- newWithGitProvider([]config.Session{
			{Title: "First", Directory: dirA, Profile: "first-profile"},
			{Title: "Second", Directory: dirB, Profile: "second-profile"},
		}, provider)
	}()

	// The probe for dirA blocks until release, so dirB can only start while
	// dirA is still in flight if the probes run concurrently.
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
	var resolver Resolver
	select {
	case resolver = <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resolver construction")
	}

	if got, want := resolver.projects[0].title, "First"; got != want {
		t.Fatalf("first resolver project = %q, want %q", got, want)
	}
	cwd := filepath.Join(linked, "service", "cmd")
	got, ok := resolver.Resolve(cwd)
	if !ok || got.Title != "First" || got.Profile != "first-profile" {
		t.Errorf("Resolve(%q) = %#v, %v; want First match by configured order", cwd, got, ok)
	}
}

func TestResolverExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := filepath.Join(home, "workspace", "api")
	if err := os.MkdirAll(filepath.Join(projectDir, "pkg"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	resolver := New([]config.Session{{Title: "API", Directory: "~/workspace/api"}})
	if got, ok := resolver.Resolve(filepath.Join(projectDir, "pkg")); !ok || got.Title != "API" {
		t.Errorf("tilde project match = %#v, %v; want title API", got, ok)
	}
}

func TestResolverMatchesSymlinkedSubdirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realDir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	resolver := New([]config.Session{{Title: "API", Directory: linkDir}})
	if got, ok := resolver.Resolve(filepath.Join(realDir, "sub")); !ok || got.Title != "API" {
		t.Errorf("symlinked subdir match = %#v, %v; want title API", got, ok)
	}
}

func TestResolverMatchesSymlinkEquivalentPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	resolver := New([]config.Session{{Title: "API", Directory: linkDir}})
	if got, ok := resolver.Resolve(realDir); !ok || got.Title != "API" {
		t.Errorf("symlink-equivalent match = %#v, %v; want title API", got, ok)
	}
}

func TestAbsPathExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The leaf does not exist, so no symlink in the temp dir is evaluated.
	want := filepath.Join(home, "projectresolve-test")
	if got := AbsPath("~/projectresolve-test"); got != want {
		t.Errorf("AbsPath(~/...) = %q, want %q", got, want)
	}
}

func TestAbsPathDoesNotExpandNamedTilde(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	input := "~definitely-not-current-user-agentmux/projectresolve-test"
	want := filepath.Clean(filepath.Join(cwd, input))
	if got := AbsPath(input); got != want {
		t.Errorf("named tilde path = %q, want %q", got, want)
	}
}
