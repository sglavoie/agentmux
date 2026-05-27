package transcript

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

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
