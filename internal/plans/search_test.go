package plans

import (
	"os"
	"strings"
	"testing"
	"time"
)

// seedTwoPlans creates two plan dirs under a fresh temp root with
// distinct content, refreshes the index, and returns the populated
// index and the two plan dir paths in (planA, planB) order.
func seedTwoPlans(t *testing.T) (*Index, string, string) {
	t.Helper()
	root := t.TempDir()
	planA := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# Alpha plan title\nintroduction line",
		"session-1.md": "alpha session body talks about Foo and Bar",
	})
	planB := seedPlanDir(t, root, "beta-ccdd44556677", map[string]string{
		"manifest.md":  "# Beta plan title\nintroduction line",
		"session-1.md": "beta session body talks about widgets and gadgets",
	})

	// Force a distinct ModTime ordering so sort-by-ModTime is testable
	// across filesystems with coarse mtime granularity. planB is the
	// newer (most-recent-modified) plan.
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	if err := os.Chtimes(planA+"/manifest.md", older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(planA+"/session-1.md", older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(planB+"/manifest.md", newer, newer); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(planB+"/session-1.md", newer, newer); err != nil {
		t.Fatal(err)
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return idx, planA, planB
}

func TestSearch_EmptyQueryReturnsAllByModTimeDesc(t *testing.T) {
	idx, planA, planB := seedTwoPlans(t)

	results, err := Search(idx, Query{Text: ""})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Meta.DirPath != planB {
		t.Errorf("results[0] = %q, want planB %q (newer ModTime should sort first)",
			results[0].Meta.DirPath, planB)
	}
	if results[1].Meta.DirPath != planA {
		t.Errorf("results[1] = %q, want planA %q", results[1].Meta.DirPath, planA)
	}
	for i, r := range results {
		if r.Matches != 0 {
			t.Errorf("results[%d].Matches = %d, want 0 for empty query", i, r.Matches)
		}
		if r.Preview != r.Meta.Snippet {
			t.Errorf("results[%d].Preview = %q, want Meta.Snippet %q",
				i, r.Preview, r.Meta.Snippet)
		}
	}
}

func TestSearch_SnippetMatchSurvivesMissingSidecar(t *testing.T) {
	idx, planA, _ := seedTwoPlans(t)

	// "Alpha" is in planA's snippet (manifest.md first line) but not
	// in planB's. Deleting planA's sidecar means Search cannot count
	// the corpus exactly, but the snippet match should still surface
	// the plan.
	key := PlanKey(planA)
	if err := os.Remove(idx.store.Path(key)); err != nil {
		t.Fatalf("removing planA sidecar: %v", err)
	}

	results, err := Search(idx, Query{Text: "Alpha"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	got := results[0]
	if got.Meta.DirPath != planA {
		t.Errorf("Meta.DirPath = %q, want %q", got.Meta.DirPath, planA)
	}
	if got.Matches != 1 {
		t.Errorf("Matches = %d, want 1 (missing sidecar falls back to snippet match)", got.Matches)
	}
	if got.Preview != got.Meta.Snippet {
		t.Errorf("Preview = %q, want Meta.Snippet %q", got.Preview, got.Meta.Snippet)
	}
}

func TestSearch_SnippetHitStillCountsSidecar(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# Alpha overview",
		"session-1.md": "alpha detail alpha tail",
	})

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	results, err := Search(idx, Query{Text: "alpha"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	got := results[0]
	if got.Meta.DirPath != planDir {
		t.Errorf("Meta.DirPath = %q, want %q", got.Meta.DirPath, planDir)
	}
	if got.Matches != 3 {
		t.Errorf("Matches = %d, want 3 (sidecar should count snippet and body hits)", got.Matches)
	}
	if got.Preview != "# Alpha overview" {
		t.Errorf("Preview = %q, want original-case snippet", got.Preview)
	}
}

func TestSearch_BodyOnlyMatchUsesSidecar(t *testing.T) {
	idx, planA, _ := seedTwoPlans(t)

	// "widgets" appears only in planB's session-1.md body, not in
	// either plan's snippet. "Foo" appears only in planA's session-1.md
	// body. Pick "Foo" to drive the planA body-only path.
	results, err := Search(idx, Query{Text: "Foo"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	got := results[0]
	if got.Meta.DirPath != planA {
		t.Errorf("Meta.DirPath = %q, want %q", got.Meta.DirPath, planA)
	}
	if got.Matches != 1 {
		t.Errorf("Matches = %d, want 1 (one occurrence of 'foo' in body)", got.Matches)
	}
	// Sidecar is lowercased so the preview will be lowercased too.
	if !strings.Contains(got.Preview, "foo") {
		t.Errorf("Preview = %q, want it to contain 'foo'", got.Preview)
	}
	if !strings.Contains(got.Preview, "alpha session body") {
		t.Errorf("Preview = %q, want the matching session-1 line", got.Preview)
	}
}

func TestSearch_NeedleAbsentReturnsEmpty(t *testing.T) {
	idx, _, _ := seedTwoPlans(t)

	results, err := Search(idx, Query{Text: "nonexistent-needle-zzz"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}

func TestSearch_UnicodeAndMixedCase(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "uni-aabb112233cc", map[string]string{
		"manifest.md":  "# Plan title",
		"session-1.md": "résumé café ÜBER one two three RÉSUMÉ",
	})
	_ = planDir

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Mixed-case query against a body that contains both résumé and
	// RÉSUMÉ. The sidecar is lowercased and the needle is lowercased
	// before comparison, so we should match both occurrences.
	results, err := Search(idx, Query{Text: "Résumé"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Matches != 2 {
		t.Errorf("Matches = %d, want 2 (both résumé and RÉSUMÉ)", results[0].Matches)
	}
	if !strings.Contains(strings.ToLower(results[0].Preview), "résumé") {
		t.Errorf("Preview = %q, want it to contain 'résumé'", results[0].Preview)
	}
}

func TestSearch_MissingSidecarSkippedSilently(t *testing.T) {
	idx, planA, planB := seedTwoPlans(t)

	// Delete planA's sidecar. Searching for a body-only needle that
	// would only match planA must skip planA cleanly without erroring.
	// Use a needle in planB's body so we have a non-empty baseline to
	// confirm Search itself works.
	if err := os.Remove(idx.store.Path(PlanKey(planA))); err != nil {
		t.Fatalf("removing planA sidecar: %v", err)
	}

	// "Foo" is body-only in planA — with the sidecar gone, it should
	// be skipped (and there's no match in planB), so results are empty.
	results, err := Search(idx, Query{Text: "Foo"})
	if err != nil {
		t.Fatalf("Search after sidecar delete: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0 (planA sidecar missing, no planB match)", len(results))
	}

	// "widgets" is body-only in planB — its sidecar is intact, so the
	// search should still return planB.
	results, err = Search(idx, Query{Text: "widgets"})
	if err != nil {
		t.Fatalf("Search for planB body needle: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Meta.DirPath != planB {
		t.Errorf("Meta.DirPath = %q, want planB %q", results[0].Meta.DirPath, planB)
	}
}

func TestSearch_SortMatchesDescThenModTimeDesc(t *testing.T) {
	root := t.TempDir()
	// planA: needle "alpha" appears 3 times in body, none in snippet
	planA := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# Plan A",
		"session-1.md": "alpha alpha alpha extra text",
	})
	// planB: needle "alpha" appears once in body, none in snippet
	planB := seedPlanDir(t, root, "beta-ccdd44556677", map[string]string{
		"manifest.md":  "# Plan B",
		"session-1.md": "single alpha mention",
	})
	// planC: needle "alpha" appears once in body, none in snippet —
	// but its mtime is newer than planB's. With equal Matches, planC
	// should sort ahead of planB.
	planC := seedPlanDir(t, root, "gamma-eeff8899aabb", map[string]string{
		"manifest.md":  "# Plan C",
		"session-1.md": "another alpha here",
	})

	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	for _, f := range []string{"manifest.md", "session-1.md"} {
		if err := os.Chtimes(planB+"/"+f, older, older); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(planC+"/"+f, newer, newer); err != nil {
			t.Fatal(err)
		}
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	results, err := Search(idx, Query{Text: "alpha"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	if results[0].Meta.DirPath != planA || results[0].Matches != 3 {
		t.Errorf("results[0] = %s/%d, want %s/3", results[0].Meta.DirPath, results[0].Matches, planA)
	}
	if results[1].Meta.DirPath != planC || results[1].Matches != 1 {
		t.Errorf("results[1] = %s/%d, want %s/1 (newer mtime breaks tie)",
			results[1].Meta.DirPath, results[1].Matches, planC)
	}
	if results[2].Meta.DirPath != planB || results[2].Matches != 1 {
		t.Errorf("results[2] = %s/%d, want %s/1", results[2].Meta.DirPath, results[2].Matches, planB)
	}
}

func TestSortResults_ByMatches(t *testing.T) {
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	rs := []Result{
		{Meta: PlanMeta{DirPath: "low-newer", ModTime: newer}, Matches: 1},
		{Meta: PlanMeta{DirPath: "high-older", ModTime: older}, Matches: 5},
		{Meta: PlanMeta{DirPath: "low-older", ModTime: older}, Matches: 1},
	}
	SortResults(rs, SortByMatches)
	got := []string{rs[0].Meta.DirPath, rs[1].Meta.DirPath, rs[2].Meta.DirPath}
	want := []string{"high-older", "low-newer", "low-older"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("SortByMatches order[%d] = %q, want %q (full order %v)",
				i, got[i], want[i], got)
		}
	}
}

func TestSortResults_ByModTime(t *testing.T) {
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	rs := []Result{
		{Meta: PlanMeta{DirPath: "low-newer", ModTime: newer}, Matches: 1},
		{Meta: PlanMeta{DirPath: "high-older", ModTime: older}, Matches: 5},
		{Meta: PlanMeta{DirPath: "high-newer", ModTime: newer}, Matches: 5},
	}
	SortResults(rs, SortByModTime)
	got := []string{rs[0].Meta.DirPath, rs[1].Meta.DirPath, rs[2].Meta.DirPath}
	want := []string{"high-newer", "low-newer", "high-older"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("SortByModTime order[%d] = %q, want %q (full order %v)",
				i, got[i], want[i], got)
		}
	}
}

func TestSearch_QuerySortByModTime(t *testing.T) {
	root := t.TempDir()
	// planA: many matches, older mtime — would win under SortByMatches.
	planA := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# Plan A",
		"session-1.md": "alpha alpha alpha extra text",
	})
	// planB: one match, newest mtime — must win under SortByModTime.
	planB := seedPlanDir(t, root, "beta-ccdd44556677", map[string]string{
		"manifest.md":  "# Plan B",
		"session-1.md": "single alpha mention",
	})

	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	for _, f := range []string{"manifest.md", "session-1.md"} {
		if err := os.Chtimes(planA+"/"+f, older, older); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(planB+"/"+f, newer, newer); err != nil {
			t.Fatal(err)
		}
	}

	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	results, err := Search(idx, Query{Text: "alpha", Sort: SortByModTime})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Meta.DirPath != planB {
		t.Errorf("results[0] = %q, want planB %q (newer mtime should win regardless of match count)",
			results[0].Meta.DirPath, planB)
	}
	if results[1].Meta.DirPath != planA {
		t.Errorf("results[1] = %q, want planA %q", results[1].Meta.DirPath, planA)
	}
}

func TestSearch_NilIndexReturnsError(t *testing.T) {
	if _, err := Search(nil, Query{Text: "anything"}); err == nil {
		t.Errorf("Search(nil, ...) returned nil error; expected non-nil")
	}
}

func TestSearch_WhitespaceOnlyQueryTreatedAsEmpty(t *testing.T) {
	idx, _, _ := seedTwoPlans(t)

	results, err := Search(idx, Query{Text: "   \t  "})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("len(results) = %d, want 2 (whitespace-only query is empty)", len(results))
	}
	for i, r := range results {
		if r.Matches != 0 {
			t.Errorf("results[%d].Matches = %d, want 0", i, r.Matches)
		}
	}
}
