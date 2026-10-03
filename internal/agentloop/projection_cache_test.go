package agentloop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func statePathFor(t *testing.T, stateDir string) string {
	t.Helper()
	return filepath.Join(stateDir, stateFileName)
}

func TestProjectionCacheReusesUnchangedDocumentsAndNoticesRewrites(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	stateDir := seedProjection(t, root, planDir, projectionSeed{
		RunID: "run-1",
		Phase: "turn",
	})
	statePath := statePathFor(t, stateDir)

	cache := NewProjectionCache()
	first, err := cache.read(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID != "run-1" {
		t.Fatalf("run id = %q", first.RunID)
	}

	// Rewrite the document body only, keeping mtime and size fixed. The cache is
	// keyed on exactly those two, so this is the change it is documented not to
	// see — asserting it here keeps the trade explicit rather than incidental.
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	swapped := append([]byte(nil), data...)
	copy(swapped, data)
	if err := os.WriteFile(statePath, swapped, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(statePath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	again, err := cache.read(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if again.RunID != "run-1" {
		t.Fatalf("cached run id = %q, want run-1", again.RunID)
	}

	// A real transition rewrites the document and moves its mtime. That must be
	// picked up: a cache that pinned the old projection would report a finished
	// run as still turning.
	seedProjection(t, root, planDir, projectionSeed{
		RunID:      "run-1",
		Phase:      "finished",
		Generation: 7,
		FinishedAt: "2026-07-10T13:59:00+00:00",
	})
	if err := os.Chtimes(statePath, time.Time{}, info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	fresh, err := cache.read(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Phase != PhaseFinished {
		t.Fatalf("phase after rewrite = %q, want finished", fresh.Phase)
	}
	if fresh.Generation != 7 {
		t.Fatalf("generation after rewrite = %d, want 7", fresh.Generation)
	}
}

// A cache that only ever grew would pin one decode per plan the machine has
// ever held, which is the very footprint the sweep exists to avoid re-reading.
func TestProjectionCacheForgetsPlansThatLeftTheStateRoot(t *testing.T) {
	root := t.TempDir()
	plans := t.TempDir()
	kept := filepath.Join(plans, "kept")
	removed := filepath.Join(plans, "removed")
	seedProjection(t, root, kept, projectionSeed{RunID: "run-kept", Phase: "turn"})
	removedState := seedProjection(t, root, removed, projectionSeed{RunID: "run-removed", Phase: "turn"})

	cache := NewProjectionCache()
	opts := DiscoverOptions{StateRoot: root, Cache: cache, ProcessBirth: staticBirth(nil)}
	if _, err := DiscoverPlans(opts); err != nil {
		t.Fatal(err)
	}
	if cache.Len() != 2 {
		t.Fatalf("cache holds %d entries, want 2", cache.Len())
	}

	if err := os.RemoveAll(removedState); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverPlans(opts); err != nil {
		t.Fatal(err)
	}
	if cache.Len() != 1 {
		t.Fatalf("cache holds %d entries after the plan left, want 1", cache.Len())
	}
}

// Caching is opt-in so one-shot callers keep read-through semantics, and a nil
// cache must be usable rather than a panic waiting on the sweep path.
func TestDiscoverWorksWithoutACache(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	seedProjection(t, root, planDir, projectionSeed{RunID: "run-1", Phase: "turn"})

	result, err := DiscoverPlans(DiscoverOptions{StateRoot: root, ProcessBirth: staticBirth(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(result.Observations))
	}
}

// The expensive documents to re-parse are the corrupt ones, since they are the
// least likely to change. Their verdict is cached like any other.
func TestProjectionCacheMemoizesFailures(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, stateFileName)
	if err := os.WriteFile(statePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := NewProjectionCache()
	if _, err := cache.read(statePath); err == nil {
		t.Fatal("corrupt document decoded without error")
	}
	if _, err := cache.read(statePath); err == nil {
		t.Fatal("cached corrupt document decoded without error")
	}
	if cache.Len() != 1 {
		t.Fatalf("cache holds %d entries, want 1", cache.Len())
	}
}

// A missing file has nothing to key an entry on. It must read through rather
// than serve whatever was last seen at that path.
func TestProjectionCacheReadsThroughWhenTheFileIsGone(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(t.TempDir(), "plan")
	stateDir := seedProjection(t, root, planDir, projectionSeed{RunID: "run-1", Phase: "turn"})
	statePath := statePathFor(t, stateDir)

	cache := NewProjectionCache()
	if _, err := cache.read(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.read(statePath); ProjectionReason(err) != ReasonNoState {
		t.Fatalf("removed state file reason = %q, want %q", ProjectionReason(err), ReasonNoState)
	}
}
