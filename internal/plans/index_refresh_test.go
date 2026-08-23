package plans

// What a first Refresh discovers and what metadata it derives: which
// directories are plans at all, which files inside them count, how deep the
// walk goes, and where the snippet comes from. What a *second* Refresh does to
// state the first one left behind — sidecar rewrites and entry pruning — lives
// in index_refresh_recache_test.go.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// seedPlanDir creates dir under root and writes the given files (relative
// path -> contents). It returns the absolute plan directory.
func seedPlanDir(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	planDir := filepath.Join(root, name)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", planDir, err)
	}
	for rel, contents := range files {
		full := filepath.Join(planDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent for %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return planDir
}

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	return idx
}

func TestRefresh_PopulatesEntriesAndSkipsSources(t *testing.T) {
	root := t.TempDir()
	planA := seedPlanDir(t, root, "alpha-aabb112233cc-ddee445566ff", map[string]string{
		"manifest.md":  "# alpha plan\n\nfirst line is the title",
		"session-1.md": "session 1 body",
		"README.txt":   "ignore me",
	})
	planB := seedPlanDir(t, root, "plan-deadbeef00ff", map[string]string{
		"manifest.md":  "\n\n# plan title\nbody",
		"session-1.md": "another session",
	})
	// Should be skipped: name == sources
	_ = seedPlanDir(t, root, "sources", map[string]string{
		"loose-prompt.md": "should not be indexed",
	})
	// Should be skipped: dot-prefixed
	_ = seedPlanDir(t, root, ".hidden", map[string]string{
		"x.md": "ignore",
	})
	// A loose file at the root (not a dir) should also be ignored
	if err := os.WriteFile(filepath.Join(root, "stray.md"), []byte("loose"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if idx.Len() != 2 {
		t.Fatalf("Len = %d, want 2", idx.Len())
	}
	a, ok := idx.Get(PlanKey(planA))
	if !ok {
		t.Fatalf("plan A missing from index")
	}
	if a.Name != "alpha" {
		t.Errorf("plan A Name = %q, want \"alpha\"", a.Name)
	}
	wantFiles := []string{"manifest.md", "session-1.md"}
	if !sort.StringsAreSorted(a.Files) {
		t.Errorf("plan A Files not sorted: %v", a.Files)
	}
	if len(a.Files) != 2 || a.Files[0] != wantFiles[0] || a.Files[1] != wantFiles[1] {
		t.Errorf("plan A Files = %v, want %v (README.txt should be excluded)", a.Files, wantFiles)
	}
	if a.Snippet == "" {
		t.Errorf("plan A Snippet empty; want first non-empty manifest line")
	}
	if a.Size <= 0 {
		t.Errorf("plan A Size = %d, want > 0", a.Size)
	}

	b, ok := idx.Get(PlanKey(planB))
	if !ok {
		t.Fatalf("plan B missing from index")
	}
	if b.Name != "plan" {
		t.Errorf("plan B Name = %q, want \"plan\"", b.Name)
	}

	// Sidecar was written for each.
	for _, dir := range []string{planA, planB} {
		path := idx.store.Path(PlanKey(dir))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing sidecar for %s: %v", dir, err)
		}
	}
}

func TestRefresh_RelativeRootStoresAbsolutePaths(t *testing.T) {
	base := t.TempDir()
	rootName := "plans-root"
	planName := "alpha-aabb112233cc"
	absRoot := filepath.Join(base, rootName)
	planDir := seedPlanDir(t, absRoot, planName, map[string]string{
		"manifest.md": "# alpha",
	})

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(base); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(rootName); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	wantDir, err := filepath.Abs(filepath.Join(rootName, planName))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	got, ok := idx.Get(PlanKey(wantDir))
	if !ok {
		t.Fatalf("entry missing under absolute key %q; seed path was %q", wantDir, planDir)
	}
	if got.DirPath != wantDir {
		t.Errorf("DirPath = %q, want absolute path %q", got.DirPath, wantDir)
	}
	if !filepath.IsAbs(got.DirPath) {
		t.Errorf("DirPath = %q, want absolute path", got.DirPath)
	}
	if _, ok := idx.Get(PlanKey(filepath.Join(rootName, planName))); ok {
		t.Errorf("entry unexpectedly indexed under relative key")
	}
}

func TestRefresh_PrefersManifestSnippet(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		// "00-context.md" sorts alphabetically before "manifest.md"
		// so we can verify manifest.md still wins for the snippet.
		"00-context.md": "context first line\nbody",
		"manifest.md":   "# manifest heading\nrest",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, ok := idx.Get(PlanKey(planDir))
	if !ok {
		t.Fatalf("entry missing")
	}
	if got.Snippet != "# manifest heading" {
		t.Errorf("Snippet = %q, want manifest first line", got.Snippet)
	}
}

func TestRefresh_FallsBackToFirstFileSnippetWhenNoManifest(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"00-overview.md": "overview opening line",
		"session-1.md":   "session 1",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, ok := idx.Get(PlanKey(planDir))
	if !ok {
		t.Fatalf("entry missing")
	}
	if got.Snippet != "overview opening line" {
		t.Errorf("Snippet = %q, want first alphabetical file's first line", got.Snippet)
	}
}

func TestRefresh_SkipsPlanWithNoMarkdown(t *testing.T) {
	root := t.TempDir()
	_ = seedPlanDir(t, root, "empty-plan-aabb112233cc", map[string]string{
		"notes.txt": "no markdown here",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if idx.Len() != 0 {
		t.Errorf("Len = %d, want 0 (plan without .md should not index)", idx.Len())
	}
}

func TestRefresh_WalksDepthTwoOnly(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "deep-aabb112233cc", map[string]string{
		"manifest.md":      "# manifest",
		"sub/nested.md":    "depth-2 file",
		"sub/deep/skip.md": "depth-3 should not be indexed",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, ok := idx.Get(PlanKey(planDir))
	if !ok {
		t.Fatalf("entry missing")
	}
	want := []string{"manifest.md", filepath.Join("sub", "nested.md")}
	if len(got.Files) != len(want) {
		t.Fatalf("Files = %v, want %v", got.Files, want)
	}
	for i, f := range got.Files {
		if f != want[i] {
			t.Errorf("Files[%d] = %q, want %q", i, f, want[i])
		}
	}
}
