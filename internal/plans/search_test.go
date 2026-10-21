package plans

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/searchquery"
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

func TestSearch_MultiWordQueryRequiresEveryWord(t *testing.T) {
	t.Parallel()

	idx, planA, _ := seedTwoPlans(t)

	results, err := Search(idx, Query{Text: "bar foo"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Meta.DirPath != planA {
		t.Fatalf("results = %+v, want only the alpha plan (holds both words)", results)
	}

	mixed, err := Search(idx, Query{Text: "foo widgets"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(mixed) != 0 {
		t.Fatalf("words split across two plans matched: %+v", mixed)
	}
}

func TestSearch_EmptyQueryReturnsAllByModTimeDesc(t *testing.T) {
	t.Parallel()

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

func TestSearch_ProjectFiltersMatchAliasesAndUnknown(t *testing.T) {
	t.Parallel()

	idx := newTestIndex(t)
	idx.entries = map[string]PlanMeta{
		"known": {
			Name: "known", Project: "Primary",
			Projects: []string{"Primary", "Alias"},
		},
		"unknown": {Name: "unknown"},
	}

	known, err := Search(idx, Query{Project: ProjectFilter{Kind: ProjectNamed, Name: "Alias"}})
	if err != nil || len(known) != 1 || known[0].Meta.Name != "known" {
		t.Fatalf("alias filter = %#v, %v", known, err)
	}
	unknown, err := Search(idx, Query{Project: ProjectFilter{Kind: ProjectUnknown}})
	if err != nil || len(unknown) != 1 || unknown[0].Meta.Name != "unknown" {
		t.Fatalf("unknown filter = %#v, %v", unknown, err)
	}
}

func TestSearch_SnippetMatchSurvivesMissingSidecar(t *testing.T) {
	t.Parallel()

	idx, planA, _ := seedTwoPlans(t)

	// "Alpha" is in planA's snippet (manifest.md first line) but not
	// in planB's. Deleting planA's sidecar makes the source files the
	// authoritative fallback; Search should return the exact count and
	// repair the disposable sidecar.
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
	if got.Matches != 2 {
		t.Errorf("Matches = %d, want 2 after source fallback", got.Matches)
	}
	if got.Preview != got.Meta.Snippet {
		t.Errorf("Preview = %q, want Meta.Snippet %q", got.Preview, got.Meta.Snippet)
	}
	if _, err := os.Stat(idx.store.Path(key)); err != nil {
		t.Errorf("missing sidecar was not repaired: %v", err)
	}
}

func TestSearch_SnippetHitStillCountsSidecar(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	// The match lies 31 characters into its line, so the preview enters
	// the line just before it.
	if got.Preview != "…body talks about foo and bar" {
		t.Errorf("Preview = %q, want the matching session-1 line", got.Preview)
	}
}

func TestSearch_NeedleAbsentReturnsEmpty(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

func TestSearch_MissingSidecarReadsSourceAndRepairs(t *testing.T) {
	t.Parallel()

	idx, planA, planB := seedTwoPlans(t)

	// Delete planA's sidecar. A body-only match must be recovered from
	// the authoritative Markdown source without affecting planB.
	if err := os.Remove(idx.store.Path(PlanKey(planA))); err != nil {
		t.Fatalf("removing planA sidecar: %v", err)
	}

	// "Foo" is body-only in planA, so source fallback should find it.
	results, err := Search(idx, Query{Text: "Foo"})
	if err != nil {
		t.Fatalf("Search after sidecar delete: %v", err)
	}
	if len(results) != 1 || results[0].Meta.DirPath != planA {
		t.Fatalf("source fallback results = %+v, want planA", results)
	}
	if _, err := os.Stat(idx.store.Path(PlanKey(planA))); err != nil {
		t.Errorf("missing sidecar was not repaired: %v", err)
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	if _, err := Search(nil, Query{Text: "anything"}); err == nil {
		t.Errorf("Search(nil, ...) returned nil error; expected non-nil")
	}
}

func TestSearch_WhitespaceOnlyQueryTreatedAsEmpty(t *testing.T) {
	t.Parallel()

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

// A plan line matched deep inside is previewed from just before the match, the
// way transcript search previews are, so a narrow list still shows the match.
func TestPlanSearchPreviewEntersALongLineNearTheMatch(t *testing.T) {
	t.Parallel()

	text := "# plan\nthe daemon renderer keeps a single writer and the companion hub debounces its publishes\n"
	count, preview := countMatchesInSearchText(text, searchquery.Parse("debounces"))
	if count != 1 || preview != "…and the companion hub debounces its publishes" {
		t.Fatalf("count = %d, preview = %q; want the line entered near the match", count, preview)
	}
}

// A multi-word query previews the line that holds every word, not the first
// line that happens to hold one of them.
func TestPlanSearchPreviewPrefersALineHoldingEveryTerm(t *testing.T) {
	t.Parallel()

	text := "# plan\nthe daemon owns the renderer\nthe socket is private\nthe daemon socket is 0600\n"
	count, preview := countMatchesInSearchText(text, searchquery.Parse("socket daemon"))
	if count != 4 || preview != "the daemon socket is 0600" {
		t.Fatalf("count = %d, preview = %q; want the line holding both words", count, preview)
	}
}
