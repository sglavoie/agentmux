package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInventoryValidDirtyLockedAndMergeFacts(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	workflow := testWorkflow(root)
	result, err := workflow.Create(context.Background(), Request{
		ProjectLabel: "api", SourceCWD: repo, SessionID: "source-1", Profile: "work", Harness: "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workflow.RemoveUnused(context.Background(), result) })

	inventory := testInventory(root)
	before := treeSnapshot(t, root)
	entries, err := inventory.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want one", entries)
	}
	entry := entries[0]
	if entry.ID != result.ID || entry.Classification != ClassificationValid || !entry.Exists || !entry.GitAvailable ||
		!entry.StatusAvailable || !entry.Registered || entry.Dirty || entry.Detached || !entry.Actionable {
		t.Fatalf("entry = %+v, want clean actionable sidecar-backed checkout", entry)
	}
	if entry.Ahead == nil || *entry.Ahead != 0 || entry.Behind == nil || *entry.Behind != 0 ||
		entry.ReachableFromSource == nil || !*entry.ReachableFromSource {
		t.Fatalf("merge facts = ahead %v behind %v reachable %v", entry.Ahead, entry.Behind, entry.ReachableFromSource)
	}
	after := treeSnapshot(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("managed tree changed during discovery:\nbefore=%v\nafter=%v", before, after)
	}

	if err := os.WriteFile(filepath.Join(result.Path, "committed.txt"), []byte("commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, result.Path, "add", "committed.txt")
	gitRun(t, result.Path, "commit", "-m", "worktree commit")
	entries, err = inventory.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry = entries[0]
	if entry.Ahead == nil || *entry.Ahead != 1 || entry.Behind == nil || *entry.Behind != 0 ||
		entry.ReachableFromSource == nil || *entry.ReachableFromSource {
		t.Fatalf("diverged merge facts = ahead %v behind %v reachable %v", entry.Ahead, entry.Behind, entry.ReachableFromSource)
	}
	gitRun(t, result.Path, "checkout", "--detach")

	if err := os.WriteFile(filepath.Join(result.Path, "untracked.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "lock", "--reason", "inventory test", result.Path)
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(result.Path, "untracked.txt"))
		_ = execGit(repo, "worktree", "unlock", result.Path)
	})
	entries, err = inventory.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry = entries[0]
	if !entry.Dirty || !entry.Locked || entry.LockReason != "inventory test" || !entry.Detached || entry.Branch != "" {
		t.Fatalf("entry = %+v, want dirty, locked, and detached", entry)
	}
}

func TestInventoryMissingSourceRefLeavesNullableMergeFacts(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	workflow := testWorkflow(root)
	result, err := workflow.Create(context.Background(), Request{ProjectLabel: "api", SourceCWD: repo, SessionID: "source-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workflow.RemoveUnused(context.Background(), result) })
	metadata, err := ReadMetadata(result.MetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata.SourceBranch = "deleted-source"
	if err := WriteMetadata(result.MetadataPath, metadata); err != nil {
		t.Fatal(err)
	}

	entries, err := testInventory(root).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Ahead != nil || entries[0].Behind != nil || entries[0].ReachableFromSource != nil ||
		!strings.Contains(strings.Join(entries[0].Warnings, "\n"), "source branch \"deleted-source\" is unavailable") {
		t.Fatalf("entry = %+v, want nullable merge facts and missing-ref warning", entries)
	}
}

func TestInventoryMissingRootIsEmptyAndDoesNotCreateIt(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "absent")
	entries, err := testInventory(root).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want empty", entries)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root stat = %v, want not created", err)
	}
}

func TestInventoryLegacyStableIDAndExternalWorktreeExcluded(t *testing.T) {
	t.Parallel()

	repo := testRepository(t)
	root := filepath.Join(t.TempDir(), "worktrees")
	managed := filepath.Join(root, "api", "legacy")
	if err := os.MkdirAll(filepath.Dir(managed), 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "worktree", "add", "-b", "amx/api/legacy", managed, "HEAD")
	t.Cleanup(func() {
		_ = execGit(repo, "worktree", "remove", "--force", managed)
		_ = execGit(repo, "branch", "-D", "amx/api/legacy")
	})
	external := filepath.Join(t.TempDir(), "external")
	gitRun(t, repo, "worktree", "add", "-b", "external-branch", external, "HEAD")
	t.Cleanup(func() {
		_ = execGit(repo, "worktree", "remove", "--force", external)
		_ = execGit(repo, "branch", "-D", "external-branch")
	})
	unowned := filepath.Join(root, "api", "unowned")
	gitRun(t, repo, "worktree", "add", "-b", "external-inside-root", unowned, "HEAD")
	t.Cleanup(func() {
		_ = execGit(repo, "worktree", "remove", "--force", unowned)
		_ = execGit(repo, "branch", "-D", "external-inside-root")
	})

	inventory := testInventory(root)
	first, err := inventory.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := inventory.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantManaged, err := canonicalDirectory(managed)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].Classification != ClassificationLegacy || first[0].ID != second[0].ID ||
		!strings.HasPrefix(first[0].ID, "legacy-") || first[0].Path != wantManaged {
		t.Fatalf("first=%+v second=%+v, want one stable managed legacy entry", first, second)
	}
	if strings.Contains(fmt.Sprint(first), external) {
		t.Fatalf("external worktree leaked into inventory: %+v", first)
	}
}

func TestInventoryClassifiesRetainedAndInvalidMetadata(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "worktrees")
	project := filepath.Join(root, "api")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(t.TempDir(), "common")
	if err := os.MkdirAll(common, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}

	missingPath := filepath.Join(project, "missing")
	missing := metadataForInventory(missingPath, common, source, "missing-id")
	if err := WriteMetadata(SidecarPath(missingPath), missing); err != nil {
		t.Fatal(err)
	}
	malformedPath := filepath.Join(project, "malformed")
	if err := os.WriteFile(SidecarPath(malformedPath), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsupportedPath := filepath.Join(project, "unsupported")
	if err := os.WriteFile(SidecarPath(unsupportedPath), []byte(`{"schema_version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mismatchPath := filepath.Join(project, "mismatch")
	mismatch := metadataForInventory(filepath.Join(project, "somewhere-else"), common, source, "mismatch-id")
	if err := WriteMetadata(SidecarPath(mismatchPath), mismatch); err != nil {
		t.Fatal(err)
	}
	regularPath := filepath.Join(project, "regular")
	if err := os.WriteFile(regularPath, []byte("not a checkout\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(SidecarPath(regularPath), metadataForInventory(regularPath, common, source, "regular-id")); err != nil {
		t.Fatal(err)
	}

	entries, err := testInventory(root).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]Classification)
	for _, entry := range entries {
		got[filepath.Base(entry.Path)] = entry.Classification
		if entry.Actionable {
			t.Fatalf("invalid retained entry became actionable: %+v", entry)
		}
	}
	want := map[string]Classification{
		"missing": ClassificationMissing, "malformed": ClassificationMetadataInvalid,
		"unsupported": ClassificationMetadataInvalid, "mismatch": ClassificationMetadataInvalid, "regular": ClassificationMetadataInvalid,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("classifications = %v, want %v", got, want)
	}
}

func TestInventoryRejectsSymlinkEscapeAndPerformsNoManagedRootWrites(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "worktrees")
	project := filepath.Join(root, "api")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	external := testRepository(t)
	if err := os.Symlink(external, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, root)
	entries, err := testInventory(root).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after := treeSnapshot(t, root)
	if len(entries) != 0 {
		t.Fatalf("symlink escape was enumerated: %+v", entries)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("managed tree changed during discovery:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestInventoryPartialGitFailureAndBoundedConcurrency(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "worktrees")
	project := filepath.Join(root, "api")
	common := filepath.Join(t.TempDir(), "common")
	source := filepath.Join(t.TempDir(), "source")
	for _, directory := range []string{project, common, source} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const count = 8
	for index := range count {
		path := filepath.Join(project, fmt.Sprintf("checkout-%02d", index))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		metadata := metadataForInventory(path, common, source, fmt.Sprintf("id-%02d", index))
		if err := WriteMetadata(SidecarPath(path), metadata); err != nil {
			t.Fatal(err)
		}
	}

	var active atomic.Int32
	var maximum atomic.Int32
	fake := func(ctx context.Context, directory string, _ time.Duration, args ...string) ([]byte, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if strings.HasSuffix(directory, "checkout-03") {
			return nil, errors.New("injected probe failure")
		}
		return fakeInventoryGit(directory, common, args...)
	}
	inventory := testInventory(root)
	inventory.git = fake
	entries, err := inventory.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != count {
		t.Fatalf("entries = %d, want %d", len(entries), count)
	}
	if maximum.Load() > maxInventoryWorkers || maximum.Load() < 2 {
		t.Fatalf("maximum concurrency = %d, want 2..%d", maximum.Load(), maxInventoryWorkers)
	}
	failures := 0
	for _, entry := range entries {
		if entry.Classification == ClassificationGitUnavailable {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("Git-unavailable entries = %d, want 1: %+v", failures, entries)
	}
}

func TestInventoryCancellationStopsWorkers(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "worktrees")
	project := filepath.Join(root, "api")
	common := filepath.Join(t.TempDir(), "common")
	source := filepath.Join(t.TempDir(), "source")
	for _, directory := range []string{project, common, source} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(project, "checkout")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteMetadata(SidecarPath(path), metadataForInventory(path, common, source, "id")); err != nil {
		t.Fatal(err)
	}
	inventory := testInventory(root)
	started := make(chan struct{}, 1)
	inventory.git = func(ctx context.Context, _ string, _ time.Duration, _ ...string) ([]byte, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type answer struct {
		entries []Entry
		err     error
	}
	finished := make(chan answer, 1)
	go func() {
		entries, err := inventory.List(ctx)
		finished <- answer{entries: entries, err: err}
	}()
	select {
	case <-started:
		cancel()
	case <-ctx.Done():
		t.Fatal("inventory Git worker did not start")
	}
	var result answer
	select {
	case result = <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("inventory did not stop after cancellation")
	}
	entries, err := result.entries, result.err
	if !errors.Is(err, context.Canceled) || len(entries) != 1 || entries[0].Classification != ClassificationGitUnavailable {
		t.Fatalf("entries=%+v err=%v, want partial canceled entry", entries, err)
	}
}

func TestResolveSelectorExactPrefixBranchPathAndAmbiguity(t *testing.T) {
	t.Parallel()

	pathRoot := t.TempDir()
	canonicalRoot, err := canonicalDirectory(pathRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{ID: "aaaaaaaa-1111", Branch: "shared", Path: canonicalAdjacentPath("/tmp/one")},
		{ID: "aaaaaaaa-2222", Branch: "shared", Path: canonicalAdjacentPath("/tmp/two")},
		{ID: "bbbbbbbb-3333", Branch: "unique", Path: canonicalRoot},
	}
	for selector, want := range map[string]string{
		"aaaaaaaa-1111": "aaaaaaaa-1111", "bbbb": "bbbbbbbb-3333", "unique": "bbbbbbbb-3333", "/tmp/two": "aaaaaaaa-2222", pathRoot: "bbbbbbbb-3333",
	} {
		got, err := ResolveSelector(entries, selector)
		if err != nil || got.ID != want {
			t.Fatalf("ResolveSelector(%q) = %+v, %v; want %s", selector, got, err, want)
		}
	}
	_, err = ResolveSelector(entries, "aaaaaaaa")
	var selectorErr *SelectorError
	if !errors.Is(err, ErrSelectorAmbiguous) || !errors.As(err, &selectorErr) || len(selectorErr.Candidates) != 2 ||
		selectorErr.Candidates[0].ID != "aaaaaaaa-1111" {
		t.Fatalf("prefix ambiguity error = %#v", err)
	}
	_, err = ResolveSelector(entries, "shared")
	if !errors.Is(err, ErrSelectorAmbiguous) {
		t.Fatalf("branch ambiguity error = %v", err)
	}
	_, err = ResolveSelector(entries, "missing")
	if !errors.Is(err, ErrSelectorNotFound) {
		t.Fatalf("not-found error = %v", err)
	}
}

func TestResolveSelectorOffersNearMissBranchesWhenNothingMatches(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{ID: "cccccccc-1111", Branch: "amx/fix-plans-footer", Path: "/tmp/one"},
		{ID: "bbbbbbbb-2222", Branch: "amx/fix-dashboard", Path: "/tmp/two"},
		{ID: "aaaaaaaa-3333", Branch: "amx/plans", Path: "/tmp/three"},
		{ID: "dddddddd-4444", Path: "/tmp/detached"},
	}
	candidateIDs := func(selector string) []string {
		t.Helper()
		_, err := ResolveSelector(entries, selector)
		var selectorErr *SelectorError
		if !errors.Is(err, ErrSelectorNotFound) || !errors.As(err, &selectorErr) {
			t.Fatalf("ResolveSelector(%q) error = %v, want not found", selector, err)
		}
		ids := []string{}
		for _, candidate := range selectorErr.Candidates {
			ids = append(ids, candidate.ID)
		}
		return ids
	}
	// Containment ranks ahead of edit distance; ties keep selector order.
	if got, want := candidateIDs("PLANS"), []string{"aaaaaaaa-3333", "cccccccc-1111"}; !slices.Equal(got, want) {
		t.Fatalf("substring candidates = %v, want %v", got, want)
	}
	if got, want := candidateIDs("amx/plnas"), []string{"aaaaaaaa-3333"}; !slices.Equal(got, want) {
		t.Fatalf("typo candidates = %v, want %v", got, want)
	}
	if got := candidateIDs("unrelated"); len(got) != 0 {
		t.Fatalf("unrelated selector candidates = %v, want none", got)
	}
}

func TestSortEntriesAttentionAndTieBreakers(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	entries := []Entry{
		{ID: "clean", Classification: ClassificationValid, GitAvailable: true, CreatedAt: when},
		{ID: "dirty", Classification: ClassificationValid, GitAvailable: true, Dirty: true, CreatedAt: when},
		{ID: "live", Classification: ClassificationValid, GitAvailable: true, Usage: Usage{Live: true}, CreatedAt: when},
		{ID: "missing", Classification: ClassificationMissing, CreatedAt: when},
		{ID: "invalid-new", Classification: ClassificationMetadataInvalid, CreatedAt: when.Add(time.Hour)},
	}
	SortEntries(entries)
	got := make([]string, len(entries))
	for index := range entries {
		got[index] = entries[index].ID
	}
	want := []string{"invalid-new", "missing", "live", "dirty", "clean"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func testInventory(root string) *Inventory {
	return &Inventory{directory: func() (string, error) { return root, nil }, git: runGit, workers: maxInventoryWorkers}
}

func metadataForInventory(path, common, source, id string) Metadata {
	return Metadata{
		SchemaVersion: MetadataSchemaVersion, ID: id, ProjectLabel: "api", Path: filepath.Clean(path),
		Branch: "amx/api/branch", GitCommonDir: filepath.Clean(common), SourceRoot: filepath.Clean(source),
		SourceBranch: "main", BaseCommit: strings.Repeat("a", 40), SourceConversationID: "source",
		CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
}

func fakeInventoryGit(directory, common string, args ...string) ([]byte, error) {
	command := strings.Join(args, " ")
	switch command {
	case "rev-parse --path-format=absolute --show-toplevel":
		return []byte(directory + "\n"), nil
	case "rev-parse --path-format=absolute --git-common-dir":
		return []byte(common + "\n"), nil
	case "worktree list --porcelain -z":
		return []byte("worktree " + directory + "\x00HEAD " + strings.Repeat("b", 40) + "\x00branch refs/heads/amx/api/branch\x00\x00"), nil
	case "status --porcelain=v1 -z":
		return nil, nil
	case "rev-parse --verify --quiet refs/heads/main^{commit}":
		return []byte(strings.Repeat("c", 40) + "\n"), nil
	case "rev-list --left-right --count refs/heads/main...HEAD":
		return []byte("0\t0\n"), nil
	case "rev-list -n 1 HEAD --not refs/heads/main":
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected fake Git command %q", command)
	}
}

func treeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var snapshot []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot = append(snapshot, fmt.Sprintf("%s|%s|%d|%d", rel, info.Mode(), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(snapshot)
	return snapshot
}

func execGit(directory string, args ...string) error {
	_, err := runGit(context.Background(), directory, gitCommandTimeout, args...)
	return err
}
