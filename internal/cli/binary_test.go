package cli

import (
	"os"
	"path/filepath"
	"strings"
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

// A `go run` binary vanishes with the command that built it, so hook files and
// a restarted daemon must never be pointed at one. Test binaries and ordinary
// builds, including a repo-local ./amx, stay allowed.
func TestTransientBinaryRecognizesGoRunBuilds(t *testing.T) {
	cases := map[string]bool{
		"/var/folders/xy/T/go-build123456/b001/exe/agentmux":       true,
		"/private/tmp/claude-501/go-build98765/b001/exe/amx":       true,
		"/Users/me/Library/Caches/go-build/bd/bded69c7-d/agentmux": true,
		"/tmp/go-build4242/b001/hooks.test":                        false,
		"/Users/me/.local/bin/amx":                                 false,
		"/Users/me/src/agentmux/amx":                               false,
		"/Users/me/exe/amx":                                        false,
		"/Users/me/release-d/amx":                                  false,
	}
	for path, want := range cases {
		if got := TransientBinary(path); got != want {
			t.Errorf("TransientBinary(%q) = %v; want %v", path, got, want)
		}
	}
}

func TestRefuseTransientBinaryNamesTheActionAndTheWayOut(t *testing.T) {
	if err := RefuseTransientBinary("/Users/me/.local/bin/amx", "write hooks"); err != nil {
		t.Fatalf("installed binary refused: %v", err)
	}
	err := RefuseTransientBinary("/tmp/go-build1/b001/exe/agentmux", "write hooks")
	if err == nil {
		t.Fatal("go run binary accepted")
	}
	for _, want := range []string{"refusing to write hooks", "/tmp/go-build1/b001/exe/agentmux", "installed amx"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}
