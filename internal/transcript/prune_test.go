package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestPrune_MarksOldEntriesHidden(t *testing.T) {
	idx := newTestIndex(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Fresh: 1 day old → keep.
	must(t, idx.SetEntryForTest("claude:fresh", SessionMeta{
		ID: "fresh", Harness: harness.Claude,
		LastAt: now.Add(-24 * time.Hour),
	}))
	// Old: 40 days old → prune (keep_days = 30).
	must(t, idx.SetEntryForTest("claude:old", SessionMeta{
		ID: "old", Harness: harness.Claude,
		LastAt: now.Add(-40 * 24 * time.Hour),
	}))
	// Already hidden: stays hidden, not counted as a flip.
	must(t, idx.SetEntryForTest("claude:gone", SessionMeta{
		ID: "gone", Harness: harness.Claude,
		LastAt:          now.Add(-100 * 24 * time.Hour),
		HiddenFromIndex: true,
	}))

	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 30

	hidden, err := Prune(idx, cfg, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if hidden != 1 {
		t.Errorf("hidden=%d, want 1", hidden)
	}

	fresh, _ := idx.Get("claude:fresh")
	if fresh.HiddenFromIndex {
		t.Errorf("fresh entry hidden by Prune")
	}
	old, _ := idx.Get("claude:old")
	if !old.HiddenFromIndex {
		t.Errorf("old entry not hidden by Prune")
	}
}

func TestPrune_DoesNotTouchFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	idx := newTestIndex(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	must(t, idx.SetEntryForTest("claude:old", SessionMeta{
		ID: "old", Harness: harness.Claude, Path: path,
		LastAt: now.Add(-365 * 24 * time.Hour),
	}))

	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 30

	if _, err := Prune(idx, cfg, now); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("transcript file removed by Prune: %v", err)
	}
}

func TestPrune_RemovesSidecarsForNewlyHiddenEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	idx := newTestIndex(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	key := "claude:old"
	must(t, idx.SetEntryForTest(key, SessionMeta{
		ID: "old", Harness: harness.Claude, Path: path,
		LastAt:       now.Add(-365 * 24 * time.Hour),
		MessageCount: 1,
	}))
	if err := idx.WriteSearchText(key, "old needle"); err != nil {
		t.Fatalf("WriteSearchText: %v", err)
	}

	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 30
	hidden, err := Prune(idx, cfg, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if hidden != 1 {
		t.Fatalf("hidden=%d, want 1", hidden)
	}
	exists, err := idx.SearchTextExists(key)
	if err != nil {
		t.Fatalf("SearchTextExists: %v", err)
	}
	if exists {
		t.Fatalf("sidecar still exists after Prune")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transcript file removed by Prune: %v", err)
	}
}

func TestPrune_BatchesIndexSave(t *testing.T) {
	idx := newTestIndex(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"old-1", "old-2", "old-3"} {
		must(t, idx.SetEntryForTest("claude:"+id, SessionMeta{
			ID: id, Harness: harness.Claude,
			LastAt: now.Add(-60 * 24 * time.Hour),
		}))
	}

	saves := 0
	saveLockedHookForTest = func() { saves++ }
	t.Cleanup(func() { saveLockedHookForTest = nil })

	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 30
	hidden, err := Prune(idx, cfg, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if hidden != 3 {
		t.Fatalf("hidden=%d, want 3", hidden)
	}
	if saves != 1 {
		t.Fatalf("saves=%d, want 1 batched save", saves)
	}
}

func TestPrune_PreservesHiddenEntriesAndSearchExcludesThem(t *testing.T) {
	idx := newTestIndex(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	must(t, idx.SetEntryForTest("claude:hidden", SessionMeta{
		ID: "hidden", Harness: harness.Claude,
		LastAt:          now.Add(-90 * 24 * time.Hour),
		SearchText:      "needle",
		HiddenFromIndex: true,
	}))
	must(t, idx.SetEntryForTest("claude:old", SessionMeta{
		ID: "old", Harness: harness.Claude,
		LastAt:     now.Add(-90 * 24 * time.Hour),
		SearchText: "needle",
	}))

	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 30
	hidden, err := Prune(idx, cfg, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if hidden != 1 {
		t.Fatalf("hidden=%d, want 1 newly hidden entry", hidden)
	}

	all := idx.List(ListFilter{IncludeHidden: true})
	if len(all) != 2 {
		t.Fatalf("hidden entries were dropped from index: len=%d, want 2", len(all))
	}
	results, err := Search(idx, nil, Query{Text: "needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Search returned hidden entries: %+v", results)
	}
}

func TestPrune_ZeroKeepDaysIsNoOp(t *testing.T) {
	idx := newTestIndex(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	must(t, idx.SetEntryForTest("claude:ancient", SessionMeta{
		ID: "ancient", Harness: harness.Claude,
		LastAt: now.Add(-365 * 24 * time.Hour),
	}))

	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 0

	hidden, err := Prune(idx, cfg, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if hidden != 0 {
		t.Errorf("hidden=%d, want 0 when KeepDays=0", hidden)
	}
	got, _ := idx.Get("claude:ancient")
	if got.HiddenFromIndex {
		t.Errorf("entry hidden despite KeepDays=0")
	}
}

func TestPrune_ZeroLastAtIsKept(t *testing.T) {
	idx := newTestIndex(t)
	must(t, idx.SetEntryForTest("claude:undated", SessionMeta{
		ID: "undated", Harness: harness.Claude,
		// LastAt is zero — no parseable timestamp on the file.
	}))

	cfg := &config.Config{}
	cfg.Defaults.SessionArchive.KeepDays = 30

	hidden, err := Prune(idx, cfg, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if hidden != 0 {
		t.Errorf("hidden=%d, want 0 (zero LastAt should not be pruned)", hidden)
	}
}

func TestPrune_NilArgsReturnError(t *testing.T) {
	if _, err := Prune(nil, &config.Config{}, time.Now()); err == nil {
		t.Errorf("expected error for nil index")
	}
	if _, err := Prune(newTestIndex(t), nil, time.Now()); err == nil {
		t.Errorf("expected error for nil config")
	}
}
