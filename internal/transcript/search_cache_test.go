package transcript

import (
	"fmt"
	"strings"
	"testing"
)

func TestSearchCacheInvalidatesOnSidecarWrite(t *testing.T) {
	idx := newTestIndex(t)
	key := "claude:cached"
	must(t, idx.SetEntryForTest(key, SessionMeta{ID: "cached", Harness: "claude", Path: "cached.jsonl"}))
	first := encodeSourceSidecar(sourceSidecar{Version: 2, Any: "old user\nold harness", User: "old user", Harness: "old harness"})
	must(t, idx.WriteSearchText(key, first))
	result, err := Search(idx, nil, Query{Text: "old", MessageSource: "user"})
	if err != nil || len(result) != 1 {
		t.Fatalf("initial search: %+v, %v", result, err)
	}
	if len(idx.searchCache.entries) != 1 {
		t.Fatal("search did not populate cache")
	}

	second := encodeSourceSidecar(sourceSidecar{Version: 2, Any: "new user\nnew harness", User: "new user", Harness: "new harness"})
	must(t, idx.WriteSearchText(key, second))
	if len(idx.searchCache.entries) != 0 {
		t.Fatal("sidecar write did not invalidate cache")
	}
	result, err = Search(idx, nil, Query{Text: "old", MessageSource: "user"})
	if err != nil || len(result) != 0 {
		t.Fatalf("stale search: %+v, %v", result, err)
	}
	result, err = Search(idx, nil, Query{Text: "new", MessageSource: "harness"})
	if err != nil || len(result) != 1 || !strings.Contains(result[0].Preview, "new") {
		t.Fatalf("updated search: %+v, %v", result, err)
	}
}

func TestSearchParallelLegacyUpgradesKeepResultOrder(t *testing.T) {
	idx := newTestIndex(t)
	reader := &dispatchReader{harness: "claude", byPath: make(map[string][]Message)}
	for i := 7; i >= 0; i-- {
		id := fmt.Sprintf("%02d", i)
		path := "session-" + id
		reader.byPath[path] = []Message{{Role: "user", Text: "needle " + id}}
		key := IndexKey("claude", id)
		must(t, idx.SetEntryForTest(key, SessionMeta{ID: id, Harness: "claude", Path: path}))
		must(t, idx.WriteSearchText(key, "needle "+id))
	}
	results, err := Search(idx, map[string]Reader{"claude": reader}, Query{Text: "needle", MessageSource: "user"})
	if err != nil || len(results) != 8 {
		t.Fatalf("parallel results=%+v, err=%v", results, err)
	}
	for i, result := range results {
		if result.Meta.ID != fmt.Sprintf("%02d", i) {
			t.Fatalf("result %d has ID %q", i, result.Meta.ID)
		}
	}
}
