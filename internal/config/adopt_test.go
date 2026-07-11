package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validAdoptionConfig(home string) []byte {
	return []byte(`[[profiles]]
name = "work"
harness = "claude"
home_dir = "` + home + `"
color = "blue"

[harness_emojis]
claude = "🦊"
`)
}

func TestFileAdopterPreservesTextPermissionsAndCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := append(validAdoptionConfig(dir), []byte("\n# keep this comment\n")...)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(dir, "api")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := NewFileAdopter(path, original).AdoptProject(context.Background(), Session{Title: "API", Directory: projectDir, Profile: "work"})
	if err != nil {
		t.Fatalf("AdoptProject: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.HasPrefix(got, original) || !strings.Contains(string(got), "[[projects]]\n  title = \"API\"") {
		t.Fatalf("adopted config did not preserve prefix or block:\n%s", got)
	}
	backup, _ := os.ReadFile(result.BackupPath)
	if !bytes.Equal(backup, original) {
		t.Fatalf("backup differs from original")
	}
	for _, check := range []string{path, result.BackupPath} {
		info, err := os.Stat(check)
		if err != nil || info.Mode().Perm() != 0o640 {
			t.Fatalf("mode %s = %v, %v; want 0640", check, info.Mode().Perm(), err)
		}
	}
	cfg, err := Load(path)
	if err != nil || Validate(cfg) != nil || len(cfg.Projects) != 1 {
		t.Fatalf("published config invalid: cfg=%+v load=%v validate=%v", cfg, err, Validate(cfg))
	}
}

func TestFileAdopterRejectsConcurrentChangeDuplicateAndInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "api")
	_ = os.Mkdir(projectDir, 0o755)
	project := Session{Title: "API", Directory: projectDir, Profile: "work"}

	t.Run("concurrent", func(t *testing.T) {
		path := filepath.Join(dir, "concurrent.toml")
		original := validAdoptionConfig(dir)
		_ = os.WriteFile(path, original, 0o600)
		changed := append(append([]byte(nil), original...), []byte("# external\n")...)
		_ = os.WriteFile(path, changed, 0o600)
		_, err := NewFileAdopter(path, original).AdoptProject(context.Background(), project)
		if !errors.Is(err, ErrConcurrentChange) {
			t.Fatalf("error = %v, want ErrConcurrentChange", err)
		}
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, changed) {
			t.Fatal("concurrent file was overwritten")
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		path := filepath.Join(dir, "duplicate.toml")
		block, _ := ProjectBlock(project)
		original := appendProjectBlock(validAdoptionConfig(dir), block)
		_ = os.WriteFile(path, original, 0o600)
		_, err := NewFileAdopter(path, original).AdoptProject(context.Background(), project)
		if !errors.Is(err, ErrDuplicateProject) {
			t.Fatalf("error = %v, want ErrDuplicateProject", err)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		path := filepath.Join(dir, "invalid.toml")
		original := []byte("[[profiles]]\nname = \"work\"\nharness = \"claude\"\n")
		_ = os.WriteFile(path, original, 0o600)
		_, err := NewFileAdopter(path, original).AdoptProject(context.Background(), project)
		if err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("error = %v, want validation failure", err)
		}
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, original) {
			t.Fatal("invalid config was mutated")
		}
	})
}

func TestFileAdopterRenameFailureRollsBackOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := validAdoptionConfig(dir)
	_ = os.WriteFile(path, original, 0o600)
	oldRename := adoptRename
	adoptRename = func(source, target string) error {
		if target == path && strings.Contains(filepath.Base(source), ".config-adopt-") {
			return errors.New("injected rename failure")
		}
		return os.Rename(source, target)
	}
	t.Cleanup(func() { adoptRename = oldRename })
	_, err := NewFileAdopter(path, original).AdoptProject(context.Background(), Session{Title: "API", Directory: filepath.Join(dir, "api"), Profile: "work"})
	if err == nil {
		t.Fatal("expected rename failure")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("original changed after failed publish")
	}
}
