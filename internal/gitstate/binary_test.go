package gitstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppleGitShimNeverRunsWhenDeveloperToolsAreMissing(t *testing.T) {
	for _, directory := range []string{"", "/path/without/developer/tools"} {
		path, err := resolveGitBinary("darwin", "/usr/bin/git", func() (string, error) { return directory, nil })
		if err == nil || path != "" {
			t.Fatalf("missing tools resolved an executable: %q, %v", path, err)
		}
	}
	path, err := resolveGitBinary("darwin", "/usr/bin/git", func() (string, error) {
		return "", errors.New("no active developer directory")
	})
	if err == nil || path != "" {
		t.Fatalf("failed tool lookup resolved an executable: %q, %v", path, err)
	}
}

func TestAppleGitUsesInstalledToolWithoutExecutingShim(t *testing.T) {
	directory := t.TempDir()
	git := filepath.Join(directory, "usr", "bin", "git")
	if err := os.MkdirAll(filepath.Dir(git), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(git, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := resolveGitBinary("darwin", "/usr/bin/git", func() (string, error) { return directory, nil })
	if err != nil || path != git {
		t.Fatalf("installed Git = %q, %v; want %s", path, err, git)
	}
	// A broken developer installation must not send us back to the shim.
	if err := os.Remove(git); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/git", git); err != nil {
		t.Fatal(err)
	}
	if path, err := resolveGitBinary("darwin", "/usr/bin/git", func() (string, error) { return directory, nil }); err == nil || path != "" {
		t.Fatalf("developer Git led back to shim: %q, %v", path, err)
	}
}

func TestIndependentGitDoesNotNeedAppleDeveloperTools(t *testing.T) {
	for _, tc := range []struct{ platform, candidate string }{
		{"linux", "/usr/bin/git"}, {"darwin", "/opt/homebrew/bin/git"},
	} {
		path, err := resolveGitBinary(tc.platform, tc.candidate, func() (string, error) {
			t.Fatal("independent Git must not query Apple's developer tools")
			return "", nil
		})
		if err != nil || path != tc.candidate {
			t.Fatalf("independent Git = %q, %v", path, err)
		}
	}
}
