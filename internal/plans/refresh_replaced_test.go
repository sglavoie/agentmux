package plans

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshPrunesPlanDirectoryReplacedByFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := seedPlanDir(t, root, "plan", map[string]string{"manifest.md": "obsolete needle\n"})
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	const replacement = "keep this ordinary file\n"
	if err := os.WriteFile(dir, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Get(PlanKey(dir)); ok {
		t.Error("refresh retained a plan whose directory is now a regular file")
	}
	if _, err := os.Stat(idx.store.Path(PlanKey(dir))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("obsolete search sidecar remains: %v", err)
	}
	results, err := Search(idx, Query{Text: "needle"})
	if err != nil || len(results) != 0 {
		t.Errorf("removed plan still matches: %+v, %v", results, err)
	}
	loaded, err := LoadIndexAt(idx.Path())
	if err != nil || loaded.Len() != 0 {
		t.Fatalf("stale plan remains after reload: %v", err)
	}
	if data, err := os.ReadFile(dir); err != nil || string(data) != replacement {
		t.Errorf("refresh modified the replacement file: %q, %v", data, err)
	}
}

func TestRefreshPrunesPlanDirectoryReplacedBySymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := seedPlanDir(t, root, "plan", map[string]string{"manifest.md": "obsolete needle\n"})
	idx := newTestIndex(t)
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	targetRoot := t.TempDir()
	target := seedPlanDir(t, targetRoot, "target", map[string]string{"manifest.md": "keep the symlink target\n"})
	if err := os.Symlink(target, dir); err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Get(PlanKey(dir)); ok {
		t.Error("refresh retained a plan whose directory is now an excluded symlink")
	}
	if _, err := os.Stat(idx.store.Path(PlanKey(dir))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("obsolete search sidecar remains: %v", err)
	}
	if got, err := os.Readlink(dir); err != nil || got != target {
		t.Errorf("refresh changed the symlink: %q, %v", got, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "manifest.md")); err != nil || string(data) != "keep the symlink target\n" {
		t.Errorf("refresh modified the symlink target: %q, %v", data, err)
	}
}
