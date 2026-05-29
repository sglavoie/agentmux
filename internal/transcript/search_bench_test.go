package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

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
// avoiding snippet-preview hits (no needle in any Snippet). Per-entry body
// is ~22 KiB so the overall haystack approximates the dev machine's ~36 MiB
// on-disk index. Run with:
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

// BenchmarkSearch_SnippetPreviewWithSidecar exercises the path where every
// entry's Snippet already contains the needle. Search still reads sidecars
// so match counts stay exact, but the preview uses the original-case snippet.
func BenchmarkSearch_SnippetPreviewWithSidecar(b *testing.B) {
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
