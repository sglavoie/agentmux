//go:build darwin || linux

package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrDaemonAlreadyRunning reports that another daemon already owns the
// single-instance lock for this socket. ErrAutoStartInProgress reports that
// another client is already spawning one.
var ErrDaemonAlreadyRunning = errors.New("daemon already running")
var ErrAutoStartInProgress = errors.New("daemon auto-start already in progress")

// DaemonLockPath is the single-instance lock guarding socketPath. Callers log
// it so an operator can see which lock a duplicate start collided with.
func DaemonLockPath(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "daemon.lock")
}

func autoStartLockPath(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "daemon.autostart.lock")
}

// AcquireDaemonLock takes the single-instance lock for socketPath, returning
// ErrDaemonAlreadyRunning when a live daemon already holds it.
func AcquireDaemonLock(socketPath string) (*os.File, error) {
	lockPath := DaemonLockPath(socketPath)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrDaemonAlreadyRunning
		}
		return nil, fmt.Errorf("lock file %s: %w", lockPath, err)
	}
	return f, nil
}

// ReleaseDaemonLock unlocks the single-instance lock file and leaves it in
// place. The lock is the flock, never the file's existence: unlinking it
// would let a starter that opened the old file before the release lock that
// orphaned inode while another locks a newly created file, and both would
// believe they own the socket.
func ReleaseDaemonLock(_ string, f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// AcquireAutoStartLock serializes client-driven daemon spawns for socketPath,
// returning ErrAutoStartInProgress when another client got there first.
func AcquireAutoStartLock(socketPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	lockPath := autoStartLockPath(socketPath)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open auto-start lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAutoStartInProgress
		}
		return nil, fmt.Errorf("lock auto-start file %s: %w", lockPath, err)
	}
	return f, nil
}

// ReleaseAutoStartLock unlocks the auto-start lock file and, like the
// single-instance lock, leaves the file in place.
func ReleaseAutoStartLock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
