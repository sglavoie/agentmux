package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The path is written into harness hook configuration and handed to the daemon
// start and restart paths, so it has to be the running binary itself, not a
// name that PATH might resolve to a different build.
func TestAmxBinaryReportsTheRunningExecutable(t *testing.T) {
	bin, err := AmxBinary()
	if err != nil {
		t.Fatalf("AmxBinary: %v", err)
	}
	if !filepath.IsAbs(bin) {
		t.Fatalf("AmxBinary() = %q; want an absolute path", bin)
	}
	want, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable is unavailable here: %v", err)
	}
	if bin != want {
		t.Fatalf("AmxBinary() = %q; want %q", bin, want)
	}
}

// The fallback is what makes the fail-open callers work: `amx daemon restart`
// and the hook indicator both proceed with the bare command name, so the value
// must be usable even when the error is discarded.
func TestBinaryNameIsTheFallbackCommand(t *testing.T) {
	if binaryName != "amx" {
		t.Fatalf("binaryName = %q; want the CLI's own command name", binaryName)
	}
}
