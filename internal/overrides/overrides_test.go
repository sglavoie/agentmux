package overrides_test

import (
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
