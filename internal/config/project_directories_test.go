package config

import (
	"os"
	"path/filepath"
	"testing"
)

// An archived project is kept for later and commonly names a checkout that is
// gone, so the directory checks skip it while still judging every other
// project, and the TOML key decodes into it.
func TestProjectDirectoryIssuesSkipArchivedProjects(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	projects := []Session{
		{Title: "live", Directory: gone, Profile: "work"},
		{Title: "kept", Directory: gone, Profile: "work", Archived: true},
	}
	issues := ProjectDirectoryIssues(projects)
	if len(issues) != 1 || issues[0].Project != "work/live" {
		t.Fatalf("issues = %+v; want only work/live", issues)
	}
	if active := ActiveProjects(projects); len(active) != 1 || active[0].Title != "live" {
		t.Fatalf("ActiveProjects = %+v; want only live", active)
	}
	if n := ArchivedCount(projects); n != 1 {
		t.Fatalf("ArchivedCount = %d; want 1", n)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[[projects]]\ntitle = \"kept\"\ndirectory = \"~\"\nprofile = \"work\"\narchived = true\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Projects) != 1 || !cfg.Projects[0].Archived || !cfg.Sessions[0].Archived {
		t.Fatalf("archived did not decode into both views: %+v / %+v", cfg.Projects, cfg.Sessions)
	}
}
