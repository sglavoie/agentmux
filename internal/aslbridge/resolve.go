package aslbridge

// Executable resolution for the bridge command.
//
// The daemon is not started from the operator's shell. It is spawned detached,
// by a login agent or by an `amx` invocation that may itself have been started
// by Kitty, and it inherits whatever PATH that ancestor had — which on a stock
// macOS login agent does not include `~/.local/bin`. `exec.LookPath` alone
// therefore fails to find an `asl` that the operator can run by name in every
// terminal they own, and the resulting `executable file not found in $PATH` is
// a diagnostic that sends people looking at their shell profile for a problem
// that is not there.
//
// So a bare command name is resolved against the directory holding the running
// `amx` binary *before* the daemon's PATH. That directory is where both halves
// of a paired install normally land — one stow target, one `~/.local/bin`, one
// Homebrew prefix — and it is a location the daemon can name without consulting
// any environment at all. PATH remains the fallback, because a split install is
// legitimate.
//
// `amx config asl-sync` exists so that neither lookup is load-bearing at run
// time: it persists the absolute path once, and a path-bearing command skips
// both branches below.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolution describes where a bridge command was found. It is reported so an
// operator can tell a configured absolute path from a lucky PATH hit, which is
// the difference between a pairing that survives a reboot and one that does not.
type Resolution struct {
	// Command is the configured value that was resolved.
	Command string
	// Executable is the absolute path that will be executed.
	Executable string
	// Source names how it was found: "configured", "sibling", or "path".
	Source string
}

// Resolution sources.
const (
	// SourceConfigured is a command that already carried a path separator, so
	// no lookup happened and nothing about the environment was consulted.
	SourceConfigured = "configured"
	// SourceSibling is a bare name found next to the running amx binary.
	SourceSibling = "sibling"
	// SourcePath is a bare name found on the daemon's inherited PATH.
	SourcePath = "path"
)

// executableDir is the seam for locating the running amx binary. Tests replace
// it; it is not read from PATH, because the whole point is to have one answer
// that does not depend on the environment.
var executableDir = func() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	// Resolved rather than used verbatim: a symlinked launcher's directory is
	// the link farm, and the sibling asl is beside the real binary.
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		return "", err
	}
	return filepath.Dir(resolved), nil
}

// lookPath is the seam for PATH resolution.
var lookPath = exec.LookPath

// ResolveCommand resolves a configured bridge command to an absolute path.
//
// A command containing a path separator is taken at its word: it is cleaned,
// made absolute, and verified to be an executable file. A bare name is looked
// for beside the running amx binary first and on PATH second.
//
// The returned error wraps exec.ErrNotFound for every not-found case, because
// the manager treats that as the one permanent, non-retryable launch failure
// and must not learn to distinguish two spellings of the same condition.
func ResolveCommand(command string) (Resolution, error) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return Resolution{}, fmt.Errorf("asl bridge: no command configured: %w", exec.ErrNotFound)
	}

	if strings.ContainsRune(trimmed, filepath.Separator) {
		absolute, err := filepath.Abs(filepath.Clean(trimmed))
		if err != nil {
			return Resolution{}, fmt.Errorf("asl bridge: %s: %w", trimmed, exec.ErrNotFound)
		}
		if err := checkExecutable(absolute); err != nil {
			return Resolution{}, fmt.Errorf("asl bridge: %s: %w", absolute, err)
		}
		return Resolution{Command: trimmed, Executable: absolute, Source: SourceConfigured}, nil
	}

	if dir, err := executableDir(); err == nil && dir != "" {
		candidate := filepath.Join(dir, trimmed)
		if checkExecutable(candidate) == nil {
			return Resolution{Command: trimmed, Executable: candidate, Source: SourceSibling}, nil
		}
	}

	found, err := lookPath(trimmed)
	if err != nil {
		// Reported with the sibling directory named, so the diagnostic says
		// where the daemon actually looked rather than implying PATH is the
		// only place it could have found the command.
		return Resolution{}, fmt.Errorf(
			"asl bridge: %q not found beside %s or on the daemon's PATH: %w",
			trimmed, siblingDirForMessage(), exec.ErrNotFound,
		)
	}
	absolute, err := filepath.Abs(found)
	if err != nil {
		absolute = found
	}
	return Resolution{Command: trimmed, Executable: absolute, Source: SourcePath}, nil
}

// siblingDirForMessage renders the sibling directory for a diagnostic, or a
// fixed phrase when the running binary cannot be located at all.
func siblingDirForMessage() string {
	dir, err := executableDir()
	if err != nil || dir == "" {
		return "the amx binary's directory"
	}
	return dir
}

// checkExecutable reports whether path is a regular file this process may
// execute. A directory or a non-executable file is reported as not-found
// rather than as its own condition: from the daemon's point of view there is
// no bridge there either way, and the manager's permanent-failure rule keys on
// exec.ErrNotFound.
func checkExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return exec.ErrNotFound
		}
		return exec.ErrNotFound
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return exec.ErrNotFound
	}
	if err := unixExecutable(path, info); err != nil {
		return err
	}
	return nil
}

// unixExecutable reports whether the mode bits permit execution. It checks the
// bits rather than attempting the exec, because a probe that runs the binary
// would be a side effect inside what callers treat as a lookup.
func unixExecutable(path string, info fs.FileInfo) error {
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable: %w", path, exec.ErrNotFound)
	}
	return nil
}
