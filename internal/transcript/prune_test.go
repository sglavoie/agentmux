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
