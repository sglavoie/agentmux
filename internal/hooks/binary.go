package hooks

import (
	"path/filepath"
)

// ExpectedBinary reports the amx a hook file should name when running is the
// binary asking. That is the installed amx — the one lookPath finds for the
// bare name, which is what `just install` replaces and what the hook files
// name today — whenever running is some other file, as a freshly built ./amx
// in a checkout is. Judged against itself, such a build reports every hook
// file as drifted and recommends a sync that would point every harness at a
// binary the next build or temp-directory cleanup removes. foreign reports
// that the two differ, so a caller can say which binary it judged against.
//
// When PATH has no amx, or either path cannot be resolved, the running binary
// is the only candidate and is returned unchanged.
func ExpectedBinary(running string, lookPath func(string) (string, error)) (expected string, foreign bool) {
	installed, err := lookPath("amx")
	if err != nil || installed == "" {
		return running, false
	}
	if abs, err := filepath.Abs(installed); err == nil {
		installed = abs
	}
	if SameBinary(running, installed) {
		return running, false
	}
	return installed, true
}

// SameBinary reports whether two paths name the same file once each is made
// absolute and its symlinks are followed, so an installed amx reached through
// a symlinked bin directory is not mistaken for another build.
func SameBinary(a, b string) bool {
	return resolveBinary(a) == resolveBinary(b)
}

func resolveBinary(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return filepath.Clean(path)
}
