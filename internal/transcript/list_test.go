package transcript

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
)

func TestList_FiltersAndSort(t *testing.T) {
	idx := newTestIndex(t)

	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude, Profile: "work", Project: "Foo",
		LastAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("codex:b", SessionMeta{
		ID: "b", Harness: harness.Codex, Profile: "work", Project: "Bar",
		LastAt: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
	}))
	must(t, idx.SetEntryForTest("claude:c", SessionMeta{
		ID: "c", Harness: harness.Claude, Profile: "home", Project: "Foo",
		LastAt:          time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		HiddenFromIndex: true,
	}))

	all := idx.List(ListFilter{})
	if len(all) != 2 {
		t.Errorf("default List len = %d, want 2 (hidden excluded)", len(all))
	}
	if len(all) > 0 && all[0].ID != "b" {
		t.Errorf("List not sorted by LastAt desc: first = %q", all[0].ID)
	}

	byHarness := idx.List(ListFilter{Harness: []string{harness.Claude}})
	if len(byHarness) != 1 || byHarness[0].ID != "a" {
		t.Errorf("Harness filter returned %+v", byHarness)
	}

	withHidden := idx.List(ListFilter{IncludeHidden: true})
	if len(withHidden) != 3 {
		t.Errorf("IncludeHidden=true len = %d, want 3", len(withHidden))
	}

	byProfile := idx.List(ListFilter{Profile: []string{"home"}, IncludeHidden: true})
	if len(byProfile) != 1 || byProfile[0].ID != "c" {
		t.Errorf("Profile filter returned %+v", byProfile)
	}
}
