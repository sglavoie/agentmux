package projectresolve

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func fakeGitFamilyMetadata(commonDir, root string, worktreeRoots ...string) gitMetadata {
	return gitMetadata{
		worktreeRoot:  root,
		worktreeRoots: worktreeRoots,
		commonDir:     commonDir,
	}
}

func abs(parts ...string) string {
	return filepath.Join(append([]string{string(filepath.Separator)}, parts...)...)
}

func TestResolveFamilyGroupsMainCheckoutAndLinkedWorktree(t *testing.T) {
	t.Parallel()

	repo := abs("repos", "app")
	linked := abs("worktrees", "app-security")
	common := filepath.Join(repo, ".git")
	resolver := newWithGitProvider(
		[]config.Session{{Title: "App", Directory: repo, Profile: "work"}},
		fakeGitMetadataProvider{metadata: map[string]gitMetadata{
			repo: fakeGitFamilyMetadata(common, repo, repo, linked),
		}},
	)

	main, ok := resolver.ResolveFamily(repo)
	if !ok {
		t.Fatal("ResolveFamily(main checkout) reported no family")
	}
	inLinked, ok := resolver.ResolveFamily(filepath.Join(linked, "internal", "pkg"))
	if !ok {
		t.Fatal("ResolveFamily(linked worktree) reported no family")
	}

	if main.Key != gitFamilyKeyPrefix+common {
		t.Errorf("main key = %q, want %q", main.Key, gitFamilyKeyPrefix+common)
	}
	if inLinked.Key != main.Key {
		t.Errorf("linked worktree key = %q, want the main checkout key %q", inLinked.Key, main.Key)
	}
	for name, family := range map[string]Family{"main": main, "linked": inLinked} {
		if family.Label != "App" {
			t.Errorf("%s label = %q, want App", name, family.Label)
		}
		if !family.IsRepository {
			t.Errorf("%s IsRepository = false, want true", name)
		}
		if family.PrimaryRoot != repo {
			t.Errorf("%s PrimaryRoot = %q, want %q", name, family.PrimaryRoot, repo)
		}
	}
}

func TestResolveFamilySeparatelyConfiguredWorktreeSharesOneKey(t *testing.T) {
	t.Parallel()

	repo := abs("repos", "app")
	linked := abs("worktrees", "app-security")
	common := filepath.Join(repo, ".git")
	resolver := newWithGitProvider(
		[]config.Session{
			{Title: "App", Directory: repo, Profile: "work"},
			{Title: "App Security", Directory: linked, Profile: "work"},
		},
		fakeGitMetadataProvider{metadata: map[string]gitMetadata{
			repo:   fakeGitFamilyMetadata(common, repo, repo, linked),
			linked: fakeGitFamilyMetadata(common, linked, repo, linked),
		}},
	)

	main, mainOK := resolver.ResolveFamily(repo)
	worktree, worktreeOK := resolver.ResolveFamily(linked)
	if !mainOK || !worktreeOK {
		t.Fatalf("ResolveFamily reported no family: main=%v worktree=%v", mainOK, worktreeOK)
	}
	if main.Key != worktree.Key {
		t.Fatalf("separately configured worktree key = %q, want %q", worktree.Key, main.Key)
	}
	// The label is the project rooted at the primary checkout, so the family
	// does not rename itself depending on which checkout was looked up.
	if main.Label != "App" || worktree.Label != "App" {
		t.Fatalf("labels = %q/%q, want App/App", main.Label, worktree.Label)
	}
	if worktree.PrimaryRoot != repo {
		t.Fatalf("worktree PrimaryRoot = %q, want the main checkout %q", worktree.PrimaryRoot, repo)
	}
}

func TestResolveFamilyMonorepoSubprojectsShareRepositoryFamily(t *testing.T) {
	t.Parallel()

	mono := abs("repos", "mono")
	common := filepath.Join(mono, ".git")
	api := filepath.Join(mono, "services", "api")
	web := filepath.Join(mono, "services", "web")
	resolver := newWithGitProvider(
		[]config.Session{
			{Title: "API", Directory: api, Profile: "work"},
			{Title: "Web", Directory: web, Profile: "work"},
		},
		fakeGitMetadataProvider{metadata: map[string]gitMetadata{
			api: fakeGitFamilyMetadata(common, mono, mono),
			web: fakeGitFamilyMetadata(common, mono, mono),
		}},
	)

	apiFamily, apiOK := resolver.ResolveFamily(filepath.Join(api, "cmd"))
	webFamily, webOK := resolver.ResolveFamily(web)
	if !apiOK || !webOK {
		t.Fatalf("ResolveFamily reported no family: api=%v web=%v", apiOK, webOK)
	}
	if apiFamily.Key != webFamily.Key {
		t.Fatalf("monorepo subproject keys differ: %q vs %q", apiFamily.Key, webFamily.Key)
	}
	// No configured project is rooted at the repository itself, so the family
	// falls back to the checkout's directory name rather than borrowing one
	// subproject's title for the whole repository.
	if apiFamily.Label != "mono" {
		t.Fatalf("monorepo label = %q, want mono", apiFamily.Label)
	}
	if apiFamily.PrimaryRoot != mono {
		t.Fatalf("monorepo PrimaryRoot = %q, want %q", apiFamily.PrimaryRoot, mono)
	}
}

func TestResolveFamilyNestedRepositoriesStaySeparate(t *testing.T) {
	t.Parallel()

	outer := abs("repos", "outer")
	inner := filepath.Join(outer, "vendor", "inner")
	resolver := newWithGitProvider(
		[]config.Session{
			{Title: "Outer", Directory: outer, Profile: "work"},
			{Title: "Inner", Directory: inner, Profile: "work"},
		},
		fakeGitMetadataProvider{metadata: map[string]gitMetadata{
			outer: fakeGitFamilyMetadata(filepath.Join(outer, ".git"), outer, outer),
			inner: fakeGitFamilyMetadata(filepath.Join(inner, ".git"), inner, inner),
		}},
	)

	outerFamily, outerOK := resolver.ResolveFamily(filepath.Join(outer, "cmd"))
	innerFamily, innerOK := resolver.ResolveFamily(filepath.Join(inner, "pkg"))
	if !outerOK || !innerOK {
		t.Fatalf("ResolveFamily reported no family: outer=%v inner=%v", outerOK, innerOK)
	}
	if outerFamily.Key == innerFamily.Key {
		t.Fatalf("nested repository shares the outer key %q", innerFamily.Key)
	}
	if innerFamily.Label != "Inner" || outerFamily.Label != "Outer" {
		t.Fatalf("labels = %q/%q, want Outer/Inner", outerFamily.Label, innerFamily.Label)
	}
}

func TestResolveFamilyDeduplicatesConfigEntriesSharingOneCommonDir(t *testing.T) {
	t.Parallel()

	repo := abs("repos", "app")
	common := filepath.Join(repo, ".git")
	resolver := newWithGitProvider(
		[]config.Session{
			{Title: "App (claude)", Directory: repo, Profile: "claude"},
			{Title: "App (codex)", Directory: repo, Profile: "codex"},
		},
		fakeGitMetadataProvider{metadata: map[string]gitMetadata{
			repo: fakeGitFamilyMetadata(common, repo, repo),
		}},
	)

	// The two entries tie as project matches, which ghost recovery treats as
	// ambiguous. They are still one repository, so the family resolves.
	if got := len(resolver.Matches(repo)); got != 2 {
		t.Fatalf("Matches returned %d matches, want the ambiguous pair", got)
	}
	family, ok := resolver.ResolveFamily(repo)
	if !ok {
		t.Fatal("duplicate config entries for one repository reported no family")
	}
	if family.Key != gitFamilyKeyPrefix+common {
		t.Errorf("key = %q, want %q", family.Key, gitFamilyKeyPrefix+common)
	}
	// Two configured projects are rooted at the checkout, so neither title can
	// name the family without picking a winner arbitrarily.
	if family.Label != "app" {
		t.Errorf("label = %q, want the checkout directory name app", family.Label)
	}
}

func TestResolveFamilyAmbiguousNonGitProjectsStayUngrouped(t *testing.T) {
	t.Parallel()

	dir := abs("notes")
	resolver := newWithGitProvider(
		[]config.Session{
			{Title: "Notes", Directory: dir, Profile: "claude"},
			{Title: "Journal", Directory: dir, Profile: "codex"},
		},
		fakeGitMetadataProvider{err: errors.New("not a repository")},
	)

	if family, ok := resolver.ResolveFamily(dir); ok {
		t.Fatalf("ambiguous non-Git directory resolved to %#v, want no family", family)
	}
}

func TestResolveFamilyNonGitProjectUsesConfiguredIdentity(t *testing.T) {
	t.Parallel()

	dir := abs("notes")
	resolver := newWithGitProvider(
		[]config.Session{{Title: "Notes", Directory: dir, Profile: "claude"}},
		fakeGitMetadataProvider{err: errors.New("not a repository")},
	)

	family, ok := resolver.ResolveFamily(filepath.Join(dir, "inbox"))
	if !ok {
		t.Fatal("unique non-Git project reported no family")
	}
	if !strings.HasPrefix(family.Key, projectFamilyKeyPrefix) {
		t.Errorf("key = %q, want a %q key", family.Key, projectFamilyKeyPrefix)
	}
	if family.IsRepository {
		t.Error("IsRepository = true for a non-Git project")
	}
	if family.Label != "Notes" {
		t.Errorf("label = %q, want Notes", family.Label)
	}
	if family.PrimaryRoot != dir {
		t.Errorf("PrimaryRoot = %q, want the configured directory %q", family.PrimaryRoot, dir)
	}
}

func TestResolveFamilyUntitledProjectFallsBackToDirectoryName(t *testing.T) {
	t.Parallel()

	dir := abs("notes", "scratch")
	resolver := newWithGitProvider(
		[]config.Session{{Directory: dir, Profile: "claude"}},
		fakeGitMetadataProvider{err: errors.New("not a repository")},
	)

	family, ok := resolver.ResolveFamily(dir)
	if !ok || family.Label != "scratch" {
		t.Fatalf("ResolveFamily = %#v, %v; want the scratch directory name", family, ok)
	}
}

func TestResolveFamilyWithoutMatchOrPathReportsNoFamily(t *testing.T) {
	t.Parallel()

	resolver := newWithGitProvider(
		[]config.Session{{Title: "App", Directory: abs("repos", "app"), Profile: "work"}},
		fakeGitMetadataProvider{metadata: map[string]gitMetadata{
			abs("repos", "app"): fakeGitFamilyMetadata(abs("repos", "app", ".git"), abs("repos", "app"), abs("repos", "app")),
		}},
	)

	if family, ok := resolver.ResolveFamily(abs("elsewhere")); ok {
		t.Errorf("unmatched directory resolved to %#v, want no family", family)
	}
	if family, ok := resolver.ResolveFamily(""); ok {
		t.Errorf("empty directory resolved to %#v, want no family", family)
	}
}

func TestResolveFamilyWithoutGitEvidenceIsNotARepository(t *testing.T) {
	t.Parallel()

	repo := abs("repos", "app")
	// The provider answered, but with no common directory — a partial probe is
	// not proof of a repository, so the project keys on its own identity.
	resolver := newWithGitProvider(
		[]config.Session{{Title: "App", Directory: repo, Profile: "work"}},
		fakeGitMetadataProvider{metadata: map[string]gitMetadata{
			repo: fakeGitMetadata(repo, repo),
		}},
	)

	family, ok := resolver.ResolveFamily(repo)
	if !ok {
		t.Fatal("project without common directory reported no family")
	}
	if family.IsRepository || strings.HasPrefix(family.Key, gitFamilyKeyPrefix) {
		t.Fatalf("family = %#v, want a non-repository family", family)
	}
}

func TestFamilyLabelNeverCarriesAnInternalKey(t *testing.T) {
	t.Parallel()

	repo := abs("repos", "app")
	dir := abs("notes")
	resolver := newWithGitProvider(
		[]config.Session{
			{Title: "App", Directory: repo, Profile: "work"},
			{Title: "Notes", Directory: dir, Profile: "claude"},
		},
		fakeGitMetadataProvider{metadata: map[string]gitMetadata{
			repo: fakeGitFamilyMetadata(filepath.Join(repo, ".git"), repo, repo),
		}},
	)

	for _, cwd := range []string{repo, dir} {
		family, ok := resolver.ResolveFamily(cwd)
		if !ok {
			t.Fatalf("ResolveFamily(%q) reported no family", cwd)
		}
		if family.Label == family.Key {
			t.Errorf("ResolveFamily(%q) label is the internal key %q", cwd, family.Key)
		}
		if strings.ContainsRune(family.Label, filepath.Separator) {
			t.Errorf("ResolveFamily(%q) label %q leaked a path", cwd, family.Label)
		}
		if strings.ContainsAny(family.Label, familyKeyFieldSeparator) {
			t.Errorf("ResolveFamily(%q) label %q leaked a key field separator", cwd, family.Label)
		}
	}
}

func TestPrimaryWorktreeRootPrefersTheMainWorktree(t *testing.T) {
	t.Parallel()

	main := abs("repos", "app")
	linked := abs("worktrees", "app-security")
	if got := primaryWorktreeRoot([]string{main, linked}, linked); got != main {
		t.Errorf("primaryWorktreeRoot = %q, want the main worktree %q", got, main)
	}
	if got := primaryWorktreeRoot(nil, linked); got != linked {
		t.Errorf("primaryWorktreeRoot(nil) = %q, want the probed checkout %q", got, linked)
	}
	if got := primaryWorktreeRoot([]string{""}, linked); got != linked {
		t.Errorf("primaryWorktreeRoot(empty entry) = %q, want the probed checkout %q", got, linked)
	}
}

func TestParseGitRevParseRoots(t *testing.T) {
	t.Parallel()

	root, common, err := parseGitRevParseRoots([]byte("/repos/app\n/repos/app/.git\n"))
	if err != nil {
		t.Fatalf("parseGitRevParseRoots: %v", err)
	}
	if root != filepath.Clean("/repos/app") || common != filepath.Clean("/repos/app/.git") {
		t.Fatalf("parseGitRevParseRoots = %q, %q", root, common)
	}

	for name, out := range map[string]string{
		"only toplevel":  "/repos/app\n",
		"extra line":     "/repos/app\n/repos/app/.git\n/repos/app\n",
		"empty common":   "/repos/app\n\n",
		"empty output":   "",
		"blank line":     "\n",
		"empty toplevel": "\n/repos/app/.git\n",
	} {
		if _, _, err := parseGitRevParseRoots([]byte(out)); err == nil {
			t.Errorf("parseGitRevParseRoots(%s) accepted malformed output", name)
		}
	}
}

func TestGitMetadataProviderTimeoutDegradesToNoFamily(t *testing.T) {
	t.Parallel()

	requireGit(t)
	repo := t.TempDir()

	// An already-expired deadline is the deterministic form of the real
	// failure: the probe must degrade to no Git metadata, never block discovery.
	provider := realGitMetadataProvider{timeout: time.Nanosecond}
	if _, err := provider.GitMetadata(repo); err == nil {
		t.Fatal("GitMetadata with an expired timeout returned no error")
	}

	resolver := newWithGitProvider(
		[]config.Session{{Title: "App", Directory: repo, Profile: "work"}},
		provider,
	)
	family, ok := resolver.ResolveFamily(repo)
	if !ok {
		t.Fatal("timed-out probe lost the configured project entirely")
	}
	if family.IsRepository {
		t.Fatalf("family = %#v, want a non-repository family after a timeout", family)
	}
}

func TestResolveFamilyAcrossRealWorktreesWithSpacesAndSymlinks(t *testing.T) {
	t.Parallel()

	git := requireGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo with space")
	linked := filepath.Join(root, "linked worktree")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	runGit(t, git, repo, "init")
	runGit(t, git, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "--no-verify", "-m", "initial")
	runGit(t, git, repo, "worktree", "add", "-b", "feature/test", linked)

	symlink := filepath.Join(root, "app link")
	if err := os.Symlink(repo, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The project is configured through the symlink; the sessions below are
	// found at the real checkout and at the linked worktree.
	resolver := NewWithGit([]config.Session{{Title: "App", Directory: symlink, Profile: "work"}})

	viaSymlink, symlinkOK := resolver.ResolveFamily(symlink)
	viaRepo, repoOK := resolver.ResolveFamily(repo)
	viaLinked, linkedOK := resolver.ResolveFamily(linked)
	if !symlinkOK || !repoOK || !linkedOK {
		t.Fatalf("ResolveFamily reported no family: symlink=%v repo=%v linked=%v", symlinkOK, repoOK, linkedOK)
	}
	if viaSymlink.Key != viaRepo.Key {
		t.Errorf("symlinked checkout key = %q, want %q", viaSymlink.Key, viaRepo.Key)
	}
	if viaLinked.Key != viaRepo.Key {
		t.Errorf("linked worktree key = %q, want %q", viaLinked.Key, viaRepo.Key)
	}
	wantCommon := gitFamilyKeyPrefix + AbsPath(filepath.Join(repo, ".git"))
	if viaRepo.Key != wantCommon {
		t.Errorf("key = %q, want %q", viaRepo.Key, wantCommon)
	}
	if !viaLinked.IsRepository {
		t.Error("IsRepository = false for a real linked worktree")
	}
	if viaLinked.PrimaryRoot != AbsPath(repo) {
		t.Errorf("PrimaryRoot = %q, want the main checkout %q", viaLinked.PrimaryRoot, AbsPath(repo))
	}
	if viaLinked.Label != "App" {
		t.Errorf("label = %q, want App", viaLinked.Label)
	}
}
