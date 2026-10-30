package plans

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshClearsSearchPreviewWhenPlanBecomesEmpty(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"", " \n\t\n"} {
		t.Run(content, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := seedPlanDir(t, root, "plan", map[string]string{"manifest.md": "oldneedle\n"})
			idx := newTestIndex(t)
			if err := idx.Refresh(root); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "manifest.md"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := idx.Refresh(root); err != nil {
				t.Fatal(err)
			}
			meta, ok := idx.Get(PlanKey(dir))
			if !ok || meta.Snippet != "" {
				t.Errorf("empty plan retained an obsolete preview: %+v, present = %t", meta, ok)
			}
			results, err := Search(idx, Query{Text: "oldneedle"})
			if err != nil || len(results) != 0 {
				t.Errorf("empty plan still matches removed text: %+v, %v", results, err)
			}
		})
	}
}
