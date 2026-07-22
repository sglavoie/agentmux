package plans

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# alpha title\nmore",
		"session-1.md": "body text",
	})
	projectDir := canonicalPlanTestPath(t, filepath.Join(t.TempDir(), "project"))
	writePlanJSON(t, filepath.Join(planDir, planMetadataFileName), map[string]any{"version": 1, "cwd": projectDir})

	src := newTestIndex(t)
	if err := src.Refresh(root, config.Session{Title: "Alpha project", Directory: projectDir}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	want, ok := src.Get(PlanKey(planDir))
	if !ok {
		t.Fatalf("entry missing pre-roundtrip")
	}

	// Save was already called by Refresh; load a fresh Index against
	// the same path and compare.
	loaded, err := LoadIndexAt(src.Path())
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	if loaded.Len() != 1 {
		t.Fatalf("loaded Len = %d, want 1", loaded.Len())
	}
	got, ok := loaded.Get(PlanKey(planDir))
	if !ok {
		t.Fatalf("entry missing post-roundtrip")
	}
	if got.Name != want.Name {
		t.Errorf("Name: got %q want %q", got.Name, want.Name)
	}
	if got.DirPath != want.DirPath {
		t.Errorf("DirPath: got %q want %q", got.DirPath, want.DirPath)
	}
	if got.Size != want.Size {
		t.Errorf("Size: got %d want %d", got.Size, want.Size)
	}
	if !got.ModTime.Equal(want.ModTime) {
		t.Errorf("ModTime: got %v want %v", got.ModTime, want.ModTime)
	}
	if got.Snippet != want.Snippet {
		t.Errorf("Snippet: got %q want %q", got.Snippet, want.Snippet)
	}
	if got.CWD != want.CWD || got.Project != want.Project {
		t.Errorf("association: got cwd/project %q/%q want %q/%q", got.CWD, got.Project, want.CWD, want.Project)
	}
	if len(got.Projects) != len(want.Projects) {
		t.Errorf("Projects: got %#v want %#v", got.Projects, want.Projects)
	} else {
		for i := range got.Projects {
			if got.Projects[i] != want.Projects[i] {
				t.Errorf("Projects: got %#v want %#v", got.Projects, want.Projects)
				break
			}
		}
	}
	if got.SearchText != "" {
		t.Errorf("SearchText leaked into JSON: %q", got.SearchText)
	}
}

func TestLoadIndexAt_MissingFileReturnsEmpty(t *testing.T) {
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	if idx.Len() != 0 {
		t.Errorf("Len = %d, want 0", idx.Len())
	}
}

func TestDelete_RemovesEntryAndSidecar(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md": "# alpha",
	})
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	key := PlanKey(planDir)
	sidecar := idx.store.Path(key)
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar missing pre-delete: %v", err)
	}
	if err := idx.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := idx.Get(key); ok {
		t.Errorf("entry still present after Delete")
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Errorf("sidecar should be deleted; stat err = %v", err)
	}
}
