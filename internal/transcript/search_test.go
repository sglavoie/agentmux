package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

func TestSearch_EmptyTextReturnsFilteredMetadata(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude, Profile: "work", Project: "Foo",
		Snippet: "alpha", LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("codex:b", SessionMeta{
		ID: "b", Harness: harness.Codex, Profile: "work", Project: "Bar",
		Snippet: "beta", LastAt: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:c", SessionMeta{
		ID: "c", Harness: harness.Claude, Profile: "home", Project: "Foo",
		Snippet:         "gamma",
		LastAt:          time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		HiddenFromIndex: true,
	}))

	results, err := Search(idx, nil, Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len=%d, want 2 (hidden excluded)", len(results))
	}
	if results[0].Meta.ID != "b" {
		t.Errorf("results not sorted LastAt desc; first=%q", results[0].Meta.ID)
	}
	if results[0].Matches != 0 {
		t.Errorf("Matches=%d for text-less query, want 0", results[0].Matches)
	}
	if results[0].Preview != "beta" {
		t.Errorf("Preview=%q, want snippet fallback %q", results[0].Preview, "beta")
	}

	byHarness, err := Search(idx, nil, Query{Harness: []string{harness.Claude}})
	if err != nil {
		t.Fatalf("Search by harness: %v", err)
	}
	if len(byHarness) != 1 || byHarness[0].Meta.ID != "a" {
		t.Errorf("harness filter returned %+v", byHarness)
	}
}

func TestSearch_TextFiltersAndCountsMatches(t *testing.T) {
	idx := newTestIndex(t)
	hitKey := "claude:hit"
	must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
		ID: "hit", Harness: harness.Claude, Path: "/tmp/hit.jsonl",
		LastAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:miss", SessionMeta{
		ID: "miss", Harness: harness.Claude, Path: "/tmp/miss.jsonl",
		LastAt: time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
	}))

	readers := map[string]Reader{
		harness.Claude: &fakeReader{
			harness:    harness.Claude,
			readResult: nil, // overridden below per-call via discover/readResult swap
		},
	}

	// Drive different ReadMessages results per session path using a
	// dispatcher reader.
	disp := &dispatchReader{
		harness: harness.Claude,
		byPath: map[string][]Message{
			"/tmp/hit.jsonl": {
				{Role: "user", Text: "first line\nsearchTerm shows up here\nlast"},
				{Role: "assistant", Text: "talked about SearchTerm again"},
			},
			"/tmp/miss.jsonl": {
				{Role: "user", Text: "nothing relevant"},
			},
		},
	}
	readers[harness.Claude] = disp

	results, err := Search(idx, readers, Query{Text: "searchterm"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len=%d, want 1 (only hit session)", len(results))
	}
	r := results[0]
	if r.Meta.ID != "hit" {
		t.Errorf("ID=%q, want hit", r.Meta.ID)
	}
	if r.Matches != 2 {
		t.Errorf("Matches=%d, want 2 (case-insensitive)", r.Matches)
	}
	if !strings.Contains(r.Preview, "searchTerm") {
		t.Errorf("Preview=%q does not contain match line", r.Preview)
	}
	if strings.Contains(r.Preview, "first line") {
		t.Errorf("Preview leaked prior line: %q", r.Preview)
	}
	repaired, err := idx.ReadSearchText(hitKey)
	if err != nil {
		t.Fatalf("ReadSearchText after fallback: %v", err)
	}
	if repaired != "first line\nsearchterm shows up here\nlast\ntalked about searchterm again" {
		t.Errorf("repaired sidecar = %q", repaired)
	}
}

func TestSearch_NoReaderMeansSessionDropped(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude, Path: "/tmp/a.jsonl",
		LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}))
	// readers map exists but has no claude entry
	results, err := Search(idx, map[string]Reader{
		harness.Codex: &fakeReader{harness: harness.Codex},
	}, Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results when reader missing, got %d", len(results))
	}
}

func TestSearch_ReadErrorDropsSession(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude, Path: "/tmp/a.jsonl",
		LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}))
	readers := map[string]Reader{
		harness.Claude: &fakeReader{
			harness: harness.Claude,
			readErr: errors.New("boom"),
		},
	}
	results, err := Search(idx, readers, Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results on read error, got %d", len(results))
	}
}

func TestSearch_ResultsSortedByLastAtThenStartedAt(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:older", SessionMeta{
		ID: "older", Harness: harness.Claude,
		StartedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LastAt:    time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:newer", SessionMeta{
		ID: "newer", Harness: harness.Claude,
		StartedAt: time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		LastAt:    time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:freshest", SessionMeta{
		ID: "freshest", Harness: harness.Claude,
		StartedAt: time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
		LastAt:    time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC),
	}))

	results, err := Search(idx, nil, Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len=%d, want 3", len(results))
	}
	wantOrder := []string{"freshest", "newer", "older"}
	for i, want := range wantOrder {
		if results[i].Meta.ID != want {
			t.Errorf("results[%d]=%q, want %q", i, results[i].Meta.ID, want)
		}
	}
}

func TestSearch_NilIndexReturnsError(t *testing.T) {
	if _, err := Search(nil, nil, Query{}); err == nil {
		t.Errorf("expected error for nil index")
	}
}

// TestSearch_FastPathUsesSidecar locks in the metadata-only search path:
// Search must filter and count matches against per-session sidecars instead
// of SessionMeta.SearchText. The readers map is intentionally nil because
// sidecars make readers optional when every survivor has one.
func TestSearch_FastPathUsesSidecar(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
		ID: "hit", Harness: harness.Claude, Path: "/tmp/hit.jsonl",
		MessageCount: 2,
		LastAt:       time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.WriteSearchText("claude:hit", "first line\nsearchterm shows up here\nlast\ntalked about searchterm again"))
	must(t, idx.SetEntryForTest("claude:miss", SessionMeta{
		ID: "miss", Harness: harness.Claude, Path: "/tmp/miss.jsonl",
		MessageCount: 1,
		LastAt:       time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.WriteSearchText("claude:miss", "nothing relevant"))

	results, err := Search(idx, nil, Query{Text: "searchterm"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len=%d, want 1", len(results))
	}
	r := results[0]
	if r.Meta.ID != "hit" {
		t.Errorf("ID=%q, want hit", r.Meta.ID)
	}
	if r.Matches != 2 {
		t.Errorf("Matches=%d, want 2", r.Matches)
	}
	if !strings.Contains(r.Preview, "searchterm") {
		t.Errorf("Preview=%q does not contain match line", r.Preview)
	}
	if strings.Contains(r.Preview, "first line") {
		t.Errorf("Preview leaked the prior line: %q", r.Preview)
	}
}

// TestSearch_FastPathSkipsReader proves the sidecar fast path never reaches
// Reader.ReadMessages. A reader that errors on every call is supplied; if
// Search were still routing through fallback the result would drop to zero.
func TestSearch_FastPathSkipsReader(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
		ID: "hit", Harness: harness.Claude, Path: "/tmp/hit.jsonl",
		MessageCount: 1,
		LastAt:       time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.WriteSearchText("claude:hit", "needle in haystack"))
	readers := map[string]Reader{
		harness.Claude: &fakeReader{
			harness: harness.Claude,
			readErr: errors.New("ReadMessages must not be called on the fast path"),
		},
	}
	results, err := Search(idx, readers, Query{Text: "needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Meta.ID != "hit" {
		t.Fatalf("fast path bypassed; results=%+v", results)
	}
	if results[0].Matches != 1 {
		t.Errorf("Matches=%d, want 1", results[0].Matches)
	}
}

// TestSearch_SnippetShortCircuits covers the snippet short-circuit added in
// session 4: when the needle appears in meta.Snippet the result emits
// Matches=1 with Preview=snippet, bypassing the sidecar scan entirely.
// The session's body still contains the needle three times — proof of the
// match-count trade-off the spec accepts in exchange for a constant-time
// lookup against the 160-rune snippet.
func TestSearch_SnippetShortCircuits(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
		ID: "hit", Harness: harness.Claude,
		MessageCount: 2,
		Snippet:      "Quick NEEDLE intro from the user",
		LastAt:       time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.WriteSearchText("claude:hit", "quick needle intro from the user\nneedle appears again\nand once more needle"))
	results, err := Search(idx, nil, Query{Text: "needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len=%d, want 1", len(results))
	}
	r := results[0]
	if r.Matches != 1 {
		t.Errorf("Matches=%d, want 1 (snippet short-circuit)", r.Matches)
	}
	if r.Preview != "Quick NEEDLE intro from the user" {
		t.Errorf("Preview=%q, want snippet verbatim", r.Preview)
	}
}

// TestSearch_SnippetMissFallsThroughToSidecar guards against the
// short-circuit accidentally short-circuiting too aggressively: when the
// needle is absent from the snippet the sidecar path must still run and
// produce the full match count.
func TestSearch_SnippetMissFallsThroughToSidecar(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:hit", SessionMeta{
		ID: "hit", Harness: harness.Claude,
		MessageCount: 2,
		Snippet:      "harmless intro line",
		LastAt:       time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.WriteSearchText("claude:hit", "harmless intro line\nneedle hidden in body\nneedle again"))
	results, err := Search(idx, nil, Query{Text: "needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len=%d, want 1", len(results))
	}
	if results[0].Matches != 2 {
		t.Errorf("Matches=%d, want 2 (sidecar counted both body hits)", results[0].Matches)
	}
}

// populateBenchIndex builds an in-memory *Index with n entries without
// going through SetEntryForTest — that helper writes the whole index to
// disk after every insert, which is O(N²) and would dominate the
// benchmark's wall-clock time.
func populateBenchIndex(b *testing.B, n int, snippet, body string) *Index {
	b.Helper()
	idx := &Index{path: filepath.Join(b.TempDir(), "transcripts.json"), entries: make(map[string]SessionMeta, n)}
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("claude:bench-%d", i)
		idx.entries[key] = SessionMeta{
			ID:           fmt.Sprintf("bench-%d", i),
			Harness:      harness.Claude,
			Profile:      "work",
			Project:      "Foo",
			MessageCount: 50,
			Snippet:      snippet,
			LastAt:       base.Add(time.Duration(i) * time.Minute),
		}
		if err := idx.WriteSearchText(key, body); err != nil {
			b.Fatalf("WriteSearchText: %v", err)
		}
	}
	return idx
}

// BenchmarkSearch_FastPath establishes a baseline for transcript.Search over
// a synthetic corpus of N=4500 sessions. Each entry carries a populated
// search-text sidecar so the benchmark exercises the fast path while
// avoiding the snippet short-circuit (no needle in any Snippet). Per-entry
// body is ~22 KiB so the overall haystack approximates the dev machine's
// ~36 MiB on-disk index. Run with:
//
//	go test ./internal/transcript -run=^$ -bench=BenchmarkSearch_FastPath
func BenchmarkSearch_FastPath(b *testing.B) {
	body := strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit\n", 200) +
		"alpha needle beta\n" +
		strings.Repeat("ut enim ad minim veniam quis nostrud exercitation\n", 200)
	idx := populateBenchIndex(b, 4500, "a synthetic transcript with lorem ipsum content", body)
	q := Query{Text: "needle"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := Search(idx, nil, q)
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		if len(results) != 4500 {
			b.Fatalf("results=%d, want 4500", len(results))
		}
	}
}

// BenchmarkSearch_Sidecar is the explicitly named sidecar benchmark requested
// by the storage migration plan. It mirrors BenchmarkSearch_FastPath so
// benchmark dashboards can track the user-visible storage contract directly.
func BenchmarkSearch_Sidecar(b *testing.B) {
	body := strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit\n", 200) +
		"alpha needle beta\n" +
		strings.Repeat("ut enim ad minim veniam quis nostrud exercitation\n", 200)
	idx := populateBenchIndex(b, 4500, "a synthetic transcript with lorem ipsum content", body)
	q := Query{Text: "needle"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := Search(idx, nil, q)
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		if len(results) != 4500 {
			b.Fatalf("results=%d, want 4500", len(results))
		}
	}
}

// BenchmarkSearch_SnippetShortCircuit exercises the path where every entry's
// Snippet already contains the needle. The full sidecar is never touched
// for any of the 4500 entries, so the Search call collapses to a sequence
// of cheap strings.Contains calls against 160-rune snippets. Demonstrates
// the throughput ceiling for the most common interactive case (user types
// a fragment that already appears in the kick-off prompt).
func BenchmarkSearch_SnippetShortCircuit(b *testing.B) {
	body := strings.Repeat("filler text without the needle\n", 400)
	idx := populateBenchIndex(b, 4500, "user asks about the needle in the kick-off prompt", body)
	q := Query{Text: "needle"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := Search(idx, nil, q)
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		if len(results) != 4500 {
			b.Fatalf("results=%d, want 4500", len(results))
		}
	}
}

func writeBenchIndexFile(b *testing.B, path string, n int, includeLegacySearchText bool) {
	b.Helper()
	entries := make(map[string]SessionMeta, n)
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	body := strings.Repeat("legacy transcript body line with searchable text\n", 200)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("claude:bench-%d", i)
		meta := SessionMeta{
			ID:           fmt.Sprintf("bench-%d", i),
			Harness:      harness.Claude,
			Profile:      "work",
			Project:      "Foo",
			MessageCount: 50,
			Snippet:      "synthetic metadata-only snippet",
			LastAt:       base.Add(time.Duration(i) * time.Minute),
		}
		if includeLegacySearchText {
			meta.SearchText = body
		}
		entries[key] = meta
	}
	data, err := json.Marshal(indexFile{Entries: entries})
	if err != nil {
		b.Fatalf("Marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
}

func BenchmarkIndexLoad_MetadataOnly(b *testing.B) {
	path := filepath.Join(b.TempDir(), "transcripts.json")
	writeBenchIndexFile(b, path, 4500, false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx, err := LoadIndexAt(path)
		if err != nil {
			b.Fatalf("LoadIndexAt: %v", err)
		}
		if idx.Len() != 4500 {
			b.Fatalf("Len=%d, want 4500", idx.Len())
		}
	}
}

func BenchmarkIndexLoad_LegacyMigration(b *testing.B) {
	sourcePath := filepath.Join(b.TempDir(), "legacy-source.json")
	writeBenchIndexFile(b, sourcePath, 4500, true)
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		b.Fatalf("ReadFile: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := b.TempDir()
		path := filepath.Join(dir, "transcripts.json")
		if err := os.WriteFile(path, source, 0o644); err != nil {
			b.Fatalf("WriteFile: %v", err)
		}
		b.StartTimer()
		idx, err := LoadIndexAt(path)
		if err != nil {
			b.Fatalf("LoadIndexAt: %v", err)
		}
		if idx.Len() != 4500 {
			b.Fatalf("Len=%d, want 4500", idx.Len())
		}
	}
}

// dispatchReader returns canned messages keyed by path. Used to drive
// per-session text differences in the same Search call.
type dispatchReader struct {
	harness string
	byPath  map[string][]Message
}

func (d *dispatchReader) Harness() string { return d.harness }
func (d *dispatchReader) Discover(string, DiscoverOptions) ([]SessionMeta, error) {
	return nil, nil
}
func (d *dispatchReader) ReadMessages(path string) ([]Message, error) {
	return d.byPath[path], nil
}
