// Package atomicfile replaces a file's contents in one step, so a reader never
// observes a partial write and a crash never leaves a truncated file behind.
//
// It exists apart from internal/cachefile because the two have opposite
// stakes. A cache is disposable: a corrupt one is rebuilt from the source and
// the worst outcome is wasted work. The files this package was extracted for —
// config.toml above all — are authoritative source artifacts. There is nothing
// to rebuild them from, so "the write was interrupted" and "the operator's
// configuration is gone" are the same sentence. cachefile now delegates here,
// which keeps one implementation without implying config is a cache.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Write replaces path with data at the given mode.
//
// The temporary file is a sibling rather than a file in TMPDIR, because rename
// is only atomic within a filesystem and TMPDIR is routinely on another one.
// Its name is unique so a stale temp from a crashed older writer cannot be
// adopted, and it is removed on every failure path so a failed replacement
// leaves nothing but the original file.
//
// Both the file and its parent directory are synced. Syncing only the file
// makes the bytes durable while leaving the directory entry that points at them
// unwritten, which is exactly the case where a power loss loses the rename.
func Write(path string, data []byte, perm os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temporary file beside %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	// Chmod rather than trusting CreateTemp's 0600: the replacement must land
	// with the mode the caller asked for, not the one the temp happened to get.
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := dirFile.Sync()
	closeErr := dirFile.Close()
	return errors.Join(syncErr, closeErr)
}

// Replace rewrites an existing file, keeping the mode it already had.
//
// Preserving the mode is the point: an operator who tightened config.toml to
// 0600 did so deliberately, and a rewrite that silently widened it back to a
// default would be a quiet regression in a file that can name private paths.
// fallbackPerm applies only when there is no file yet.
func Replace(path string, data []byte, fallbackPerm os.FileMode) error {
	perm := fallbackPerm
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading the mode of %s: %w", path, err)
	}
	return Write(path, data, perm)
}
