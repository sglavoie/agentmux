package overrides_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/overrides"
)

func TestLoadMissing(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "overrides.json"))
	f, err := overrides.Load()
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if len(f.Entries) != 0 {
		t.Fatalf("expected empty entries for missing file, got %v", f.Entries)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "overrides.json"))

	want := overrides.File{
		Version: 1,
		Entries: []overrides.Entry{
			{ProjectTitle: "api", ProfileName: "work"},
			{ProjectTitle: "dotfiles", ProfileName: "personal"},
		},
	}
	if err := overrides.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := overrides.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != want.Version {
		t.Errorf("Version: got %d, want %d", got.Version, want.Version)
	}
	if len(got.Entries) != len(want.Entries) {
		t.Fatalf("len(Entries): got %d, want %d", len(got.Entries), len(want.Entries))
	}
	for i, e := range want.Entries {
		if got.Entries[i] != e {
			t.Errorf("Entries[%d]: got %+v, want %+v", i, got.Entries[i], e)
		}
	}
}

func TestSaveEmptyEntriesWritesArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overrides.json")
	t.Setenv(overrides.PathEnvVar, path)

	if err := overrides.Save(overrides.File{Version: 1}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal saved file: %v", err)
	}
	entries, ok := decoded["entries"].([]any)
	if !ok {
		t.Fatalf("entries encoded as %T, want JSON array; raw=%s", decoded["entries"], raw)
	}
	if len(entries) != 0 {
		t.Fatalf("entries length: got %d, want 0", len(entries))
	}
}

func TestSaveAtomic(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "overrides.json")
	t.Setenv(overrides.PathEnvVar, path)

	f := overrides.File{
		Version: 1,
		Entries: []overrides.Entry{{ProjectTitle: "api", ProfileName: "work"}},
	}
	if err := overrides.Save(f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// No .tmp file should remain after a successful Save (it was renamed).
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("expected no .tmp file after successful Save")
	}
	// The final file should be loadable with correct content.
	got, err := overrides.Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].ProjectTitle != "api" {
		t.Errorf("unexpected entries after Save: %v", got.Entries)
	}
}

func TestSaveIgnoresBlockedLegacyTempPath(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "overrides.json")
	t.Setenv(overrides.PathEnvVar, path)

	legacyTmp := path + ".tmp"
	if err := os.Mkdir(legacyTmp, 0o755); err != nil {
		t.Fatalf("seed blocked legacy temp path: %v", err)
	}

	f := overrides.File{
		Version: 1,
		Entries: []overrides.Entry{{ProjectTitle: "api", ProfileName: "work"}},
	}
	if err := overrides.Save(f); err != nil {
		t.Fatalf("Save with blocked legacy temp path: %v", err)
	}
	if info, err := os.Stat(legacyTmp); err != nil || !info.IsDir() {
		t.Fatalf("legacy temp path should be left untouched as a directory, info=%v err=%v", info, err)
	}
	got, err := overrides.Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0] != f.Entries[0] {
		t.Errorf("unexpected entries after Save: %v", got.Entries)
	}
}

// TestSaveCleansUpTempOnRenameFailure pins the post-failure invariant: when
// the final rename cannot complete (here because the destination path is
// already occupied by a directory), the Save call must report the error and
// must not leave a "<name>.tmp" sibling on disk for the next caller to step
// on. Regression guard against the previous os.WriteFile-based path that
// left orphaned .tmp files behind whenever the rename failed.
func TestSaveCleansUpTempOnRenameFailure(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "overrides.json")
	// Occupy the destination with a non-empty directory so os.Rename fails.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("seed directory at destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	t.Setenv(overrides.PathEnvVar, path)

	err := overrides.Save(overrides.File{Version: 1})
	if err == nil {
		t.Fatal("Save should have failed; destination is a non-empty directory")
	}
	if _, statErr := os.Stat(path + ".tmp"); !os.IsNotExist(statErr) {
		t.Fatalf("expected .tmp cleaned up after rename failure, stat err=%v", statErr)
	}
}
