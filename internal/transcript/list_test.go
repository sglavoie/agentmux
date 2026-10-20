package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestList_FiltersAndSort(t *testing.T) {
	t.Parallel()

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

func TestList_ProfileFilterMatchesAllCandidateProfiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "transcripts.json")
	data := []byte(`{
		"entries": {
			"claude:shared": {
				"id": "shared",
				"harness": "claude",
				"profile": "work",
				"profiles": ["work", "personal"],
				"project": "Foo",
				"snippet": "shared home session"
			}
		}
	}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	idx, err := LoadIndexAt(path)
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}

	for _, profile := range []string{"work", "personal"} {
		got := idx.List(ListFilter{Profile: []string{profile}})
		if len(got) != 1 || got[0].ID != "shared" {
			t.Fatalf("Profile filter %q returned %+v, want shared session", profile, got)
		}
	}
}

func TestSessionMeta_ProfileMembershipHelpers(t *testing.T) {
	meta := SessionMeta{
		Profile:  "work",
		Profiles: []string{"work", "personal", "work", ""},
	}
	if got := meta.DisplayProfile(); got != "work" {
		t.Fatalf("DisplayProfile = %q, want work", got)
	}
	gotProfiles := meta.ProfileNames()
	wantProfiles := []string{"work", "personal"}
	if len(gotProfiles) != len(wantProfiles) {
		t.Fatalf("ProfileNames len = %d, want %d: %#v", len(gotProfiles), len(wantProfiles), gotProfiles)
	}
	for i := range wantProfiles {
		if gotProfiles[i] != wantProfiles[i] {
			t.Fatalf("ProfileNames[%d] = %q, want %q; full=%#v", i, gotProfiles[i], wantProfiles[i], gotProfiles)
		}
	}
	if !meta.MatchesAnyProfile([]string{"personal"}) {
		t.Fatalf("MatchesAnyProfile did not match Profiles membership")
	}
	if meta.MatchesAnyProfile([]string{"ghost"}) {
		t.Fatalf("MatchesAnyProfile matched unrelated profile")
	}

	legacy := SessionMeta{Profile: "legacy"}
	if !legacy.MatchesAnyProfile([]string{"legacy"}) {
		t.Fatalf("legacy profile fallback did not match Profile")
	}
	if got := (SessionMeta{Profiles: []string{"fallback"}}).DisplayProfile(); got != "fallback" {
		t.Fatalf("DisplayProfile without Profile = %q, want fallback", got)
	}
}

func TestSessionMeta_ProjectMembershipHelpers(t *testing.T) {
	meta := SessionMeta{
		Project:  "SS",
		Projects: []string{"SS", "streamutil", "SS", ""},
	}
	gotProjects := meta.ProjectNames()
	wantProjects := []string{"SS", "streamutil"}
	if len(gotProjects) != len(wantProjects) {
		t.Fatalf("ProjectNames len = %d, want %d: %#v", len(gotProjects), len(wantProjects), gotProjects)
	}
	for i := range wantProjects {
		if gotProjects[i] != wantProjects[i] {
			t.Fatalf("ProjectNames[%d] = %q, want %q; full=%#v", i, gotProjects[i], wantProjects[i], gotProjects)
		}
	}
	if !meta.MatchesAnyProject(nil) {
		t.Fatal("MatchesAnyProject with no requested projects should be unfiltered")
	}
	if !meta.MatchesAnyProject([]string{"streamutil"}) {
		t.Fatal("MatchesAnyProject did not match Projects membership")
	}
	if meta.MatchesAnyProject([]string{"unrelated"}) {
		t.Fatal("MatchesAnyProject matched unrelated project")
	}

	legacy := SessionMeta{Project: "legacy"}
	if got := legacy.ProjectNames(); len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("legacy ProjectNames = %#v, want [legacy]", got)
	}
	if !legacy.MatchesAnyProject([]string{"legacy"}) {
		t.Fatal("legacy project fallback did not match Project")
	}
}

func TestDisplayProfileForGroupPrefersProjectDefaultWithinGroup(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Projects: []config.Session{{
			Title:   "Foo",
			Profile: "personal",
		}},
	}

	if got := displayProfileForGroup(cfg, "Foo", "", []string{"work", "personal"}); got != "personal" {
		t.Fatalf("displayProfileForGroup project default = %q, want personal", got)
	}
	if got := displayProfileForGroup(cfg, "Foo", "", []string{"work"}); got != "work" {
		t.Fatalf("displayProfileForGroup outside group = %q, want work", got)
	}
	if got := displayProfileForGroup(cfg, "Other", "", []string{"work", "personal"}); got != "work" {
		t.Fatalf("displayProfileForGroup no project default = %q, want work", got)
	}
	if got := displayProfileForGroup(cfg, "Foo", "work", []string{"work", "personal"}); got != "work" {
		t.Fatalf("displayProfileForGroup matched profile = %q, want work", got)
	}
}

func TestList_SortTieBreakersAreDeterministic(t *testing.T) {
	t.Parallel()

	idx := newTestIndex(t)
	lastAt := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)

	must(t, idx.SetEntryForTest("codex:b", SessionMeta{
		ID: "b", Harness: harness.Codex,
		StartedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LastAt:    lastAt,
	}))
	must(t, idx.SetEntryForTest("claude:c", SessionMeta{
		ID: "c", Harness: harness.Claude,
		StartedAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		LastAt:    lastAt,
	}))
	must(t, idx.SetEntryForTest("claude:a", SessionMeta{
		ID: "a", Harness: harness.Claude,
		StartedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LastAt:    lastAt,
	}))

	got := idx.List(ListFilter{})
	want := []string{"claude:c", "claude:a", "codex:b"}
	if len(got) != len(want) {
		t.Fatalf("List len = %d, want %d: %+v", len(got), len(want), got)
	}
	for n, meta := range got {
		key := IndexKey(meta.Harness, meta.ID)
		if key != want[n] {
			t.Fatalf("List[%d] = %q, want %q; full list=%+v", n, key, want[n], got)
		}
	}
}
