package plans

// What a re-Refresh does to the state a previous one left behind. Two rules
// meet here. A cache hit must not rewrite a sidecar that is already correct,
// but "already correct" has to account for a sidecar that vanished, was
// replaced by something that is not a regular file, or whose plan gained and
// lost markdown files without changing mtimes. And an entry that stops
// qualifying — the directory vanished, its last markdown file was removed, or
// it was indexed under a name Refresh now skips — must take its sidecar with
// it, so a stale entry can never keep answering searches.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRefresh_IdempotentNoSidecarRewrite(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# title\nbody",
		"session-1.md": "session 1",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 1: %v", err)
	}
	sidecar := idx.store.Path(PlanKey(planDir))
	// Back-date the sidecar well past any filesystem's mtime granularity: a
	// rewrite stamps it with the present, so an unchanged old time proves
	// there was none, without sleeping and hoping the clock moved.
	backdated := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(sidecar, backdated, backdated); err != nil {
		t.Fatalf("back-date sidecar: %v", err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 2: %v", err)
	}
	info, err := os.Stat(sidecar)
	if err != nil {
		t.Fatalf("stat sidecar again: %v", err)
	}
	if !info.ModTime().Equal(backdated) {
		t.Errorf("sidecar mtime changed: %v -> %v (expected no rewrite on cache hit)", backdated, info.ModTime())
	}
}

func TestRefresh_RebuildsMissingSidecarOnCacheHit(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# title",
		"session-1.md": "body-only-needle",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 1: %v", err)
	}

	sidecar := idx.store.Path(PlanKey(planDir))
	if err := os.Remove(sidecar); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}

	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 2: %v", err)
	}
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar was not rebuilt: %v", err)
	}

	results, err := Search(idx, Query{Text: "body-only-needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Meta.DirPath != planDir {
		t.Errorf("Meta.DirPath = %q, want %q", results[0].Meta.DirPath, planDir)
	}
}

func TestRefresh_RebuildsNonRegularSidecarOnCacheHit(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# title",
		"session-1.md": "body-only-needle",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 1: %v", err)
	}

	sidecar := idx.store.Path(PlanKey(planDir))
	if err := os.Remove(sidecar); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}
	if err := os.Mkdir(sidecar, 0o755); err != nil {
		t.Fatalf("replace sidecar with directory: %v", err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 2: %v", err)
	}
	if info, err := os.Stat(sidecar); err != nil {
		t.Fatalf("stat rebuilt sidecar: %v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("rebuilt sidecar mode = %v, want regular file", info.Mode())
	}

	results, err := Search(idx, Query{Text: "body-only-needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Meta.DirPath != planDir {
		t.Errorf("Meta.DirPath = %q, want %q", results[0].Meta.DirPath, planDir)
	}
}

func TestRefresh_RebuildsWhenMarkdownFileListChanges(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md": "# title",
		"old.md":      "old-needle",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 1: %v", err)
	}
	original, ok := idx.Get(PlanKey(planDir))
	if !ok {
		t.Fatalf("entry missing after first refresh")
	}

	if err := os.Remove(filepath.Join(planDir, "old.md")); err != nil {
		t.Fatalf("remove old markdown: %v", err)
	}
	newPath := filepath.Join(planDir, "new.md")
	if err := os.WriteFile(newPath, []byte("new-needle"), 0o644); err != nil {
		t.Fatalf("write new markdown: %v", err)
	}
	if err := os.Chtimes(newPath, original.ModTime, original.ModTime); err != nil {
		t.Fatalf("set new markdown time: %v", err)
	}

	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 2: %v", err)
	}

	oldResults, err := Search(idx, Query{Text: "old-needle"})
	if err != nil {
		t.Fatalf("Search old needle: %v", err)
	}
	if len(oldResults) != 0 {
		t.Fatalf("len(oldResults) = %d, want 0 after old markdown was removed", len(oldResults))
	}

	newResults, err := Search(idx, Query{Text: "new-needle"})
	if err != nil {
		t.Fatalf("Search new needle: %v", err)
	}
	if len(newResults) != 1 {
		t.Fatalf("len(newResults) = %d, want 1 after sidecar rebuild", len(newResults))
	}
	if newResults[0].Meta.DirPath != planDir {
		t.Errorf("Meta.DirPath = %q, want %q", newResults[0].Meta.DirPath, planDir)
	}
}

func TestRefresh_DropsPreviouslyIndexedSkippedDirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"sources", ".hidden"} {
		planDir := seedPlanDir(t, root, name, map[string]string{
			"manifest.md": "# should not remain indexed",
		})
		key := PlanKey(planDir)

		idx := newTestIndex(t)
		idx.entries[key] = PlanMeta{
			Name:    name,
			DirPath: planDir,
			Files:   []string{"manifest.md"},
			Snippet: "# should not remain indexed",
		}
		if err := idx.store.Write(key, "should not remain indexed"); err != nil {
			t.Fatalf("seed sidecar for %s: %v", name, err)
		}
		sidecar := idx.store.Path(key)

		if err := idx.Refresh(root); err != nil {
			t.Fatalf("Refresh with %s: %v", name, err)
		}
		if _, ok := idx.Get(key); ok {
			t.Errorf("%s entry should be dropped", name)
		}
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Errorf("%s sidecar should be deleted; stat err = %v", name, err)
		}
	}
}

func TestRefresh_DropsVanishedPlans(t *testing.T) {
	root := t.TempDir()
	planA := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md": "# alpha",
	})
	planB := seedPlanDir(t, root, "beta-ccdd44556677", map[string]string{
		"manifest.md": "# beta",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 1: %v", err)
	}
	if idx.Len() != 2 {
		t.Fatalf("after first refresh Len = %d, want 2", idx.Len())
	}
	sidecarB := idx.store.Path(PlanKey(planB))
	if _, err := os.Stat(sidecarB); err != nil {
		t.Fatalf("expected sidecar B to exist: %v", err)
	}

	// Remove plan B from disk; re-refresh should drop its entry and
	// sidecar.
	if err := os.RemoveAll(planB); err != nil {
		t.Fatalf("removing plan B: %v", err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 2: %v", err)
	}
	if idx.Len() != 1 {
		t.Fatalf("after vanish Len = %d, want 1", idx.Len())
	}
	if _, ok := idx.Get(PlanKey(planA)); !ok {
		t.Errorf("plan A unexpectedly dropped")
	}
	if _, ok := idx.Get(PlanKey(planB)); ok {
		t.Errorf("plan B entry should be gone")
	}
	if _, err := os.Stat(sidecarB); !os.IsNotExist(err) {
		t.Errorf("plan B sidecar should be deleted; stat err = %v", err)
	}
}

func TestRefresh_DropsPlanWhenMarkdownRemoved(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md": "# alpha",
		"notes.txt":   "not indexed",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 1: %v", err)
	}
	key := PlanKey(planDir)
	sidecar := idx.store.Path(key)
	if _, ok := idx.Get(key); !ok {
		t.Fatalf("entry missing after first refresh")
	}
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar missing after first refresh: %v", err)
	}

	if err := os.Remove(filepath.Join(planDir, "manifest.md")); err != nil {
		t.Fatalf("removing manifest: %v", err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 2: %v", err)
	}
	if _, ok := idx.Get(key); ok {
		t.Errorf("entry should be dropped after plan has no markdown files")
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Errorf("sidecar should be deleted after markdown removal; stat err = %v", err)
	}
}

// TestRefresh_UnchangedPlansDoNotRewriteTheCache pins that a refresh finding
// nothing new leaves plans.json alone. The file is rewritten in a compact form
// between refreshes; a refresh that persisted anyway would put the indented
// form back, which is the evidence this test reads. A real change must still
// be written.
func TestRefresh_UnchangedPlansDoNotRewriteTheCache(t *testing.T) {
	root := t.TempDir()
	seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md": "# Alpha\nbody",
	})
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}
	written, err := os.ReadFile(idx.path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(written, &decoded); err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(compact, written) {
		t.Fatal("setup: the cache must be written indented for the marker to work")
	}
	if err := os.WriteFile(idx.path, compact, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := idx.Refresh(root); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	if after, _ := os.ReadFile(idx.path); !bytes.Equal(after, compact) {
		t.Fatal("a refresh that found nothing new rewrote plans.json")
	}

	seedPlanDir(t, root, "beta-ccdd44556677", map[string]string{"manifest.md": "# Beta\nbody"})
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("third Refresh: %v", err)
	}
	if after, _ := os.ReadFile(idx.path); bytes.Equal(after, compact) {
		t.Fatal("a refresh that found a new plan did not persist it")
	}
	if got := len(idx.List()); got != 2 {
		t.Fatalf("entries = %d, want 2", got)
	}
}
