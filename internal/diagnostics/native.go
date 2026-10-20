package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const nativeLaunchGrace = 5 * time.Minute

// NativeOptions controls retention for raw harness-owned logs.
type NativeOptions struct {
	KeepDays       int
	MaxBytes       int64
	LaunchMaxBytes int64
	Now            func() time.Time
}

// NativeReport describes both current storage and maintenance work. Active
// launch directories are never mutated, even when they exceed a configured
// target.
type NativeReport struct {
	Launches          int
	ActiveLaunches    int
	TotalBytes        int64
	ExcessBytes       int64
	ActiveExcessBytes int64
	RemovedLaunches   int
	RemovedFiles      int
	RemovedBytes      int64
	TrimmedFiles      int
	Errors            []string
}

type nativeLaunch struct {
	id       string
	path     string
	active   bool
	recent   bool
	size     int64
	modified time.Time
	files    []nativeFile
}

type nativeFile struct {
	path     string
	size     int64
	modified time.Time
}

// NewLaunchID returns a random RFC 4122 version-4 UUID for one controlled
// harness launch.
func NewLaunchID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("diagnostics: generating launch ID: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

// NativeLaunchDir resolves a launch below the protected native root without
// touching the filesystem.
func NativeLaunchDir(paths Paths, launchID string) (string, error) {
	if !validLaunchID(launchID) {
		return "", fmt.Errorf("diagnostics: invalid launch ID %q", launchID)
	}
	dir := filepath.Join(paths.NativeDir, launchID)
	if !withinRoot(paths.NativeDir, dir) {
		return "", fmt.Errorf("diagnostics: native launch directory escaped root")
	}
	return dir, nil
}

// EnsureNativeLaunch creates one private harness-log directory.
func EnsureNativeLaunch(paths Paths, launchID string) (string, error) {
	dir, err := NativeLaunchDir(paths, launchID)
	if err != nil {
		return "", err
	}
	if err := paths.Ensure(); err != nil {
		return "", err
	}
	if err := ensurePrivateDirectory(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// InspectNative reports current storage without modifying it.
func InspectNative(paths Paths, options NativeOptions, active map[string]struct{}) (NativeReport, error) {
	return inspectOrMaintainNative(paths, options, active, false)
}

// MaintainNative applies age, per-launch, and aggregate limits to inactive
// launches. A launch ID in active is observation-only and may temporarily
// exceed both limits. Very recent directories receive the same short grace so
// concurrent launchers cannot prune one another before Kitty exposes both IDs.
func MaintainNative(paths Paths, options NativeOptions, active map[string]struct{}) (NativeReport, error) {
	if err := paths.Ensure(); err != nil {
		return NativeReport{}, err
	}
	lock, err := acquireFileLock(context.Background(), nativeMaintenanceLockPath(paths), time.Second)
	if err != nil {
		return NativeReport{}, err
	}
	defer releaseFileLock(lock)
	return inspectOrMaintainNative(paths, options, active, true)
}

func nativeMaintenanceLockPath(paths Paths) string {
	return filepath.Join(paths.Root, "native.lock")
}

func inspectOrMaintainNative(paths Paths, options NativeOptions, active map[string]struct{}, mutate bool) (NativeReport, error) {
	options = normalizeNativeOptions(options)
	if mutate {
		if err := paths.Ensure(); err != nil {
			return NativeReport{}, err
		}
	} else if _, err := os.Stat(paths.NativeDir); os.IsNotExist(err) {
		return NativeReport{}, nil
	} else if err != nil {
		return NativeReport{}, fmt.Errorf("diagnostics: inspecting native root: %w", err)
	}

	launches, scanErrors, err := scanNativeLaunches(paths.NativeDir, active, mutate)
	if err != nil {
		return NativeReport{}, err
	}
	report := NativeReport{Launches: len(launches), Errors: scanErrors}
	graceCutoff := options.Now().Add(-nativeLaunchGrace)
	for index := range launches {
		launch := &launches[index]
		launch.recent = launch.modified.After(graceCutoff)
		if launch.active {
			report.ActiveLaunches++
			if options.LaunchMaxBytes > 0 && launch.size > options.LaunchMaxBytes {
				report.ActiveExcessBytes += launch.size - options.LaunchMaxBytes
			}
		}
	}

	if mutate {
		pruneExpiredNative(&launches, options, &report)
		for index := range launches {
			if launches[index].active || launches[index].recent || options.LaunchMaxBytes <= 0 || launches[index].size <= options.LaunchMaxBytes {
				continue
			}
			trimNativeLaunch(&launches[index], options.LaunchMaxBytes, &report)
		}
		pruneAggregateNative(&launches, options.MaxBytes, &report)
	}

	report.TotalBytes = 0
	for _, launch := range launches {
		report.TotalBytes += launch.size
	}
	if options.MaxBytes > 0 && report.TotalBytes > options.MaxBytes {
		report.ExcessBytes = report.TotalBytes - options.MaxBytes
	}
	return report, nil
}

func normalizeNativeOptions(options NativeOptions) NativeOptions {
	if options.KeepDays == 0 {
		options.KeepDays = 14
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 1 << 30
	}
	if options.LaunchMaxBytes == 0 {
		options.LaunchMaxBytes = 128 << 20
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return options
}

func scanNativeLaunches(root string, active map[string]struct{}, restrictModes bool) ([]nativeLaunch, []string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, fmt.Errorf("diagnostics: reading native root: %w", err)
	}
	launches := make([]nativeLaunch, 0, len(entries))
	errorsFound := make([]string, 0)
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			errorsFound = append(errorsFound, infoErr.Error())
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			errorsFound = append(errorsFound, fmt.Sprintf("unexpected native entry %s", entry.Name()))
			continue
		}
		launch := nativeLaunch{id: entry.Name(), path: filepath.Join(root, entry.Name()), modified: info.ModTime()}
		_, launch.active = active[launch.id]
		walkErr := filepath.WalkDir(launch.path, func(path string, item fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			itemInfo, err := item.Info()
			if err != nil {
				return err
			}
			if item.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink is not allowed: %s", path)
			}
			if item.IsDir() {
				if restrictModes && !launch.active {
					if err := os.Chmod(path, directoryMode); err != nil {
						return err
					}
				}
				return nil
			}
			if !itemInfo.Mode().IsRegular() {
				return fmt.Errorf("non-regular native entry: %s", path)
			}
			if restrictModes && !launch.active && orphanNativeTrimTemp(path) {
				return os.Remove(path)
			}
			if restrictModes && !launch.active {
				if err := os.Chmod(path, fileMode); err != nil {
					return err
				}
			}
			launch.files = append(launch.files, nativeFile{path: path, size: itemInfo.Size(), modified: itemInfo.ModTime()})
			launch.size += itemInfo.Size()
			if itemInfo.ModTime().After(launch.modified) {
				launch.modified = itemInfo.ModTime()
			}
			return nil
		})
		if walkErr != nil {
			errorsFound = append(errorsFound, walkErr.Error())
			continue
		}
		launches = append(launches, launch)
	}
	return launches, errorsFound, nil
}

func pruneExpiredNative(launches *[]nativeLaunch, options NativeOptions, report *NativeReport) {
	if options.KeepDays <= 0 {
		return
	}
	cutoff := options.Now().Add(-time.Duration(options.KeepDays) * 24 * time.Hour)
	kept := (*launches)[:0]
	for _, launch := range *launches {
		if launch.active || launch.recent || !launch.modified.Before(cutoff) {
			kept = append(kept, launch)
			continue
		}
		if err := os.RemoveAll(launch.path); err != nil {
			report.Errors = append(report.Errors, err.Error())
			kept = append(kept, launch)
			continue
		}
		report.RemovedLaunches++
		report.RemovedFiles += len(launch.files)
		report.RemovedBytes += launch.size
	}
	*launches = kept
}

func trimNativeLaunch(launch *nativeLaunch, target int64, report *NativeReport) {
	sort.Slice(launch.files, func(i, j int) bool {
		return launch.files[i].modified.Before(launch.files[j].modified)
	})
	kept := make([]nativeFile, 0, len(launch.files))
	for index, file := range launch.files {
		remaining := launch.size - target
		if remaining <= 0 {
			kept = append(kept, launch.files[index:]...)
			break
		}
		if file.size <= remaining {
			if err := os.Remove(file.path); err != nil {
				report.Errors = append(report.Errors, err.Error())
				kept = append(kept, file)
				continue
			}
			launch.size -= file.size
			report.RemovedFiles++
			report.RemovedBytes += file.size
			continue
		}
		newSize := file.size - remaining
		if err := retainFileTail(file.path, newSize); err != nil {
			report.Errors = append(report.Errors, err.Error())
			kept = append(kept, file)
			continue
		}
		launch.size -= remaining
		report.RemovedBytes += remaining
		report.TrimmedFiles++
		file.size = newSize
		kept = append(kept, file)
		kept = append(kept, launch.files[index+1:]...)
		break
	}
	launch.files = kept
}

func retainFileTail(path string, size int64) error {
	if size < 0 {
		size = 0
	}
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if size >= info.Size() {
		return nil
	}
	if _, err := source.Seek(info.Size()-size, io.SeekStart); err != nil {
		return err
	}
	// A fixed "<log>.trim" name rather than internal/atomicfile's unique temp:
	// orphanNativeTrimTemp recognizes and sweeps exactly this name after a
	// crash, and the O_EXCL open below refuses a planted one.
	tempPath := path + ".trim"
	if err := validateRegularTarget(tempPath); err != nil {
		return err
	}
	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	temp, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	tempPath = temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := io.Copy(temp, source); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func orphanNativeTrimTemp(path string) bool {
	if !strings.HasSuffix(path, ".trim") {
		return false
	}
	info, err := os.Lstat(strings.TrimSuffix(path, ".trim"))
	return err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular()
}

func pruneAggregateNative(launches *[]nativeLaunch, target int64, report *NativeReport) {
	if target <= 0 {
		return
	}
	total := int64(0)
	for _, launch := range *launches {
		total += launch.size
	}
	if total <= target {
		return
	}
	sort.SliceStable(*launches, func(i, j int) bool {
		return (*launches)[i].modified.Before((*launches)[j].modified)
	})
	kept := make([]nativeLaunch, 0, len(*launches))
	for _, launch := range *launches {
		if total <= target || launch.active || launch.recent {
			kept = append(kept, launch)
			continue
		}
		if err := os.RemoveAll(launch.path); err != nil {
			report.Errors = append(report.Errors, err.Error())
			kept = append(kept, launch)
			continue
		}
		total -= launch.size
		report.RemovedLaunches++
		report.RemovedFiles += len(launch.files)
		report.RemovedBytes += launch.size
	}
	*launches = kept
}

func validLaunchID(value string) bool {
	if len(value) != 36 || strings.Count(value, "-") != 4 {
		return false
	}
	for index, r := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
