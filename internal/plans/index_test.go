package plans

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
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

func TestDisplayName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"add-ability-to-search-drifting-beacon-4ca2183c5850", "add-ability-to-search-drifting-beacon"},
		{"agent-mux-memory-exhaustion-fix-9-sessions-8027329421d8-5d44126d1695", "agent-mux-memory-exhaustion-fix-9-sessions"},
		{"plan-deadbeef00ff", "plan"},
		{"plan-x-y-z", "plan-x-y-z"},
		{"plan", "plan"},
		{"", ""},
		{"plan-not12hex-aabbccddeeff", "plan-not12hex"},
		{"plan-zzzzzzzzzzzz", "plan-zzzzzzzzzzzz"},       // non-hex chars: not stripped
		{"plan-aabb1122ccdd-ee", "plan-aabb1122ccdd-ee"}, // trailing isn't 12 hex
	}
	for _, tc := range cases {
		got := DisplayName(tc.in)
		if got != tc.want {
			t.Errorf("DisplayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncateSnippetBoundary(t *testing.T) {
	exact := strings.Repeat("a", snippetMaxLen)
	if got := truncateSnippet(exact); got != exact {
		t.Errorf("truncateSnippet(exact length) = %q, want %q", got, exact)
	}

	over := exact + "b"
	want := exact + "…"
	if got := truncateSnippet(over); got != want {
		t.Errorf("truncateSnippet(over length) = %q, want %q", got, want)
	}
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
	info1, err := os.Stat(sidecar)
	if err != nil {
		t.Fatalf("stat sidecar: %v", err)
	}

	// Sleep just past filesystem mtime granularity (some FS have 1s
	// resolution); a rewrite would update mtime, no-op should not.
	time.Sleep(20 * time.Millisecond)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh 2: %v", err)
	}
	info2, err := os.Stat(sidecar)
	if err != nil {
		t.Fatalf("stat sidecar again: %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("sidecar mtime changed: %v -> %v (expected no rewrite on cache hit)",
			info1.ModTime(), info2.ModTime())
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

func TestSaveLoadRoundtrip(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# alpha title\nmore",
		"session-1.md": "body text",
	})

	src := newTestIndex(t)
	if err := src.Refresh(root); err != nil {
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
	if got.SearchText != "" {
		t.Errorf("SearchText leaked into JSON: %q", got.SearchText)
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
