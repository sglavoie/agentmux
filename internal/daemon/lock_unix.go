//go:build darwin || linux

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var errDaemonAlreadyRunning = errors.New("daemon already running")
var errAutoStartInProgress = errors.New("daemon auto-start already in progress")

func daemonLockPath(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "daemon.lock")
}

func autoStartLockPath(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "daemon.autostart.lock")
}

func acquireDaemonLock(socketPath string) (*os.File, error) {
	lockPath := daemonLockPath(socketPath)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errDaemonAlreadyRunning
		}
		return nil, fmt.Errorf("lock file %s: %w", lockPath, err)
	}
	return f, nil
}

func releaseDaemonLock(socketPath string, f *os.File) {
	_ = os.Remove(daemonLockPath(socketPath))
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

func acquireAutoStartLock(socketPath string) (*os.File, error) {
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
			return nil, errAutoStartInProgress
		}
		return nil, fmt.Errorf("lock auto-start file %s: %w", lockPath, err)
	}
	return f, nil
}

func releaseAutoStartLock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
