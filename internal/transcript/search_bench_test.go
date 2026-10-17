package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// populateBenchIndex builds an in-memory *Index with n entries without
// going through SetEntryForTest — that helper writes the whole index to
// disk after every insert, which is O(N²) and would dominate the
// benchmark's wall-clock time.
func populateBenchIndex(b *testing.B, n int, snippet, body string) *Index {
	b.Helper()
	idx, err := LoadIndexAt(filepath.Join(b.TempDir(), "transcripts.json"))
	if err != nil {
		b.Fatalf("LoadIndexAt: %v", err)
	}
	idx.entries = make(map[string]SessionMeta, n)
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

func BenchmarkRefresh_UnchangedFilesFastPath(b *testing.B) {
	dir := b.TempDir()
	idx, err := LoadIndexAt(filepath.Join(dir, "transcripts.json"))
	if err != nil {
		b.Fatalf("LoadIndexAt: %v", err)
	}

	const n = 4500
	idx.entries = make(map[string]SessionMeta, n)
	metas := make([]SessionMeta, 0, n)
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		path := filepath.Join(dir, "home", "projects", "-tmp-bench", fmt.Sprintf("sess-%d.jsonl", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			b.Fatalf("WriteFile: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			b.Fatalf("Stat: %v", err)
		}
		meta := SessionMeta{
			ID:           fmt.Sprintf("bench-%d", i),
			Harness:      harness.Claude,
			Profile:      "work",
			Project:      "Bench",
			CWD:          "/tmp/bench",
			Path:         path,
			FileSize:     info.Size(),
			FileModTime:  info.ModTime(),
			MessageCount: 2,
			Snippet:      "cached bench transcript",
			StartedAt:    base.Add(time.Duration(i) * time.Minute),
			LastAt:       base.Add(time.Duration(i) * time.Minute),
		}
		key := IndexKey(meta.Harness, meta.ID)
		idx.entries[key] = meta
		metas = append(metas, meta)
		if err := idx.WriteSearchText(key, "cached bench transcript body"); err != nil {
			b.Fatalf("WriteSearchText: %v", err)
		}
	}

	cfg := &config.Config{
		Profiles: []config.Account{{
			Name:    "work",
			Harness: harness.Claude,
			HomeDir: filepath.Join(dir, "home"),
		}},
	}
	reader := &fakeReader{
		harness: harness.Claude,
		discover: func(_ string, opts DiscoverOptions) ([]SessionMeta, error) {
			out := make([]SessionMeta, 0, len(metas))
			for _, meta := range metas {
				if opts.Cached != nil {
					if cached, ok := opts.Cached(meta.Path, meta.FileSize, meta.FileModTime); ok {
						out = append(out, cached)
						continue
					}
				}
				fresh := meta
				fresh.SearchText = "fresh bench transcript body"
				out = append(out, fresh)
			}
			return out, nil
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := idx.Refresh(cfg, map[string]Reader{harness.Claude: reader}); err != nil {
			b.Fatalf("Refresh: %v", err)
		}
		if idx.Len() != n {
			b.Fatalf("Len=%d, want %d", idx.Len(), n)
		}
	}
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

// Same warm 10,000-conversation archive, with and without exact scope. This
// measures the work a debounced native history search asks the backend to do.
func BenchmarkHistoryArchiveScope(b *testing.B) {
	body := strings.Repeat("ordinary transcript text\n", 100) + "needle phrase\n"
	idx := populateBenchIndex(b, 10000, "conversation", body)
	for _, scope := range []string{"", "claude:bench-5000"} {
		name := "all"
		if scope != "" {
			name = "conversation"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				results, err := Search(idx, nil, Query{Text: "needle", Conversation: scope})
				if err != nil || len(results) == 0 {
					b.Fatalf("results=%d error=%v", len(results), err)
				}
			}
		})
	}
}
