package datastore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type sqliteCodeError int

func (e sqliteCodeError) Error() string { return fmt.Sprintf("sqlite code %d", e) }
func (e sqliteCodeError) Code() int     { return int(e) }

func TestIsCorruptionClassifiesWrappedPrimaryAndExtendedCodes(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "primary", err: sqliteCodeError(sqliteCorruptCode), want: true},
		{name: "wrapped", err: fmt.Errorf("open failed: %w", sqliteCodeError(sqliteCorruptCode)), want: true},
		{name: "extended", err: sqliteCodeError(sqliteCorruptCode | 7<<8), want: true},
		{name: "busy", err: sqliteCodeError(sqliteBusyCode), want: false},
		{name: "plain error", err: errors.New("database disk image is malformed"), want: false},
		{name: "nil", err: nil, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsCorruption(test.err); got != test.want {
				t.Fatalf("IsCorruption(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

func TestRecoveryLockAllowsSharedHoldersAndBlocksExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	first, err := acquireSharedRecoveryLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquire first shared lock: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := acquireSharedRecoveryLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquire second shared lock: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = acquireExclusiveRecoveryLock(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("exclusive lock error = %v, want context.Canceled", err)
	}

	info, err := os.Stat(recoveryLockPath(path))
	if err != nil {
		t.Fatalf("stat recovery lock: %v", err)
	}
	if info.Mode().Perm() != databaseFileMode {
		t.Fatalf("recovery lock mode = %v, want %v", info.Mode().Perm(), databaseFileMode)
	}
}

func TestStoreRetainsSharedRecoveryLockUntilSQLiteCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	store, err := OpenPath(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	acquired := make(chan *recoveryLock, 1)
	errCh := make(chan error, 1)
	go func() {
		lock, err := acquireExclusiveRecoveryLock(ctx, path)
		if err != nil {
			errCh <- err
			return
		}
		acquired <- lock
	}()

	select {
	case lock := <-acquired:
		_ = lock.Close()
		t.Fatal("exclusive recovery lock acquired while Store was live")
	case err := <-errCh:
		t.Fatalf("exclusive recovery lock failed early: %v", err)
	case <-time.After(75 * time.Millisecond):
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case lock := <-acquired:
		if err := lock.Close(); err != nil {
			t.Fatalf("close exclusive lock: %v", err)
		}
	case err := <-errCh:
		t.Fatalf("exclusive recovery lock after Close: %v", err)
	case <-ctx.Done():
		t.Fatal("exclusive recovery lock remained blocked after Store.Close")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}

func TestOpenPathCancellationWhileRecoveryIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	if err := os.MkdirAll(filepath.Dir(path), dataDirectoryMode); err != nil {
		t.Fatal(err)
	}
	exclusive, err := acquireExclusiveRecoveryLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquire exclusive lock: %v", err)
	}
	t.Cleanup(func() { _ = exclusive.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	store, err := OpenPath(ctx, path)
	if store != nil {
		_ = store.Close()
		t.Fatal("OpenPath returned a Store while recovery lock was exclusive")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("OpenPath error = %v, want context.DeadlineExceeded", err)
	}
}

func TestOpenPathUnderExclusiveRecoveryLockLeavesLockWithCaller(t *testing.T) {
	path, err := prepareDatabasePath(filepath.Join(t.TempDir(), DatabaseFileName))
	if err != nil {
		t.Fatalf("prepare database path: %v", err)
	}
	exclusive, err := acquireExclusiveRecoveryLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquire exclusive lock: %v", err)
	}
	store, err := openPathUnderRecoveryLock(context.Background(), path)
	if err != nil {
		_ = exclusive.Close()
		t.Fatalf("open under exclusive recovery lock: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := exclusive.downgradeToShared(); err != nil {
		_ = store.Close()
		_ = exclusive.Close()
		t.Fatalf("downgrade recovery lock: %v", err)
	}
	store.recoveryLock = exclusive

	other, err := OpenPath(context.Background(), path)
	if err != nil {
		_ = store.Close()
		t.Fatalf("OpenPath after exclusive lock downgrade: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatalf("close other Store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close recovered Store: %v", err)
	}
}

func TestFailedOpenReleasesRecoveryLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	if err := os.WriteFile(path, []byte("not a sqlite database"), databaseFileMode); err != nil {
		t.Fatal(err)
	}
	store, err := OpenPath(context.Background(), path)
	if store != nil {
		_ = store.Close()
		t.Fatal("OpenPath returned a Store for invalid SQLite data")
	}
	if err == nil {
		t.Fatal("OpenPath unexpectedly accepted invalid SQLite data")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lock, err := acquireExclusiveRecoveryLock(ctx, path)
	if err != nil {
		t.Fatalf("failed OpenPath leaked shared recovery lock: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("close exclusive lock: %v", err)
	}
}

func TestRecoveryLockRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DatabaseFileName)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, recoveryLockPath(path)); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireSharedRecoveryLock(context.Background(), path)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("acquireSharedRecoveryLock accepted a symlink")
	}
	if err == nil {
		t.Fatal("acquireSharedRecoveryLock returned no error for symlink")
	}
}
