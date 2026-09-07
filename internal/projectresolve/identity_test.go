package projectresolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// classifyingGitProvider answers with metadata, with the positive "not a
// repository" refusal, or with an ordinary probe failure, so the three outcomes
// authorization has to keep apart can be posed without a filesystem.
type classifyingGitProvider struct {
	metadata     map[string]gitMetadata
	notARepo     map[string]bool
	failure      error
	failureDirs  map[string]bool
	defaultFails bool
}

func (p classifyingGitProvider) GitMetadata(dir string) (gitMetadata, error) {
	if p.notARepo[dir] {
		return gitMetadata{}, fmt.Errorf("git rev-parse: %w: fatal: not a git repository", errNotARepository)
	}
	if p.failureDirs[dir] || (p.defaultFails && p.failure != nil) {
		return gitMetadata{}, p.failure
	}
	if meta, ok := p.metadata[dir]; ok {
		return meta, nil
	}
	return gitMetadata{}, errors.New("git metadata not found")
}

func TestResolveIdentityRejectsUnusablePaths(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing target"), dangling); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	resolver := newIdentityResolver(classifyingGitProvider{})
	for name, path := range map[string]string{
		"empty":            "",
		"missing":          filepath.Join(root, "absent"),
		"file":             file,
		"dangling symlink": dangling,
	} {
		identity, err := resolver.ResolveIdentity(path)
		if !errors.Is(err, ErrPathUnresolved) {
			t.Errorf("ResolveIdentity(%s) = %#v, %v; want ErrPathUnresolved", name, identity, err)
		}
	}
}

func TestResolveIdentityNonRepositoryIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	real := AbsPath(dir)
	resolver := newIdentityResolver(classifyingGitProvider{notARepo: map[string]bool{real: true}})

	identity, err := resolver.ResolveIdentity(dir)
	if err != nil {
		t.Fatalf("ResolveIdentity(plain directory): %v", err)
	}
	if identity.IsRepository || identity.CommonDir != "" {
		t.Fatalf("identity = %#v, want a resolved non-repository", identity)
	}
	if identity.Path != real {
		t.Fatalf("Path = %q, want %q", identity.Path, real)
	}
}

func TestResolveIdentityFailedProbeIsUnresolvedNotAbsent(t *testing.T) {
	dir := t.TempDir()
	// A probe that merely failed must never read as "plain directory": the
	// directory could be a linked worktree of an excluded repository, and the
	// difference is exactly what an exclusion depends on.
	resolver := newIdentityResolver(classifyingGitProvider{
		failure:      errors.New("git rev-parse timed out after 2s"),
		defaultFails: true,
	})

	identity, err := resolver.ResolveIdentity(dir)
	if !errors.Is(err, ErrIdentityUnresolved) {
		t.Fatalf("ResolveIdentity(failed probe) = %#v, %v; want ErrIdentityUnresolved", identity, err)
	}
	if errors.Is(err, ErrPathUnresolved) {
		t.Error("a failed Git probe was reported as an unresolved path")
	}
}

func TestResolveIdentityIncompleteRepositoryIsUnresolved(t *testing.T) {
	dir := t.TempDir()
	real := AbsPath(dir)
	for name, meta := range map[string]gitMetadata{
		"no common dir": {worktreeRoot: real, worktreeRoots: []string{real}},
		"no checkout":   {commonDir: filepath.Join(real, ".git")},
		"foreign checkout": {
			worktreeRoot:  filepath.Join(real, "elsewhere"),
			worktreeRoots: []string{filepath.Join(real, "elsewhere")},
			commonDir:     filepath.Join(real, ".git"),
		},
	} {
		resolver := newIdentityResolver(classifyingGitProvider{metadata: map[string]gitMetadata{real: meta}})
		if identity, err := resolver.ResolveIdentity(dir); !errors.Is(err, ErrIdentityUnresolved) {
			t.Errorf("ResolveIdentity(%s) = %#v, %v; want ErrIdentityUnresolved", name, identity, err)
		}
	}
}

func TestResolveIdentityReportsCheckoutPosition(t *testing.T) {
	root := t.TempDir()
	repo := AbsPath(root)
	api := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	linked := filepath.Join(repo, "linked")
	common := filepath.Join(repo, ".git")
	resolver := newIdentityResolver(classifyingGitProvider{metadata: map[string]gitMetadata{
		api: {worktreeRoot: repo, worktreeRoots: []string{repo, linked}, commonDir: common},
	}})

	identity, err := resolver.ResolveIdentity(api)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if identity.RelativeDir != filepath.Join("services", "api") {
		t.Errorf("RelativeDir = %q, want services/api", identity.RelativeDir)
	}
	if identity.CommonDir != common || identity.WorktreeRoot != repo {
		t.Errorf("identity = %#v, want common %q and root %q", identity, common, repo)
	}
	if !containsAbsPath(identity.WorktreeRoots, linked) {
		t.Errorf("WorktreeRoots = %#v, want the linked worktree", identity.WorktreeRoots)
	}
}

func TestIsNotARepositoryStderrOnlyMatchesTheAbsenceOfARepository(t *testing.T) {
	for _, text := range []string{
		"fatal: not a git repository (or any of the parent directories): .git",
		"fatal: not a git repository: '/repos/app/.git'",
		"fatal: '/repos/app' is not a working tree",
	} {
		if !isNotARepositoryStderr(text) {
			t.Errorf("stderr %q was not read as an absent repository", text)
		}
	}
	// Everything else stays an unresolved identity. Each of these can be
	// reported from *inside* a repository, so reading any of them as "no
	// repository here" would drop an applicable exclusion.
	for _, text := range []string{
		"",
		"fatal: detected dubious ownership in repository at '/repos/app'",
		"fatal: could not read Username for 'https://example.invalid'",
		"fatal: '/worktrees/gone' does not appear to be a git repository",
		"error: could not lock config file .git/config: Permission denied",
	} {
		if isNotARepositoryStderr(text) {
			t.Errorf("stderr %q was read as an absent repository", text)
		}
	}
}

func TestResolveIdentityAgainstRealGitRepository(t *testing.T) {
	git := requireGit(t)
	root := t.TempDir()
	requireNoEnclosingRepository(t, root)

	repo := filepath.Join(root, "repo with space")
	linked := filepath.Join(root, "linked worktree")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	runGit(t, git, repo, "init")
	runGit(t, git, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	runGit(t, git, repo, "worktree", "add", "-b", "feature/identity", linked)
	api := filepath.Join(linked, "services", "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	symlink := filepath.Join(root, "app link")
	if err := os.Symlink(repo, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	resolver := NewIdentityResolver()
	viaRepo, repoErr := resolver.ResolveIdentity(repo)
	viaSymlink, symlinkErr := resolver.ResolveIdentity(symlink)
	viaLinked, linkedErr := resolver.ResolveIdentity(api)
	if repoErr != nil || symlinkErr != nil || linkedErr != nil {
		t.Fatalf("ResolveIdentity errors: repo=%v symlink=%v linked=%v", repoErr, symlinkErr, linkedErr)
	}

	if viaSymlink.Path != viaRepo.Path {
		t.Errorf("symlinked path = %q, want the real checkout %q", viaSymlink.Path, viaRepo.Path)
	}
	if viaLinked.CommonDir != viaRepo.CommonDir {
		t.Errorf("linked worktree common dir = %q, want %q", viaLinked.CommonDir, viaRepo.CommonDir)
	}
	if !viaLinked.IsRepository || !viaRepo.IsRepository {
		t.Error("real checkouts did not report IsRepository")
	}
	if viaRepo.RelativeDir != "." {
		t.Errorf("checkout root RelativeDir = %q, want .", viaRepo.RelativeDir)
	}
	if viaLinked.RelativeDir != filepath.Join("services", "api") {
		t.Errorf("linked worktree RelativeDir = %q, want services/api", viaLinked.RelativeDir)
	}
	if !containsAbsPath(viaLinked.WorktreeRoots, repo) || !containsAbsPath(viaLinked.WorktreeRoots, linked) {
		t.Errorf("WorktreeRoots = %#v, want both checkouts", viaLinked.WorktreeRoots)
	}
}

func TestResolveIdentityAgainstRealNonRepository(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	requireNoEnclosingRepository(t, dir)

	identity, err := NewIdentityResolver().ResolveIdentity(dir)
	if err != nil {
		t.Fatalf("ResolveIdentity(real plain directory): %v", err)
	}
	if identity.IsRepository {
		t.Fatalf("identity = %#v, want a non-repository", identity)
	}
}

func TestResolveIdentityRealProbeFailureStaysUnresolved(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	// An expired deadline is the deterministic form of a real probe failure.
	// It must not degrade into "not a repository".
	resolver := newIdentityResolver(realGitMetadataProvider{timeout: time.Nanosecond})
	if identity, err := resolver.ResolveIdentity(repo); !errors.Is(err, ErrIdentityUnresolved) {
		t.Fatalf("ResolveIdentity(expired probe) = %#v, %v; want ErrIdentityUnresolved", identity, err)
	}
}

// requireNoEnclosingRepository keeps the "plain directory" assertions honest on
// a machine whose temporary directory happens to live inside a checkout.
func requireNoEnclosingRepository(t *testing.T, dir string) {
	t.Helper()
	for current := AbsPath(dir); ; {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			t.Skipf("temporary directory %s is inside a git repository", dir)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return
		}
		current = parent
	}
}
