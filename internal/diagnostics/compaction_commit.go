package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func (j *Journal) prepareCompactionOutputs(ctx context.Context, records []compactionRecord) (outputs []*preparedCompactionOutput, retErr error) {
	defer func() {
		if retErr != nil {
			closePreparedCompactionOutputs(outputs)
		}
	}()
	var current *preparedCompactionOutput
	for _, record := range records {
		if err := ctxError(ctx); err != nil {
			return outputs, err
		}
		lineSize := int64(len(record.raw) + 1)
		if current == nil || current.size > 0 && current.size+lineSize > MaxSegmentBytes {
			output, err := j.createCompactionOutput()
			if err != nil {
				return outputs, err
			}
			outputs = append(outputs, output)
			current = output
		}
		written, err := current.file.Write(record.raw)
		if err == nil && written != len(record.raw) {
			err = io.ErrShortWrite
		}
		if err == nil {
			var newlineWritten int
			newlineWritten, err = current.file.Write([]byte{'\n'})
			if err == nil && newlineWritten != 1 {
				err = io.ErrShortWrite
			}
		}
		if err != nil {
			return outputs, err
		}
		current.size += lineSize
	}
	for _, output := range outputs {
		if err := output.file.Sync(); err != nil {
			return outputs, err
		}
	}
	return outputs, nil
}

func (j *Journal) createCompactionOutput() (*preparedCompactionOutput, error) {
	file, err := os.CreateTemp(j.paths.JournalDir, compactionTempPrefix+"*.tmp")
	if err != nil {
		return nil, err
	}
	tempPath := file.Name()
	fail := func(err error) (*preparedCompactionOutput, error) {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		_ = os.Remove(tempPath)
		return nil, err
	}
	if err := file.Chmod(fileMode); err != nil {
		return fail(err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return fail(err)
	}
	finalPath := filepath.Join(j.paths.JournalDir, fmt.Sprintf(
		"events-compacted-%s-%d-%06d.jsonl", timestamp(j.opts.Now()), os.Getpid(), rotationSequence.Add(1),
	))
	return &preparedCompactionOutput{file: file, tempPath: tempPath, finalPath: finalPath}, nil
}

func closePreparedCompactionOutputs(outputs []*preparedCompactionOutput) {
	for _, output := range outputs {
		if output == nil {
			continue
		}
		if output.file != nil {
			_ = unix.Flock(int(output.file.Fd()), unix.LOCK_UN)
			_ = output.file.Close()
			output.file = nil
		}
		if !output.published {
			_ = os.Remove(output.tempPath)
		}
	}
}

func compactionOutputPaths(outputs []*preparedCompactionOutput, published bool) []string {
	paths := make([]string, 0, len(outputs))
	for _, output := range outputs {
		if published {
			paths = append(paths, output.finalPath)
		} else {
			paths = append(paths, output.tempPath)
		}
	}
	return paths
}

// commitCompaction revalidates inputs, publishes outputs, and removes or
// quarantines every input while holding one continuous exclusive lock. A
// second process therefore cannot validate the same inputs after publication
// but before cleanup and publish a duplicate copy.
func (j *Journal) commitCompaction(ctx context.Context, candidates []compactionCandidate, outputs []*preparedCompactionOutput, report *MaintenanceReport, hooks compactionHooks) (bool, error) {
	j.sealedMu.Lock()
	defer j.sealedMu.Unlock()
	lock, err := acquireFileLock(ctx, j.lock, j.opts.MaintenanceLockTimeout)
	if err != nil {
		return false, err
	}
	defer releaseFileLock(lock)
	if err := ctxError(ctx); err != nil {
		return false, err
	}
	valid, err := validateCompactionCandidates(candidates)
	if err != nil || !valid {
		return valid, err
	}

	published := make([]string, 0, len(outputs))
	for _, output := range outputs {
		if _, err := os.Lstat(output.finalPath); err == nil {
			return false, fmt.Errorf("diagnostics: compaction output already exists: %s", output.finalPath)
		} else if !os.IsNotExist(err) {
			return false, err
		}
		if err := os.Rename(output.tempPath, output.finalPath); err != nil {
			if len(published) > 0 {
				err = errors.Join(err, syncDirectory(j.paths.JournalDir))
			}
			return false, err
		}
		output.published = true
		published = append(published, output.finalPath)
		if hooks.afterOutputRename != nil {
			if err := hooks.afterOutputRename(output.finalPath); err != nil {
				return false, err
			}
		}
	}
	if len(published) > 0 {
		if err := syncDirectory(j.paths.JournalDir); err != nil {
			return false, err
		}
	}
	if err := ctxError(ctx); err != nil {
		return false, err
	}

	changedDirectory := false
	for _, candidate := range candidates {
		if err := ctxError(ctx); err != nil {
			return false, err
		}
		cleanedPath := candidate.path
		if candidate.corrupt {
			cleanedPath = candidate.path + ".partial-corrupt-" + timestamp(j.opts.Now()) + fmt.Sprintf("-%06d", rotationSequence.Add(1))
			if err := os.Rename(candidate.path, cleanedPath); err != nil {
				return false, err
			}
			report.QuarantinedFiles = append(report.QuarantinedFiles, cleanedPath)
		} else if err := os.Remove(candidate.path); err != nil {
			return false, err
		}
		changedDirectory = true
		if hooks.afterInputCleanup != nil {
			if err := hooks.afterInputCleanup(cleanedPath); err != nil {
				return false, err
			}
		}
	}
	if changedDirectory {
		if err := syncDirectory(j.paths.JournalDir); err != nil {
			return false, err
		}
	}
	return true, nil
}

func validateCompactionCandidates(candidates []compactionCandidate) (bool, error) {
	for _, candidate := range candidates {
		if err := validateRegularTarget(candidate.path); err != nil {
			return false, err
		}
		info, err := os.Lstat(candidate.path)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !sameCompactionCandidate(candidate, info) {
			return false, nil
		}
	}
	return true, nil
}

func (j *Journal) cleanupCompactionTemps(ctx context.Context) error {
	entries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if err := ctxError(ctx); err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, compactionTempPrefix) || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		path := filepath.Join(j.paths.JournalDir, name)
		if err := validateRegularTarget(path); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_RDWR, fileMode)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			_ = file.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			return err
		}
		lockedInfo, statErr := file.Stat()
		pathInfo, pathErr := os.Stat(path)
		if statErr == nil && pathErr == nil && os.SameFile(lockedInfo, pathInfo) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
				return err
			}
			removed = true
		}
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}
	if removed {
		return syncDirectory(j.paths.JournalDir)
	}
	return nil
}
