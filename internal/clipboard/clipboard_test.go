package clipboard

import (
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
	if err := Copy("ignored"); err == nil {
		t.Fatal("expected ErrUnsupported, got nil")
	}
}
