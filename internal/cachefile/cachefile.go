// Package cachefile provides the process-safe primitives used by disposable
// filesystem caches. Locks deliberately live beside the metadata file so all
// processes derive the same coordination path without another registry.
package cachefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sglavoie/agentmux/internal/atomicfile"
)

// WithExclusive obtains a blocking advisory lock at <path>.lock for the
// duration of fn. It does not lock path itself: atomic replacement changes the
// inode at path, while the stable sidecar lock remains valid across renames.
func WithExclusive(path string, fn func() error) (retErr error) {
	if fn == nil {
		return errors.New("cachefile: exclusive lock requires a callback")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}
	lockPath := path + ".lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening cache lock %s: %w", lockPath, err)
	}
	defer func() {
		unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeErr := file.Close()
		retErr = errors.Join(retErr, unlockErr, closeErr)
	}()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("locking cache %s: %w", lockPath, err)
	}
	return fn()
}

// WriteFileAtomic replaces path with data using a sibling temporary file.
// The unique temporary name avoids collisions with stale files from crashed
// older writers. Syncing both the file and its parent directory makes the
// replacement durable across a power loss on filesystems that support it.
//
// The mechanism now lives in internal/atomicfile, which authoritative source
// artifacts use too. This remains the cache-facing name so callers keep reading
// as cache code.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return atomicfile.Write(path, data, perm)
}

// Quarantine renames a corrupt metadata file beside its original using a
// sortable, colon-free UTC timestamp. Nanoseconds make collisions between
// repeated recovery attempts vanishingly unlikely.
func Quarantine(path string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	destination := path + ".corrupt-" + stamp
	if err := os.Rename(path, destination); err != nil {
		return "", err
	}
	return destination, nil
}
