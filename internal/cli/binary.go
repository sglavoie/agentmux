package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// binaryName is the bare command used when the process cannot locate its own
// executable. It resolves through PATH, which is enough for the two callers
// that tolerate the failure: writing a hook command line, and re-execing the
// daemon.
const binaryName = "amx"

// AmxBinary reports the path of the running amx binary. Commands need it
// because they write it into harness hook configuration and hand it to the
// daemon start and restart paths, so the daemon that comes back up is the same
// build as the CLI that asked for it.
//
// On failure it returns binaryName alongside the reason: callers that must fail
// loudly return the error, and callers that are deliberately fail-open (the
// hook indicator, `amx daemon restart`) report or ignore it and use the
// fallback, exactly as they did when each resolved os.Executable itself.
func AmxBinary() (string, error) {
	bin, err := os.Executable()
	if err != nil {
		return binaryName, fmt.Errorf("resolving amx binary: %w", err)
	}
	return bin, nil
}

// TransientBinary reports whether path is an executable `go run` built for
// itself rather than one that was installed or built on purpose. Go puts it in
// one of two places under a `go-build*` directory: an `exe` directory in a
// temporary work tree that is deleted when the command exits, or, since Go
// 1.24, a `<hash>-d` entry in the build cache that the next cache trim evicts.
// Writing such a path into hook files, or re-execing the daemon from it,
// leaves something pointing at a file that is about to disappear, and the
// hook indicator fails open, so nothing would say so. Test binaries are not
// caught: `go test` builds them beside the package archive in `b001`.
func TransientBinary(path string) bool {
	dir := filepath.Dir(filepath.Clean(path))
	if parent := filepath.Base(dir); parent != "exe" && !strings.HasSuffix(parent, "-d") {
		return false
	}
	for _, elem := range strings.Split(filepath.ToSlash(dir), "/") {
		if strings.HasPrefix(elem, "go-build") {
			return true
		}
	}
	return false
}

// RefuseTransientBinary returns an error naming the action when path is a
// `go run` build (see TransientBinary), and nil otherwise.
func RefuseTransientBinary(path, action string) error {
	if !TransientBinary(path) {
		return nil
	}
	return fmt.Errorf("refusing to %s from %s: it is a `go run` build that Go deletes once the command exits or its build cache is trimmed; run the installed amx, or a built one such as `just build`'s ./amx", action, path)
}
