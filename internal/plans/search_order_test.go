package plans

import (
	"fmt"
	"testing"
	"time"
)

func TestListOrdersPlansWithEqualTimestampsByPath(t *testing.T) {
	t.Parallel()
	idx := newTestIndex(t)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 31; i >= 0; i-- {
		path := fmt.Sprintf("/plans/%02d", i)
		idx.entries[path] = PlanMeta{DirPath: path, ModTime: stamp}
	}
	for attempt := 0; attempt < 8; attempt++ {
		for i, meta := range idx.List() {
			if want := fmt.Sprintf("/plans/%02d", i); meta.DirPath != want {
				t.Fatalf("List()[%d] = %q, want %q for tied timestamps", i, meta.DirPath, want)
			}
		}
	}
}

func TestSortResultsBreaksEqualRankTiesByPath(t *testing.T) {
	t.Parallel()
	for _, mode := range []SortMode{SortByMatches, SortByModTime} {
		results := []Result{
			{Meta: PlanMeta{DirPath: "/plans/b"}, Matches: 2},
			{Meta: PlanMeta{DirPath: "/plans/a"}, Matches: 2},
		}
		SortResults(results, mode)
		if results[0].Meta.DirPath != "/plans/a" {
			t.Errorf("SortResults mode %d retained input-dependent ordering: %+v", mode, results)
		}
	}
}
