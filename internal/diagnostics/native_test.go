package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureNativeLaunchUsesUUIDAndPrivateDirectory(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	launchID, err := NewLaunchID()
	if err != nil {
		t.Fatal(err)
	}
	if !validLaunchID(launchID) {
		t.Fatalf("NewLaunchID() = %q", launchID)
	}
	dir, err := EnsureNativeLaunch(paths, launchID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode = %o, want 700", got)
	}
	if _, err := NativeLaunchDir(paths, "../escape"); err == nil {
		t.Fatal("NativeLaunchDir accepted traversal")
	}
}

func TestMaintainNativeProtectsActiveAndPrunesInactive(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	now := time.Unix(1_800_000_000, 0)
	activeID := "11111111-1111-4111-8111-111111111111"
	oldID := "22222222-2222-4222-8222-222222222222"
	trimID := "33333333-3333-4333-8333-333333333333"
	activeDir := writeNativeFixture(t, paths, activeID, "active.log", 20, now.Add(-48*time.Hour))
	oldDir := writeNativeFixture(t, paths, oldID, "old.log", 7, now.Add(-30*24*time.Hour))
	trimDir := writeNativeFixture(t, paths, trimID, "trim.log", 15, now.Add(-time.Hour))

	report, err := MaintainNative(paths, NativeOptions{
		KeepDays: 14, MaxBytes: 30, LaunchMaxBytes: 10, Now: func() time.Time { return now },
	}, map[string]struct{}{activeID: {}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(activeDir); err != nil {
		t.Fatalf("active launch was pruned: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expired inactive launch still exists: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(trimDir, "trim.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 10 || data != nil && !strings.HasSuffix(strings.Repeat("x", 15), string(data)) {
		t.Fatalf("trimmed data length/content = %d %q", len(data), data)
	}
	info, err := os.Stat(filepath.Join(trimDir, "trim.log"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("maintained file mode = %v, want 0600", info.Mode().Perm())
	}
	if report.ActiveExcessBytes != 10 || report.ExcessBytes != 0 || report.RemovedLaunches != 1 || report.TrimmedFiles != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestMaintainNativeAggregateQuotaPrunesOldestInactiveLaunch(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	now := time.Unix(1_800_000_000, 0)
	activeID := "55555555-5555-4555-8555-555555555555"
	oldID := "66666666-6666-4666-8666-666666666666"
	newID := "77777777-7777-4777-8777-777777777777"
	writeNativeFixture(t, paths, activeID, "active.log", 12, now.Add(-3*time.Hour))
	oldDir := writeNativeFixture(t, paths, oldID, "old.log", 8, now.Add(-2*time.Hour))
	newDir := writeNativeFixture(t, paths, newID, "new.log", 8, now.Add(-time.Hour))

	report, err := MaintainNative(paths, NativeOptions{
		KeepDays: 14, MaxBytes: 20, LaunchMaxBytes: 20, Now: func() time.Time { return now },
	}, map[string]struct{}{activeID: {}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("oldest inactive launch still exists: %v", err)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Fatalf("newer inactive launch was removed: %v", err)
	}
	if report.TotalBytes != 20 || report.ExcessBytes != 0 || report.RemovedLaunches != 1 || report.RemovedBytes != 8 {
		t.Fatalf("report = %+v", report)
	}
}

func TestInspectNativeIsReadOnly(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	launchID := "44444444-4444-4444-8444-444444444444"
	dir := writeNativeFixture(t, paths, launchID, "debug.log", 12, time.Now().Add(-48*time.Hour))
	report, err := InspectNative(paths, NativeOptions{KeepDays: 1, MaxBytes: 5, LaunchMaxBytes: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalBytes != 12 || report.ExcessBytes != 7 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(dir, "debug.log")); err != nil {
		t.Fatalf("InspectNative mutated storage: %v", err)
	}
}

func TestMaintainNativeProtectsRecentlyPreparedLaunchFromConcurrentPruning(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	now := time.Unix(1_800_000_000, 0)
	launchID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	dir := writeNativeFixture(t, paths, launchID, "starting.log", 20, now.Add(-time.Minute))
	report, err := MaintainNative(paths, NativeOptions{
		KeepDays: 1, MaxBytes: 1, LaunchMaxBytes: 1, Now: func() time.Time { return now },
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "starting.log")); err != nil {
		t.Fatalf("recent launch was pruned before Kitty could report it active: %v", err)
	}
	if report.ExcessBytes != 19 {
		t.Fatalf("report = %+v, want visible deferred excess", report)
	}
}

func TestMaintainNativeRecoversOrphanTrimFile(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	now := time.Unix(1_800_000_000, 0)
	modified := now.Add(-time.Hour)
	launchID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	dir := writeNativeFixture(t, paths, launchID, "debug.log", 15, modified)
	tempPath := filepath.Join(dir, "debug.log.trim")
	if err := os.WriteFile(tempPath, []byte("orphan"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tempPath, modified, modified); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, modified, modified); err != nil {
		t.Fatal(err)
	}

	report, err := MaintainNative(paths, NativeOptions{
		KeepDays: 14, MaxBytes: 100, LaunchMaxBytes: 10, Now: func() time.Time { return now },
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("orphan trim file still exists: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 10 || report.TrimmedFiles != 1 || len(report.Errors) != 0 {
		t.Fatalf("data length = %d, report = %+v", len(data), report)
	}
}

func TestMaintainNativeWaitsForCrossProcessMaintenanceLock(t *testing.T) {
	t.Parallel()

	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	now := time.Unix(1_800_000_000, 0)
	launchID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	dir := writeNativeFixture(t, paths, launchID, "old.log", 8, now.Add(-30*24*time.Hour))
	lock, err := acquireFileLock(context.Background(), nativeMaintenanceLockPath(paths), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := MaintainNative(paths, NativeOptions{KeepDays: 14, Now: func() time.Time { return now }}, nil)
		done <- err
	}()
	time.Sleep(25 * time.Millisecond)
	if _, err := os.Stat(dir); err != nil {
		releaseFileLock(lock)
		t.Fatalf("maintenance mutated storage before acquiring the lock: %v", err)
	}
	releaseFileLock(lock)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not resume after the lock was released")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expired launch still exists after maintenance resumed: %v", err)
	}
}

func writeNativeFixture(t *testing.T, paths Paths, launchID, name string, size int, modified time.Time) string {
	t.Helper()
	dir, err := EnsureNativeLaunch(paths, launchID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, modified, modified); err != nil {
		t.Fatal(err)
	}
	return dir
}
