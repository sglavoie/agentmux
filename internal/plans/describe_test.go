package plans

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDescribeReadsOnePlanWithoutAnIndex: the name and the recorded working
// directory come from the plan directory itself, and a directory that is not
// a split plan is refused.
func TestDescribeReadsOnePlanWithoutAnIndex(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	planDir := filepath.Join(root, "fix things-aabb112233cc")
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Describe(planDir); err == nil {
		t.Fatal("a directory with no manifest.md was described as a plan")
	}
	if err := os.WriteFile(filepath.Join(planDir, "manifest.md"), []byte("# manifest\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	meta, err := Describe(planDir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "fix things" || meta.DirPath != planDir || meta.CWD != "" {
		t.Fatalf("meta = %+v, want the plan with no working directory", meta)
	}

	metadata, _ := json.Marshal(map[string]any{"version": planMetadataSchemaVersion, "cwd": work})
	if err := os.WriteFile(filepath.Join(planDir, planMetadataFileName), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if meta, err = Describe(planDir); err != nil || meta.CWD != work {
		t.Fatalf("meta = %+v, err = %v, want working directory %s", meta, err, work)
	}

	if _, err := Describe(filepath.Join(root, "missing")); err == nil {
		t.Fatal("a missing directory was described as a plan")
	}
}
