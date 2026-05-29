package clipboard

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

// TestCopyDarwinSmoke verifies Copy round-trips a string via pbcopy/pbpaste.
// Skipped on non-darwin or when the binaries aren't present (CI without GUI).
func TestCopyDarwinSmoke(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	if _, err := exec.LookPath("pbcopy"); err != nil {
		t.Skip("pbcopy not available")
	}
	if _, err := exec.LookPath("pbpaste"); err != nil {
		t.Skip("pbpaste not available")
	}

	// Snapshot the user's clipboard so the test doesn't clobber it.
	prior, priorErr := exec.Command("pbpaste").Output()
	if priorErr == nil {
		t.Cleanup(func() {
			cmd := exec.Command("pbcopy")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				return
			}
			if err := cmd.Start(); err != nil {
				return
			}
			_, _ = stdin.Write(prior)
			_ = stdin.Close()
			_ = cmd.Wait()
		})
	}

	want := "amx-clipboard-test-token"
	if err := Copy(want); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		t.Fatalf("pbpaste: %v", err)
	}
	if string(out) != want {
		t.Fatalf("clipboard mismatch: got %q, want %q", out, want)
	}
}

func TestCopyUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin has a supported backend")
	}
	if err := Copy("ignored"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestCopyDarwinMissingBackend(t *testing.T) {
	err := copy("ignored", "darwin", func(string) (string, error) {
		return "", exec.ErrNotFound
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}
