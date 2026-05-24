package hooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFn is the write step used by atomicWriteFile. It exists as a
// package-level variable so tests can inject faults (e.g. a simulated crash
// mid-write) without touching the surrounding orchestration code. Production
// code never reassigns this; callers should use t.Cleanup to restore the
// default in tests.
var atomicWriteFn = func(f *os.File, data []byte) (int, error) {
	return f.Write(data)
}

// atomicWriteFile writes data to path atomically: it creates a sibling
// "<name>.tmp" file in the same directory, fsyncs and closes it, then
// renames it over path. On any failure the temp file is removed so a partial
// write never replaces the original. The rename is constrained to the same
// directory so it stays on a single filesystem, where POSIX guarantees
// rename atomicity.
func atomicWriteFile(path string, data []byte, perm os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmpPath := filepath.Join(dir, base+".tmp")

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("creating temp file %s: %w", tmpPath, err)
	}
	// Force the requested mode explicitly: O_CREATE only applies perm when
	// the file is newly created, so a leftover .tmp from a prior crashed
	// write could otherwise retain stale (and possibly broader) permissions
	// before being renamed over the destination.
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod temp file %s: %w", tmpPath, err)
	}

	// Ensure the temp file is removed on any failure path. On the happy path
	// the rename consumes it, so Remove will be a no-op error we ignore.
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if n, werr := atomicWriteFn(f, data); werr != nil {
		_ = f.Close()
		return fmt.Errorf("writing temp file %s: %w", tmpPath, werr)
	} else if n != len(data) {
		_ = f.Close()
		return fmt.Errorf("short write to %s: wrote %d of %d bytes", tmpPath, n, len(data))
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("syncing temp file %s: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing temp file %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpPath, path, err)
	}
	return nil
}

// errInjectedFault is the sentinel returned by tests that fault-inject into
// atomicWriteFn. Exported only via the test build path.
var errInjectedFault = errors.New("hooks: injected write fault")

// writeBackup writes data to path with permissions perm, ensuring perm is
// enforced even when path already exists. os.WriteFile only applies perm on
// creation: an existing file keeps its previous mode, which can leave a
// stale-permission .bak after the user tightens the original settings file
// to 0o600. We chmod after the write so the backup mode always tracks the
// caller-provided perm.
func writeBackup(path string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}
