package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/timeutil"
)

// RotateAndRetain rotates an oversized active journal and applies age and
// quota retention. The directory scan and retention calculation happen
// without storage locks; candidates are revalidated under a short exclusive
// lock immediately before removal.
func (j *Journal) RotateAndRetain(ctx context.Context) (MaintenanceReport, error) {
	var report MaintenanceReport
	if j == nil {
		return report, errors.New("diagnostics: nil journal")
	}
	if err := ctxError(ctx); err != nil {
		return report, err
	}
	// The oversized-active check mutates only the active segment, so it takes
	// the append side; this also serializes with Append's own size-triggered
	// rotation so two rotators never race on the same rename.
	j.appendMu.Lock()
	lock, err := acquireFileLock(ctx, j.appendLock, j.opts.MaintenanceLockTimeout)
	if err != nil {
		j.appendMu.Unlock()
		return report, err
	}
	if info, statErr := os.Stat(j.current); statErr == nil && info.Size() > j.opts.SegmentBytes {
		if _, err := j.rotateLocked(); err != nil {
			releaseFileLock(lock)
			j.appendMu.Unlock()
			return report, err
		}
		report.RotatedFiles++
	} else if statErr != nil && !os.IsNotExist(statErr) {
		releaseFileLock(lock)
		j.appendMu.Unlock()
		return report, statErr
	}
	releaseFileLock(lock)
	j.appendMu.Unlock()

	files, err := j.retentionFiles(ctx)
	if err != nil {
		return report, err
	}
	candidates := j.retentionCandidates(files)
	if len(candidates) == 0 {
		return report, nil
	}
	j.sealedMu.Lock()
	lock, err = acquireFileLock(ctx, j.lock, j.opts.MaintenanceLockTimeout)
	if err != nil {
		j.sealedMu.Unlock()
		return report, err
	}
	defer func() {
		releaseFileLock(lock)
		j.sealedMu.Unlock()
	}()
	for _, candidate := range candidates {
		if err := ctxError(ctx); err != nil {
			return report, err
		}
		info, err := os.Stat(candidate.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return report, err
		}
		if candidate.info == nil || !os.SameFile(candidate.info, info) {
			continue
		}
		if err := os.Remove(candidate.path); err != nil && !os.IsNotExist(err) {
			return report, err
		}
		report.RemovedFiles++
		report.RemovedBytes += candidate.size
	}
	if report.RemovedFiles > 0 {
		if err := syncDirectory(j.paths.JournalDir); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (j *Journal) rotateLocked() (string, error) {
	if _, err := os.Stat(j.current); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if err := validateRegularTarget(j.current); err != nil {
		return "", err
	}
	rotated := filepath.Join(j.paths.JournalDir, fmt.Sprintf(
		"events-%s-%d-%06d.jsonl", timestamp(j.opts.Now()), os.Getpid(), rotationSequence.Add(1),
	))
	if err := syncFile(j.current); err != nil {
		return "", fmt.Errorf("diagnostics: syncing journal before rotation: %w", err)
	}
	if err := os.Rename(j.current, rotated); err != nil {
		return "", fmt.Errorf("diagnostics: rotating journal: %w", err)
	}
	if err := syncDirectory(j.paths.JournalDir); err != nil {
		return rotated, err
	}
	return rotated, nil
}

type retainedFile struct {
	path      string
	name      string
	size      int64
	modTime   time.Time
	info      os.FileInfo
	protected bool
}

func (j *Journal) retentionCandidates(files []retainedFile) []retainedFile {
	var total int64
	for _, file := range files {
		total += file.size
	}
	cutoff := time.Time{}
	if j.opts.KeepDays > 0 {
		cutoff = timeutil.DaysAgo(j.opts.Now(), j.opts.KeepDays)
	}
	var candidates []retainedFile
	for _, file := range files {
		if file.path == j.current {
			continue
		}
		removeForAge := !cutoff.IsZero() && file.modTime.Before(cutoff)
		removeForSize := j.opts.MaxBytes > 0 && total > j.opts.MaxBytes
		if !removeForAge && !removeForSize {
			continue
		}
		total -= file.size
		candidates = append(candidates, file)
	}
	return candidates
}

func (j *Journal) retentionFiles(ctx context.Context) ([]retainedFile, error) {
	entries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		return nil, err
	}
	var files []retainedFile
	for _, entry := range entries {
		if err := ctxError(ctx); err != nil {
			return nil, err
		}
		name := entry.Name()
		if entry.IsDir() || !isSnapshotSegmentName(name) {
			continue
		}
		path := filepath.Join(j.paths.JournalDir, name)
		if err := validateRegularTarget(path); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		files = append(files, retainedFile{path: path, name: name, size: info.Size(), modTime: info.ModTime(), info: info})
	}
	sort.Slice(files, func(a, b int) bool {
		if files[a].modTime.Equal(files[b].modTime) {
			return files[a].name < files[b].name
		}
		return files[a].modTime.Before(files[b].modTime)
	})
	return files, nil
}

func (j *Journal) segmentFilesLocked(currentLast bool) ([]retainedFile, error) {
	entries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		return nil, err
	}
	var files []retainedFile
	for _, entry := range entries {
		name := entry.Name()
		isJournalSegment := name == "events.jsonl" || strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")
		if entry.IsDir() || !isJournalSegment {
			continue
		}
		path := filepath.Join(j.paths.JournalDir, name)
		if err := validateRegularTarget(path); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if err := os.Chmod(path, fileMode); err != nil {
			return nil, err
		}
		files = append(files, retainedFile{path: path, name: name, size: info.Size(), modTime: info.ModTime()})
	}
	sort.Slice(files, func(a, b int) bool {
		if currentLast {
			if files[a].path == j.current {
				return false
			}
			if files[b].path == j.current {
				return true
			}
		}
		if files[a].modTime.Equal(files[b].modTime) {
			return files[a].name < files[b].name
		}
		return files[a].modTime.Before(files[b].modTime)
	})
	return files, nil
}
