package plans

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSearchRepairPreservesConcurrentMutation(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"refresh", "delete", "corrupt index"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			planDir := seedPlanDir(t, root, "one", map[string]string{"manifest.md": "old needle"})
			idx := newTestIndex(t)
			if err := idx.Refresh(root); err != nil {
				t.Fatal(err)
			}
			key := PlanKey(planDir)
			if err := idx.store.Delete(key); err != nil {
				t.Fatal(err)
			}
			other, err := LoadIndexAt(idx.Path())
			if err != nil {
				t.Fatal(err)
			}
			// Complete another process's mutation after reading the old
			// source, but before the in-flight search can repair its sidecar.
			read := func(meta PlanMeta) (string, error) {
				text, err := readPlanSearchText(meta)
				if err != nil {
					return "", err
				}
				switch mutation {
				case "refresh":
					if err := os.WriteFile(filepath.Join(planDir, "manifest.md"), []byte("new needle with more text"), 0o600); err != nil {
						t.Fatal(err)
					}
					err = other.Refresh(root)
				case "delete":
					err = other.Delete(key)
				case "corrupt index":
					err = os.WriteFile(idx.Path(), []byte("broken"), 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
				return text, nil
			}
			results, err := search(idx, Query{Text: "needle"}, read)
			if err != nil || len(results) != 1 || results[0].Matches != 1 {
				t.Fatalf("in-flight search lost its source result: %+v, %v", results, err)
			}
			raw, err := idx.store.Read(key)
			if mutation == "refresh" {
				if err != nil || raw != "new needle with more text" {
					t.Fatalf("search overwrote refreshed sidecar: %q, %v", raw, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("search recreated sidecar after %s: %q, %v", mutation, raw, err)
			}
		})
	}
}

func TestSearchRepairsLargeFallbackBatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const count = 130
	for n := range count {
		seedPlanDir(t, root, strconv.Itoa(n), map[string]string{"manifest.md": "title\nbody needle"})
	}
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	for _, meta := range idx.List() {
		if err := idx.store.Delete(PlanKey(meta.DirPath)); err != nil {
			t.Fatal(err)
		}
	}
	results, err := Search(idx, Query{Text: "needle"})
	if err != nil || len(results) != count {
		t.Fatalf("fallback search returned %d results, %v", len(results), err)
	}
	// Check the full batch and final partial batch without another source read.
	for _, meta := range idx.List() {
		text, err := idx.store.Read(PlanKey(meta.DirPath))
		if err != nil || text != "title\nbody needle" {
			t.Errorf("repair for %s = %q, %v", meta.DirPath, text, err)
		}
	}
}
