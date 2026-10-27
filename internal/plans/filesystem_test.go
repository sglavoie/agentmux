package plans

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func filesystemPlanFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# Alpha plan",
		"session-1.md": "body-only filesystem needle",
	})
	return root, planDir
}

func TestFilesystemPlanLoadStrictDiagnosticsAndMalformedRecovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")
	if err := os.WriteFile(path, []byte(`{"entries":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if idx, err := loadIndexAtStrict(path); err == nil || idx != nil {
		t.Fatalf("strict load = %#v, %v; want nil decode failure", idx, err)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 0 {
		t.Fatalf("strict loader quarantined metadata: %v", matches)
	}

	idx, warning := LoadIndexAt(path)
	if idx == nil || warning == nil {
		t.Fatalf("recovering load = %#v, %v", idx, warning)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 1 {
		t.Fatalf("quarantine files = %v, want one", matches)
	}
	root, _ := filesystemPlanFixture(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh after quarantine: %v", err)
	}
	if idx.Len() != 1 {
		t.Fatalf("rebuilt Len = %d, want 1", idx.Len())
	}
	if _, err := loadIndexAtStrict(path); err != nil {
		t.Fatalf("rebuilt metadata is not strict-loadable: %v", err)
	}
}

func TestFilesystemPlanRefreshReturnsSnapshotWhenQuarantineOrPersistenceFails(t *testing.T) {
	root, _ := filesystemPlanFixture(t)
	path := filepath.Join(t.TempDir(), "plans.json")
	if err := os.WriteFile(path, []byte(`{"entries":`), 0o644); err != nil {
		t.Fatal(err)
	}
	originalQuarantine := quarantinePlanIndex
	quarantinePlanIndex = func(string) (string, error) { return "", errors.New("quarantine denied") }
	t.Cleanup(func() { quarantinePlanIndex = originalQuarantine })
	idx, warning := LoadIndexAt(path)
	if idx == nil || warning == nil {
		t.Fatalf("LoadIndexAt = %#v, %v", idx, warning)
	}
	if err := idx.Refresh(root); err == nil {
		t.Fatal("Refresh succeeded without quarantine warning")
	}
	if idx.Len() != 1 {
		t.Fatalf("rebuilt in-memory Len = %d, want 1", idx.Len())
	}
	if _, err := loadIndexAtStrict(path); err != nil {
		t.Fatalf("metadata was not repaired after quarantine failure: %v", err)
	}

	originalWrite := writePlanIndex
	writePlanIndex = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { writePlanIndex = originalWrite })
	secondPath := filepath.Join(t.TempDir(), "plans.json")
	second, err := LoadIndexAt(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Refresh(root); err == nil {
		t.Fatal("Refresh succeeded despite persistence failure")
	}
	if second.Len() != 1 {
		t.Fatalf("snapshot withheld after persistence failure: Len=%d", second.Len())
	}
	writePlanIndex = originalWrite
	if err := second.Save(); err != nil {
		t.Fatalf("retrying dirty snapshot Save: %v", err)
	}
	if loaded, err := LoadIndexAt(secondPath); err != nil || loaded.Len() != 1 {
		t.Fatalf("saved retry = %#v, %v", loaded, err)
	}
}

func TestFilesystemPlanRefreshPreservesPartialPlanAndCachedRows(t *testing.T) {
	root, planDir := filesystemPlanFixture(t)
	path := filepath.Join(t.TempDir(), "plans.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	cached, ok := idx.Get(PlanKey(planDir))
	if !ok {
		t.Fatal("fixture plan was not cached")
	}
	if err := os.Remove(idx.store.Path(PlanKey(planDir))); err != nil {
		t.Fatal(err)
	}

	originalHook := refreshPlanBeforeReadForTest
	removed := false
	refreshPlanBeforeReadForTest = func(path string) {
		if !removed && filepath.Base(path) == "session-1.md" {
			removed = true
			_ = os.Remove(path)
		}
	}
	t.Cleanup(func() { refreshPlanBeforeReadForTest = originalHook })
	if err := idx.Refresh(root); err == nil {
		t.Fatal("Refresh did not report the individual-file failure")
	}
	got, ok := idx.Get(PlanKey(planDir))
	if !ok {
		t.Fatal("cached plan was dropped after an individual-file failure")
	}
	if got.Snippet != cached.Snippet {
		t.Fatalf("cached snippet changed from %q to %q", cached.Snippet, got.Snippet)
	}

	// A new plan with one failing file still contributes useful metadata from
	// the remaining file instead of hiding the entire discovery pass.
	newDir := seedPlanDir(t, root, "beta-ccdd44556677", map[string]string{
		"manifest.md": "# Beta plan",
		"broken.md":   "unreadable during scan",
	})
	removed = false
	refreshPlanBeforeReadForTest = func(path string) {
		if !removed && filepath.Base(path) == "broken.md" {
			removed = true
			_ = os.Remove(path)
		}
	}
	if err := idx.Refresh(root); err == nil {
		t.Fatal("Refresh did not report partial new-plan failure")
	}
	if _, ok := idx.Get(PlanKey(newDir)); !ok {
		t.Fatal("partial metadata for a new plan was dropped")
	}
}

func TestFilesystemPlanRefreshWritesSidecarOnlyAfterFreshnessGate(t *testing.T) {
	root, planDir := filesystemPlanFixture(t)
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}

	key := PlanKey(planDir)
	sidecar := idx.store.Path(key)
	beforeSidecar, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	beforeMeta, ok := idx.Get(key)
	if !ok {
		t.Fatal("fixture plan was not cached")
	}

	source := filepath.Join(planDir, "session-1.md")
	if err := os.WriteFile(source, []byte("discovery-only sidecar text"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalHook := refreshPlansBeforeMergeForTest
	var hookErr error
	refreshPlansBeforeMergeForTest = func() {
		hookErr = os.WriteFile(source, []byte("source changed again after discovery with a different size"), 0o644)
	}
	t.Cleanup(func() { refreshPlansBeforeMergeForTest = originalHook })

	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if hookErr != nil {
		t.Fatalf("changing source before merge: %v", hookErr)
	}
	afterSidecar, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterSidecar, beforeSidecar) {
		t.Fatalf("freshness-rejected discovery rewrote sidecar\nbefore: %q\nafter:  %q", beforeSidecar, afterSidecar)
	}
	afterMeta, ok := idx.Get(key)
	if !ok {
		t.Fatal("freshness-rejected discovery removed cached metadata")
	}
	if afterMeta.Size != beforeMeta.Size || !afterMeta.ModTime.Equal(beforeMeta.ModTime) {
		t.Fatalf("freshness-rejected discovery changed metadata from size=%d mtime=%v to size=%d mtime=%v",
			beforeMeta.Size, beforeMeta.ModTime, afterMeta.Size, afterMeta.ModTime)
	}
}

func TestFilesystemPlanSearchRepairsUnreadableSidecar(t *testing.T) {
	t.Parallel()

	root, planDir := filesystemPlanFixture(t)
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	key := PlanKey(planDir)
	sidecar := idx.store.Path(key)
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	results, err := Search(idx, Query{Text: "filesystem needle"})
	if err != nil || len(results) != 1 {
		t.Fatalf("Search = %+v, %v", results, err)
	}
	if info, err := os.Stat(sidecar); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("sidecar repair mode = %v, %v", info, err)
	}
}

func TestFilesystemPlanEmptyDiscoveryPreservesConcurrentRefresh(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "one", map[string]string{"manifest.md": "original plan"})
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	other, err := LoadIndexAt(idx.Path())
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(planDir, "manifest.md")
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	// The first scan sees an empty directory. Before its removal merges,
	// another process finishes writing and indexing a replacement plan.
	originalHook := refreshPlansBeforeMergeForTest
	t.Cleanup(func() { refreshPlansBeforeMergeForTest = originalHook })
	refreshPlansBeforeMergeForTest = func() {
		refreshPlansBeforeMergeForTest = nil
		if err := os.WriteFile(manifest, []byte("replacement plan with fresh needle"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := other.Refresh(root); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	reopened, err := LoadIndexAt(idx.Path())
	if err != nil {
		t.Fatal(err)
	}
	key := PlanKey(planDir)
	if _, ok := reopened.Get(key); !ok {
		t.Fatal("stale empty-directory discovery deleted the concurrently refreshed plan")
	}
	text, err := reopened.store.Read(key)
	if err != nil || text != "replacement plan with fresh needle" {
		t.Fatalf("concurrently refreshed sidecar = %q, %v", text, err)
	}
}

func TestFilesystemPlanConcurrentLoadRefreshDelete(t *testing.T) {
	t.Parallel()

	root, planDir := filesystemPlanFixture(t)
	path := filepath.Join(t.TempDir(), "plans.json")
	seed, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Refresh(root); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 12; iteration++ {
				idx, loadErr := LoadIndexAt(path)
				if loadErr != nil {
					t.Errorf("worker %d load: %v", worker, loadErr)
					return
				}
				if (worker+iteration)%3 == 0 {
					_ = idx.Delete(PlanKey(planDir))
				} else if refreshErr := idx.Refresh(root); refreshErr != nil {
					t.Errorf("worker %d refresh: %v", worker, refreshErr)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("concurrent metadata is malformed: %s", data)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("shared advisory lock file missing: %v", err)
	}
}

func TestFilesystemPlanDeleteSidecarFailureKeepsCleanState(t *testing.T) {
	t.Parallel()

	root, planDir := filesystemPlanFixture(t)
	path := filepath.Join(t.TempDir(), "plans.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	key := PlanKey(planDir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sidecar := idx.store.Path(key)
	if err := os.Remove(sidecar); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "block-removal"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := idx.Delete(key); err == nil {
		t.Fatal("Delete succeeded despite sidecar removal failure")
	}
	if _, ok := idx.Get(key); !ok {
		t.Fatal("index entry was removed after sidecar failure")
	}
	idx.mu.Lock()
	dirty := idx.filesystemDirty
	idx.mu.Unlock()
	if dirty {
		t.Fatal("sidecar failure changed a clean index to dirty")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("sidecar failure changed metadata\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestFilesystemPlanAtomicReplacementLeavesNoTemporaryFiles(t *testing.T) {
	t.Parallel()

	root, _ := filesystemPlanFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 3; iteration++ {
		if err := idx.Refresh(root); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if matched, _ := filepath.Match(".plans.json.*.tmp", entry.Name()); matched {
			t.Fatalf("atomic replacement left temporary file %s", entry.Name())
		}
	}
}

func TestFilesystemPlanUnreadableCacheAbortsMutations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Index, string, string) error
	}{
		{name: "Save", mutate: func(idx *Index, _, _ string) error {
			return idx.Save()
		}},
		{name: "Delete", mutate: func(idx *Index, _, key string) error {
			return idx.Delete(key)
		}},
		{name: "Refresh", mutate: func(idx *Index, root, _ string) error {
			return idx.Refresh(root)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, root, key, path, before, restore := staleUnreadablePlanCache(t)
			if err := tt.mutate(idx, root, key); err == nil {
				t.Fatalf("%s succeeded with an unreadable cache", tt.name)
			}
			restore()
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("%s rewrote an unreadable cache\nbefore: %s\nafter:  %s", tt.name, before, after)
			}
			readable, err := idx.store.Readable(key)
			if err != nil {
				t.Fatal(err)
			}
			if !readable {
				t.Fatalf("%s removed a search sidecar before aborting", tt.name)
			}
		})
	}
}

func staleUnreadablePlanCache(t *testing.T) (*Index, string, string, string, []byte, func()) {
	t.Helper()
	root, planDir := filesystemPlanFixture(t)
	path := filepath.Join(t.TempDir(), "plans.json")
	seed, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Refresh(root); err != nil {
		t.Fatal(err)
	}
	key := PlanKey(planDir)
	stale, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := LoadIndexAt(path)
	if err != nil {
		t.Fatal(err)
	}
	newer.mu.Lock()
	newer.entries[PlanKey(filepath.Join(root, "concurrent-aabb112233cc"))] = PlanMeta{
		Name: "concurrent", DirPath: filepath.Join(root, "concurrent-aabb112233cc"),
	}
	newer.filesystemDirty = true
	newer.mu.Unlock()
	if err := newer.Save(); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		if err := os.Chmod(path, 0o644); err != nil {
			t.Errorf("restoring cache permissions: %v", err)
		}
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	if file, openErr := os.Open(path); openErr == nil {
		_ = file.Close()
		t.Skip("file permissions do not make the cache unreadable for this user")
	}
	return stale, root, key, path, before, restore
}
