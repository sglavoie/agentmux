package clipboard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	err := copy("ignored", "linux", func(string) (string, error) {
		t.Fatal("lookPath should not be called on unsupported platforms")
		return "", nil
	})
	if !errors.Is(err, ErrUnsupported) {
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

func TestCopyDarwinUnexpectedLookupError(t *testing.T) {
	lookupErr := errors.New("lookup failed")
	err := copy("ignored", "darwin", func(string) (string, error) {
		return "", lookupErr
	})
	if errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected non-unsupported lookup error, got %v", err)
	}
	if !errors.Is(err, lookupErr) {
		t.Fatalf("expected wrapped lookup error, got %v", err)
	}
}

func TestCopyDarwinWritesTextToBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("temporary shell backend requires a POSIX shell")
	}

	output := filepath.Join(t.TempDir(), "clipboard.txt")
	t.Setenv("AMX_CLIPBOARD_TEST_OUTPUT", output)
	backend := writeScript(t, `#!/bin/sh
cat > "$AMX_CLIPBOARD_TEST_OUTPUT"
`)

	want := "first line\nsecond line\n"
	err := copy(want, "darwin", func(name string) (string, error) {
		if name != "pbcopy" {
			t.Fatalf("lookPath called for %q, want pbcopy", name)
		}
		return backend, nil
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read backend output: %v", err)
	}
	if string(got) != want {
		t.Fatalf("backend input mismatch: got %q, want %q", got, want)
	}
}

func TestCopyDarwinBackendFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("temporary shell backend requires a POSIX shell")
	}

	backend := writeScript(t, `#!/bin/sh
cat > /dev/null
exit 7
`)

	err := copy("ignored", "darwin", func(string) (string, error) {
		return backend, nil
	})
	if err == nil {
		t.Fatal("expected backend error")
	}
	if errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected backend error, got unsupported: %v", err)
	}
}

func writeScript(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "pbcopy")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write test backend: %v", err)
	}
	return path
}
