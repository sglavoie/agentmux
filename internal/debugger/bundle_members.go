package debugger

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func doctorSnapshot(status *StatusReport) any {
	if status == nil {
		return map[string]any{"schema_version": 1, "availability": "unavailable"}
	}
	return map[string]any{
		"schema_version": 1, "generated_at": status.GeneratedAt, "healthy": status.Healthy,
		"daemon": status.Daemon, "findings": status.Findings,
		"source": "bundle_offline_snapshot",
	}
}

func versionSnapshot(cli buildinfo.Info, status *StatusReport) any {
	result := map[string]any{"schema_version": BundleSchemaVersion, "cli": cli}
	if status != nil && status.Daemon.Build != nil {
		result["daemon"] = status.Daemon.Build
		result["build_matches"] = status.Daemon.BuildMatches
	}
	return result
}

func configSnapshot(cfg *config.Config, configErr error) any {
	if configErr != nil || cfg == nil {
		message := "configuration unavailable"
		if configErr != nil {
			message = configErr.Error()
		}
		return map[string]any{"schema_version": BundleSchemaVersion, "availability": "unavailable", "error": message}
	}
	harnessCounts := make(map[string]int)
	for _, profile := range cfg.Profiles {
		harnessCounts[profile.EffectiveHarness()]++
	}
	type harnessCount struct {
		Harness string `json:"harness"`
		Count   int    `json:"count"`
	}
	counts := make([]harnessCount, 0, len(harnessCounts))
	for harnessName, count := range harnessCounts {
		counts = append(counts, harnessCount{Harness: harnessName, Count: count})
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i].Harness < counts[j].Harness })
	d := cfg.Defaults.Diagnostics
	return map[string]any{
		"schema_version": BundleSchemaVersion, "availability": "available",
		"profile_count": len(cfg.Profiles), "project_count": len(cfg.Projects), "harness_counts": counts,
		"diagnostics": map[string]any{
			"journal_enabled": d.JournalEnabledValue(), "journal_keep_days": d.JournalKeepDays, "journal_max_mib": d.JournalMaxMiB,
			"native_logging": d.NativeLoggingValue(), "native_keep_days": d.NativeKeepDays,
			"native_max_mib": d.NativeMaxMiB, "native_session_max_mib": d.NativeSessionMaxMiB,
		},
	}
}

func redactedNativeExcerpts(paths diagnostics.Paths, sessions []NativeSession, selector Selector, redactor *bundleRedactor) ([]byte, int, bool, error) {
	candidates := make(map[string]struct{})
	for _, session := range sessions {
		if session.Coverage != "managed" || !nativeSessionMatches(session, selector) || strings.TrimSpace(session.LogPath) == "" {
			continue
		}
		path := filepath.Clean(session.LogPath)
		if pathWithin(paths.NativeDir, path) {
			candidates[path] = struct{}{}
		}
	}
	// Offline bundles may not have Kitty metadata. The managed native root is
	// still owned by Agent Mux, so an unscoped request may inspect it safely.
	if len(candidates) == 0 && selector.WindowID == 0 && selector.SessionID == "" && selector.ASLRunID == "" && selector.Harness == "" && selector.Profile == "" {
		candidates[paths.NativeDir] = struct{}{}
	}
	files := make([]string, 0)
	for candidate := range candidates {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, false, err
		}
		if info.Mode().IsRegular() {
			files = append(files, candidate)
			continue
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		err = filepath.WalkDir(candidate, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == candidate {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || !pathWithin(paths.NativeDir, path) {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, 0, false, err
		}
	}
	sort.Strings(files)
	var output strings.Builder
	var readBytes int64
	truncated := false
	for _, path := range files {
		if readBytes >= maxNativeTotalBytes {
			truncated = true
			break
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			continue
		}
		start := int64(0)
		limit := info.Size()
		if limit > maxNativeFileBytes {
			start = limit - maxNativeFileBytes
			limit = maxNativeFileBytes
			truncated = true
		}
		if remaining := maxNativeTotalBytes - readBytes; limit > remaining {
			start += limit - remaining
			limit = remaining
			truncated = true
		}
		_, _ = file.Seek(start, io.SeekStart)
		scanner := bufio.NewScanner(io.LimitReader(file, limit))
		scanner.Buffer(make([]byte, 64<<10), int(maxNativeFileBytes))
		for scanner.Scan() {
			if line := redactor.nativeLine(scanner.Text()); line != "" {
				output.WriteString(line)
				output.WriteByte('\n')
			}
		}
		_ = file.Close()
		readBytes += limit
	}
	return []byte(output.String()), len(files), truncated, nil
}

func pathWithin(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func normalizeBundleOutput(path string, now time.Time, mode BundleMode) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = fmt.Sprintf("agent-mux-debug-%s-%s.tar.gz", now.Format("20060102T150405Z"), mode)
	}
	if !strings.HasSuffix(strings.ToLower(path), ".tar.gz") {
		path += ".tar.gz"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func writeBundleAtomic(path string, now time.Time, members []bundleMember) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return fmt.Errorf("debugger: unsafe bundle output directory: %s is not a real directory", directory)
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("debugger: bundle target %s is not a regular file", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	sort.Slice(members, func(i, j int) bool { return members[i].name < members[j].name })
	// The archive is encoded straight into atomicfile's temp. The explicit
	// target check above keeps this package's message; RequireRegularTarget
	// repeats it as close to the temp's creation as the shared code can.
	return atomicfile.WriteStream(path, 0o600, func(w io.Writer) error {
		gzipWriter := gzip.NewWriter(w)
		gzipWriter.ModTime = now
		gzipWriter.OS = 255
		tarWriter := tar.NewWriter(gzipWriter)
		for _, member := range members {
			header := &tar.Header{
				Name: member.name, Mode: 0o600, Size: int64(len(member.data)), ModTime: now,
				AccessTime: time.Time{}, ChangeTime: time.Time{}, Uid: 0, Gid: 0, Uname: "", Gname: "",
				Format: tar.FormatPAX,
			}
			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}
			if _, err := tarWriter.Write(member.data); err != nil {
				return err
			}
		}
		return errors.Join(tarWriter.Close(), gzipWriter.Close())
	}, atomicfile.NoCreateParents(), atomicfile.RequireRegularTarget())
}
