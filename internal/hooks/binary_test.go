package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A build that is not the installed amx is judged against the installed one,
// an installed amx reached through a symlink is not foreign, and a PATH
// without amx leaves the running binary as the only candidate.
func TestExpectedBinaryPrefersTheInstalledAmx(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	installed := filepath.Join(root, "real", "amx")
	link := filepath.Join(root, "bin", "amx")
	dev := filepath.Join(root, "checkout", "amx")
	for _, path := range []string{installed, dev} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installed, link); err != nil {
		t.Fatal(err)
	}
	onPath := func(string) (string, error) { return link, nil }

	if got, foreign := ExpectedBinary(installed, onPath); got != installed || foreign {
		t.Fatalf("installed binary through a symlink = %q, %v; want itself, not foreign", got, foreign)
	}
	if got, foreign := ExpectedBinary(dev, onPath); got != link || !foreign {
		t.Fatalf("dev build = %q, %v; want the PATH amx %q, foreign", got, foreign, link)
	}
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	if got, foreign := ExpectedBinary(dev, missing); got != dev || foreign {
		t.Fatalf("PATH without amx = %q, %v; want the running binary, not foreign", got, foreign)
	}
}
