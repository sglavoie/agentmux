//go:build darwin || linux

package process

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A starter that opened the lock file while another daemon held it must
// still contend with whoever locks it next: releasing never replaces the
// file, so every starter locks the same inode.
func TestDaemonLockReleaseKeepsOneLockForEveryStarter(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	first, err := AcquireDaemonLock(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	// A slower starter has opened the file but not locked it yet.
	slow, err := os.OpenFile(DaemonLockPath(socketPath), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slow.Close() }()

	ReleaseDaemonLock(socketPath, first)
	second, err := AcquireDaemonLock(socketPath)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	defer ReleaseDaemonLock(socketPath, second)

	err = syscall.Flock(int(slow.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		t.Fatalf("slow starter's flock = %v, want it to contend with the running daemon", err)
	}
	if _, err := AcquireDaemonLock(socketPath); !errors.Is(err, ErrDaemonAlreadyRunning) {
		t.Fatalf("third start = %v, want ErrDaemonAlreadyRunning", err)
	}
}
