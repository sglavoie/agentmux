package plans

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A newer sibling (for example, a manifest restored from an archive) must
// not mask a same-size edit to an older file in the plan.
func planWithOlderFile(t *testing.T) (root, dir string, oldTime time.Time) {
	t.Helper()
	root = t.TempDir()
	dir = seedPlanDir(t, root, "one", map[string]string{
		"manifest.md": "# Plan",
		"session.md":  "old needle",
	})
	oldTime = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	setPlanFileTime(t, dir, "session.md", oldTime)
	setPlanFileTime(t, dir, "manifest.md", oldTime.Add(24*time.Hour))
	return root, dir, oldTime
}

func setPlanFileTime(t *testing.T, dir, rel string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(dir, rel), when, when); err != nil {
		t.Fatal(err)
	}
}

func replaceOlderPlanFile(t *testing.T, dir string, oldTime time.Time) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "session.md"), []byte("new needle"), 0o600); err != nil {
		t.Fatal(err)
	}
	setPlanFileTime(t, dir, "session.md", oldTime.Add(time.Hour))
}

func TestRefreshDetectsSameSizeEditBelowNewestTimestamp(t *testing.T) {
	t.Parallel()
	root, dir, oldTime := planWithOlderFile(t)
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	// Detection must survive loading the cache in a different process.
	idx, err := LoadIndexAt(idx.Path())
	if err != nil {
		t.Fatal(err)
	}
	replaceOlderPlanFile(t, dir, oldTime)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]int{"old": 0, "new": 1} {
		results, err := Search(idx, Query{Text: query})
		if err != nil || len(results) != want {
			t.Errorf("Search(%q) = %d results, %v; want %d", query, len(results), err, want)
		}
	}
}

func TestRefreshRejectsSameSizeEditAfterDiscovery(t *testing.T) {
	root, dir, oldTime := planWithOlderFile(t)
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	key := PlanKey(dir)
	// Force discovery to read the old source, then change a file before merge
	// without changing the aggregate size or newest timestamp.
	if err := idx.store.Delete(key); err != nil {
		t.Fatal(err)
	}
	original := refreshPlansBeforeMergeForTest
	t.Cleanup(func() { refreshPlansBeforeMergeForTest = original })
	refreshPlansBeforeMergeForTest = func() {
		refreshPlansBeforeMergeForTest = nil
		replaceOlderPlanFile(t, dir, oldTime)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	if text, err := idx.store.Read(key); !os.IsNotExist(err) {
		t.Fatalf("stale discovery wrote a sidecar: %q, %v", text, err)
	}
}

func TestSearchRepairRejectsSameSizeConcurrentRefresh(t *testing.T) {
	t.Parallel()
	root, dir, oldTime := planWithOlderFile(t)
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	key := PlanKey(dir)
	if err := idx.store.Delete(key); err != nil {
		t.Fatal(err)
	}
	other, err := LoadIndexAt(idx.Path())
	if err != nil {
		t.Fatal(err)
	}
	read := func(meta PlanMeta) (string, error) {
		text, err := readPlanSearchText(meta)
		if err != nil {
			return "", err
		}
		replaceOlderPlanFile(t, dir, oldTime)
		if err := other.Refresh(root); err != nil {
			t.Fatal(err)
		}
		return text, nil
	}
	results, err := search(idx, Query{Text: "old"}, read)
	if err != nil || len(results) != 1 {
		t.Fatalf("in-flight search lost its snapshot: %+v, %v", results, err)
	}
	if text, err := idx.store.Read(key); err != nil || text != "# plan\nnew needle" {
		t.Fatalf("search repair replaced newer corpus: %q, %v", text, err)
	}
}

func TestRefreshPreservesFilesAddedByConcurrentRefresh(t *testing.T) {
	root, dir, _ := planWithOlderFile(t)
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	if err := idx.store.Delete(PlanKey(dir)); err != nil {
		t.Fatal(err)
	}
	other, err := LoadIndexAt(idx.Path())
	if err != nil {
		t.Fatal(err)
	}
	original := refreshPlansBeforeMergeForTest
	t.Cleanup(func() { refreshPlansBeforeMergeForTest = original })
	refreshPlansBeforeMergeForTest = func() {
		refreshPlansBeforeMergeForTest = nil
		if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("added needle"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := other.Refresh(root); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	results, err := Search(idx, Query{Text: "added"})
	if err != nil || len(results) != 1 {
		t.Fatalf("stale refresh hid the added file: %+v, %v", results, err)
	}
	if len(results[0].Meta.Files) != 3 {
		t.Fatalf("stale refresh replaced the newer file list: %v", results[0].Meta.Files)
	}
}

func TestRefreshUpgradesLegacyFingerprintOnce(t *testing.T) {
	t.Parallel()
	root, dir, _ := planWithOlderFile(t)
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	key := PlanKey(dir)
	meta := idx.entries[key]
	meta.FileFingerprint = ""
	meta.Snippet = "outdated cached snippet"
	idx.entries[key] = meta
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadIndexAt(idx.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	meta, _ = idx.Get(key)
	if meta.FileFingerprint == "" || meta.Snippet != "# Plan" {
		t.Fatalf("legacy cache was not rebuilt: %+v", meta)
	}
	before, err := os.Stat(idx.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(idx.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("unchanged upgraded cache was rewritten")
	}
}
