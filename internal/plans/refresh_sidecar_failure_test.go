package plans

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefreshRetriesFailedSidecarWithoutKeepingStaleSearchText(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"# Plan\nnewneedle with more text\n", ""} {
		for _, retry := range []string{"refresh", "save"} {
			t.Run(retry+"/"+content, func(t *testing.T) {
				t.Parallel()

				root := t.TempDir()
				dir := seedPlanDir(t, root, "plan", map[string]string{"manifest.md": "# Plan\noldneedle\n"})
				idx := newTestIndex(t)
				if err := idx.Refresh(root); err != nil {
					t.Fatal(err)
				}
				key := PlanKey(dir)
				store := idx.store.Dir()
				backup := store + ".backup"
				if err := os.Rename(store, backup); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(store, []byte("block sidecar writes"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "manifest.md"), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if err := idx.Refresh(root); err == nil {
						t.Fatal("Refresh succeeded despite failed sidecar writes")
					}
				}
				if err := os.Remove(store); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(backup, store); err != nil {
					t.Fatal(err)
				}

				// Restoring writes also restores the old, readable sidecar. Both this
				// process and a fresh process must prefer the pending new search corpus.
				loaded, err := LoadIndexAt(idx.Path())
				if err != nil {
					t.Fatal(err)
				}
				for _, index := range []*Index{idx, loaded} {
					for _, query := range []string{"newneedle", "oldneedle"} {
						results, err := Search(index, Query{Text: query})
						want := 0
						if strings.Contains(content, query) {
							want = 1
						}
						if err != nil || len(results) != want {
							t.Errorf("search %q returned %d results, %v; want %d", query, len(results), err, want)
						}
					}
				}
				if retry == "refresh" {
					err = loaded.Refresh(root)
				} else {
					err = loaded.Save()
				}
				if err != nil {
					t.Fatal(err)
				}
				text, err := loaded.store.Read(key)
				if err != nil || text != strings.ToLower(content) {
					t.Errorf("retry retained a stale sidecar: %q, %v", text, err)
				}
				if meta, _ := loaded.Get(key); meta.SearchText != "" || meta.SearchTextPending {
					t.Error("successful retry retained pending search text")
				}
				metadata, err := os.ReadFile(loaded.Path())
				if err != nil || bytes.Contains(metadata, []byte(`"search_text`)) {
					t.Errorf("successful retry retained inline search fields in metadata: %s, %v", metadata, err)
				}
			})
		}
	}
}
