package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sglavoie/agentmux/internal/atomicfile"
)

// atomicWritePrivate replaces path in one step inside a directory it first
// makes private and real. The target check is atomicfile's, with the package's
// prefix, so a symlink or other non-regular target still reads
// "diagnostics: <path> is not a regular file", as validateRegularTarget says.
func atomicWritePrivate(path string, data []byte) error {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	err := atomicfile.Write(path, data, fileMode, atomicfile.NoCreateParents(), atomicfile.RequireRegularTarget())
	var notRegular *atomicfile.NotRegularError
	if errors.As(err, &notRegular) {
		return fmt.Errorf("diagnostics: %w", err)
	}
	return err
}

// syncFile flushes a file that was appended to without per-write syncs.
func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func acquireFileLock(ctx context.Context, path string, timeout time.Duration) (*os.File, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := validateRegularTarget(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(fileMode); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := acquireOpenFileLock(ctx, file, timeout); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func initializeFileLock(path string) error {
	if err := validateRegularTarget(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return err
	}
	return errors.Join(file.Chmod(fileMode), file.Close())
}

// acquireSharedFileLock opens the lock prepared by NewJournal without
// creating or chmodding anything on the snapshot read path.
func acquireSharedFileLock(ctx context.Context, path string, timeout time.Duration) (*os.File, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRegularTarget(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	if err := acquireOpenFileLockMode(ctx, file, timeout, unix.LOCK_SH); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func acquireOpenFileLock(ctx context.Context, file *os.File, timeout time.Duration) error {
	return acquireOpenFileLockMode(ctx, file, timeout, unix.LOCK_EX)
}

func acquireOpenFileLockMode(ctx context.Context, file *os.File, timeout time.Duration, mode int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(timeout)
	for {
		err := unix.Flock(int(file.Fd()), mode|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		if time.Now().After(deadline) {
			return ErrLockTimeout
		}
		timer := time.NewTimer(2 * time.Millisecond)
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

func releaseFileLock(file *os.File) {
	if file == nil {
		return
	}
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	_ = file.Close()
}

func ctxError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func timestamp(now time.Time) string {
	return now.UTC().Format("20060102T150405.000000000Z")
}
