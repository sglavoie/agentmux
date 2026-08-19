package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const mutationLockName = ".amx-worktree.lock"

func withMutationLock(ctx context.Context, root string, fn func() error) error {
	if fn == nil {
		return errors.New("worktree: mutation lock requires a callback")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lockPath := filepath.Join(root, mutationLockName)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("worktree: opening mutation lock: %w", err)
	}
	defer func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("worktree: securing mutation lock: %w", err)
	}
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("worktree: acquiring mutation lock: %w", err)
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf("worktree: acquiring mutation lock: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return fn()
}
