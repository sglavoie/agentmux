package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	maxCompactionInputBytes int64 = 64 << 20
	maxCompactionInputFiles       = 2048
)

const compactionTempPrefix = ".events-compacting-"

type compactionHooks struct {
	afterPreparation  func([]string) error
	afterOutputRename func(string) error
	afterPublication  func([]string) error
	afterInputCleanup func(string) error
}

type compactionCandidate struct {
	path    string
	name    string
	size    int64
	modTime int64
	info    os.FileInfo
	corrupt bool
}

type compactionRecord struct {
	raw      []byte
	envelope TraceEnvelope
	order    int
}

type preparedCompactionOutput struct {
	file      *os.File
	tempPath  string
	finalPath string
	size      int64
	published bool
}

// Compact incrementally merges the oldest sealed journal segments. It never
// rewrites the active journal and leaves scheduling to the daemon maintainer.
func (j *Journal) Compact(ctx context.Context) (MaintenanceReport, error) {
	return j.compact(ctx, compactionHooks{})
}

func (j *Journal) compact(ctx context.Context, hooks compactionHooks) (MaintenanceReport, error) {
	var report MaintenanceReport
	if j == nil {
		return report, errors.New("diagnostics: nil journal")
	}
	if err := ctxError(ctx); err != nil {
		return report, err
	}
	if err := j.cleanupCompactionTemps(ctx); err != nil {
		return report, err
	}

	var after *compactionCandidate
	for {
		candidates, err := j.captureCompactionCandidatesAfter(ctx, after)
		if err != nil || len(candidates) < 2 {
			return report, err
		}
		records, duplicates, corruptLines, changed, err := readCompactionCandidates(ctx, candidates)
		report.CorruptLines += corruptLines
		if err != nil {
			return report, err
		}
		if changed {
			return report, nil
		}

		corruptFiles := 0
		for i := range candidates {
			if candidates[i].corrupt {
				corruptFiles++
			}
		}
		// Compute the exact partition count from record bytes before creating any
		// temporary output. A duplicate-free, corruption-free window that cannot
		// reduce file count is a proven no-op; advance past it so newer tiny
		// segments do not remain permanently hidden behind a packed prefix.
		if countCompactionOutputs(records) >= len(candidates) && duplicates == 0 && corruptFiles == 0 {
			last := candidates[len(candidates)-1]
			after = &last
			continue
		}

		outputs, err := j.prepareCompactionOutputs(ctx, records)
		if err != nil {
			return report, err
		}
		defer closePreparedCompactionOutputs(outputs)

		tempPaths := compactionOutputPaths(outputs, false)
		if hooks.afterPreparation != nil {
			if err := hooks.afterPreparation(tempPaths); err != nil {
				return report, err
			}
		}
		if err := ctxError(ctx); err != nil {
			return report, err
		}

		committed, err := j.commitCompaction(ctx, candidates, outputs, &report, hooks)
		if err != nil {
			return report, err
		}
		if !committed {
			return report, nil
		}
		if hooks.afterPublication != nil {
			if err := hooks.afterPublication(compactionOutputPaths(outputs, true)); err != nil {
				return report, err
			}
		}
		report.CompactionInputFiles = len(candidates)
		for _, candidate := range candidates {
			report.CompactionInputBytes += candidate.size
		}
		report.CompactionDuplicatesOmitted = duplicates
		for _, output := range outputs {
			if output.published {
				report.CompactionOutputFiles++
				report.CompactionOutputBytes += output.size
			}
		}
		return report, nil
	}
}

func (j *Journal) captureCompactionCandidates(ctx context.Context) ([]compactionCandidate, error) {
	return j.captureCompactionCandidatesAfter(ctx, nil)
}

func (j *Journal) captureCompactionCandidatesAfter(ctx context.Context, after *compactionCandidate) ([]compactionCandidate, error) {
	j.mu.Lock()
	lock, err := acquireSharedFileLock(ctx, j.lock, j.opts.LockTimeout)
	if err != nil {
		j.mu.Unlock()
		return nil, err
	}
	defer func() {
		releaseFileLock(lock)
		j.mu.Unlock()
	}()

	entries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		return nil, err
	}
	candidates := make([]compactionCandidate, 0, len(entries))
	for _, entry := range entries {
		if err := ctxError(ctx); err != nil {
			return nil, err
		}
		name := entry.Name()
		if entry.IsDir() || !isCompactionSegmentName(name) {
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
		if info.Size() > maxCompactionInputBytes {
			continue
		}
		candidates = append(candidates, compactionCandidate{
			path: path, name: name, size: info.Size(), modTime: info.ModTime().UnixNano(), info: info,
		})
	}
	sort.Slice(candidates, func(i, k int) bool {
		if candidates[i].modTime != candidates[k].modTime {
			return candidates[i].modTime < candidates[k].modTime
		}
		return candidates[i].name < candidates[k].name
	})

	selected := candidates[:0]
	var selectedBytes int64
	for _, candidate := range candidates {
		if after != nil && !compactionCandidateLess(*after, candidate) {
			continue
		}
		if selectedBytes+candidate.size > maxCompactionInputBytes {
			break
		}
		selected = append(selected, candidate)
		selectedBytes += candidate.size
		if len(selected) == maxCompactionInputFiles {
			break
		}
	}
	return selected, nil
}

func compactionCandidateLess(left, right compactionCandidate) bool {
	if left.modTime != right.modTime {
		return left.modTime < right.modTime
	}
	return left.name < right.name
}

func isCompactionSegmentName(name string) bool {
	return strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")
}

func readCompactionCandidates(ctx context.Context, candidates []compactionCandidate) ([]compactionRecord, int, int, bool, error) {
	var records []compactionRecord
	seen := make(map[snapshotRecordIdentity]struct{})
	duplicates := 0
	corruptLines := 0
	order := 0
	for i := range candidates {
		if err := ctxError(ctx); err != nil {
			return nil, duplicates, corruptLines, false, err
		}
		file, changed, err := openCompactionCandidate(candidates[i])
		if err != nil || changed {
			return nil, duplicates, corruptLines, changed, err
		}
		captured := snapshotFile{
			file: file, path: candidates[i].path, kind: snapshotSegment,
			limit: candidates[i].size, sealed: true,
		}
		_, _, corrupt, scanErr := scanSnapshotFile(ctx, captured, func(raw []byte, envelope TraceEnvelope) error {
			identity := snapshotIdentity(raw, envelope)
			if _, duplicate := seen[identity]; duplicate {
				duplicates++
				return nil
			}
			seen[identity] = struct{}{}
			records = append(records, compactionRecord{
				raw: append([]byte(nil), raw...), envelope: envelope, order: order,
			})
			order++
			return nil
		})
		closeErr := file.Close()
		corruptLines += corrupt
		candidates[i].corrupt = corrupt > 0
		if err := errors.Join(scanErr, closeErr); err != nil {
			return nil, duplicates, corruptLines, false, err
		}
	}
	sort.SliceStable(records, func(i, k int) bool {
		if snapshotRecordLess(records[i].envelope, records[k].envelope) {
			return true
		}
		if snapshotRecordLess(records[k].envelope, records[i].envelope) {
			return false
		}
		return records[i].order < records[k].order
	})
	return records, duplicates, corruptLines, false, nil
}

func openCompactionCandidate(candidate compactionCandidate) (*os.File, bool, error) {
	if err := validateRegularTarget(candidate.path); err != nil {
		return nil, false, err
	}
	file, err := os.Open(candidate.path)
	if os.IsNotExist(err) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, false, err
	}
	pathInfo, err := os.Lstat(candidate.path)
	if os.IsNotExist(err) {
		_ = file.Close()
		return nil, true, nil
	}
	if err != nil {
		_ = file.Close()
		return nil, false, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() ||
		!os.SameFile(info, pathInfo) || !sameCompactionCandidate(candidate, info) {
		_ = file.Close()
		return nil, true, nil
	}
	return file, false, nil
}

func sameCompactionCandidate(candidate compactionCandidate, current os.FileInfo) bool {
	return candidate.info != nil && current != nil && candidate.size == current.Size() && os.SameFile(candidate.info, current)
}

func countCompactionOutputs(records []compactionRecord) int {
	outputs := 0
	var outputBytes int64
	for _, record := range records {
		lineSize := int64(len(record.raw) + 1)
		if outputs == 0 || outputBytes > 0 && outputBytes+lineSize > MaxSegmentBytes {
			outputs++
			outputBytes = 0
		}
		outputBytes += lineSize
	}
	return outputs
}

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
	j.mu.Lock()
	defer j.mu.Unlock()
	lock, err := acquireFileLock(ctx, j.lock, j.opts.LockTimeout)
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
