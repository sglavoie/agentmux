package datastore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	recoveryLockSuffix     = ".recovery.lock"
	recoveryLockRetryDelay = 10 * time.Millisecond
)

// recoveryLock is kept separate from SQLite's own locks. Recovery takes it
// exclusively before moving database artifacts; every ordinary Store keeps a
// shared lock from before opening SQLite until after sql.DB.Close returns.
type recoveryLock struct {
	mu     sync.Mutex
	fd     int
	path   string
	closed bool
}

func acquireSharedRecoveryLock(ctx context.Context, databasePath string) (*recoveryLock, error) {
	return acquireRecoveryLock(ctx, databasePath, unix.LOCK_SH)
}

func acquireExclusiveRecoveryLock(ctx context.Context, databasePath string) (*recoveryLock, error) {
	return acquireRecoveryLock(ctx, databasePath, unix.LOCK_EX)
}

func acquireRecoveryLock(ctx context.Context, databasePath string, mode int) (*recoveryLock, error) {
	if mode != unix.LOCK_SH && mode != unix.LOCK_EX {
		return nil, fmt.Errorf("datastore: invalid recovery lock mode %d", mode)
	}
	path := databasePath + recoveryLockSuffix
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(databaseFileMode))
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("datastore: refusing symbolic-link recovery lock %s", path)
		}
		return nil, fmt.Errorf("datastore: opening recovery lock %s without following links: %w", path, err)
	}
	lock := &recoveryLock{fd: fd, path: path}
	closeOnError := func(primary error) error {
		return errors.Join(primary, lock.Close())
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, closeOnError(fmt.Errorf("datastore: inspecting recovery lock %s: %w", path, err))
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, closeOnError(fmt.Errorf("datastore: recovery lock %s is not a regular file", path))
	}
	if err := unix.Fchmod(fd, uint32(databaseFileMode)); err != nil {
		return nil, closeOnError(fmt.Errorf("datastore: securing recovery lock %s: %w", path, err))
	}
	if err := waitForRecoveryLock(ctx, fd, mode); err != nil {
		return nil, closeOnError(fmt.Errorf("datastore: acquiring %s recovery lock %s: %w", recoveryLockModeName(mode), path, err))
	}
	return lock, nil
}

func waitForRecoveryLock(ctx context.Context, fd, mode int) error {
	for {
		err := unix.Flock(fd, mode|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}

		timer := time.NewTimer(recoveryLockRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func recoveryLockModeName(mode int) string {
	if mode == unix.LOCK_EX {
		return "exclusive"
	}
	return "shared"
}

// downgradeToShared lets recovery transfer its exclusive lock into a live
// Store without creating an unlocked publication window.
func (l *recoveryLock) downgradeToShared() error {
	if l == nil {
		return errors.New("datastore: downgrading nil recovery lock")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("datastore: downgrading closed recovery lock")
	}
	if err := unix.Flock(l.fd, unix.LOCK_SH); err != nil {
		return fmt.Errorf("datastore: downgrading recovery lock %s: %w", l.path, err)
	}
	return nil
}

func (l *recoveryLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true

	unlockErr := unix.Flock(l.fd, unix.LOCK_UN)
	closeErr := unix.Close(l.fd)
	if unlockErr != nil {
		unlockErr = fmt.Errorf("datastore: unlocking recovery lock %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("datastore: closing recovery lock %s: %w", l.path, closeErr)
	}
	return errors.Join(unlockErr, closeErr)
}

func recoveryLockPath(databasePath string) string {
	return databasePath + recoveryLockSuffix
}
