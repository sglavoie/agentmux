package diagnostics

// The two halves of a snapshot read. Capture runs under the storage locks and
// does the minimum that has to be serialized against appends: list the journal
// and spool directories, open each file, and pin a byte limit. Scanning then
// runs with every lock released, reading only the pinned prefix so a concurrent
// append can neither block the reader nor be half-observed. The record pipeline
// that consumes both lives in snapshot.go.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func (j *Journal) captureSnapshotFiles(ctx context.Context, cursor *SnapshotCursor, betweenEnumerateAndOpen func()) ([]snapshotFile, JournalHealth, error) {
	var health JournalHealth
	j.sealedMu.Lock()
	defer j.sealedMu.Unlock()
	lock, err := acquireSharedFileLock(ctx, j.lock, j.opts.LockTimeout)
	if err != nil {
		return nil, health, err
	}
	defer releaseFileLock(lock)

	var captured []snapshotFile
	closeOnError := func(err error) ([]snapshotFile, JournalHealth, error) {
		closeSnapshotFiles(captured)
		return nil, health, err
	}
	known := make(map[snapshotFileIdentity]struct{})

	if err := j.captureJournalSegments(ctx, cursor, known, &captured, &health, betweenEnumerateAndOpen); err != nil {
		return closeOnError(err)
	}

	spoolEntries, err := os.ReadDir(j.paths.SpoolDir)
	if err != nil {
		return closeOnError(err)
	}
	for _, entry := range spoolEntries {
		if err := ctxError(ctx); err != nil {
			return closeOnError(err)
		}
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if strings.Contains(name, ".partial-") {
			health.QuarantinedFiles++
		}
		isClaim := isSpoolClaimName(name)
		if !isClaim && !isSpoolSourceName(name) {
			continue
		}
		file, openErr := openSnapshotFile(filepath.Join(j.paths.SpoolDir, name), snapshotSpool, isClaim, cursor)
		if openErr != nil {
			return closeOnError(openErr)
		}
		if file == nil {
			continue
		}
		if _, duplicate := known[file.identity]; duplicate {
			_ = file.file.Close()
			continue
		}
		known[file.identity] = struct{}{}
		captured = append(captured, *file)
		health.SpoolFiles++
		health.SpoolBytes += file.limit
		info, statErr := file.file.Stat()
		if statErr != nil {
			return closeOnError(statErr)
		}
		if health.OldestSpoolModTime.IsZero() || info.ModTime().Before(health.OldestSpoolModTime) {
			health.OldestSpoolModTime = info.ModTime()
		}
		if isLegacySpoolClaimName(name) {
			health.LegacyClaimFiles++
		}
	}
	return captured, health, nil
}

// captureJournalSegments lists and opens the sealed and active journal
// segments while holding append.lock, so an in-flight append cannot rotate
// events.jsonl between the directory listing and the open of a segment.
// Without this serialization a rename could seal the active inode into a
// segment name we never listed, and the capture would silently omit every
// record in the just-rotated segment until a later snapshot. events.lock is
// already held by the caller, so taking append.lock here preserves the
// canonical events-before-append lock ordering (sealedMu before appendMu). The
// lock is released before the caller scans the spool directory, which no
// append-side rotation can perturb.
func (j *Journal) captureJournalSegments(ctx context.Context, cursor *SnapshotCursor, known map[snapshotFileIdentity]struct{}, captured *[]snapshotFile, health *JournalHealth, betweenEnumerateAndOpen func()) error {
	j.appendMu.Lock()
	defer j.appendMu.Unlock()
	appendLock, err := acquireSharedFileLock(ctx, j.appendLock, j.opts.LockTimeout)
	if err != nil {
		return err
	}
	defer releaseFileLock(appendLock)

	journalEntries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		return err
	}
	if betweenEnumerateAndOpen != nil {
		betweenEnumerateAndOpen()
	}
	for _, entry := range journalEntries {
		if err := ctxError(ctx); err != nil {
			return err
		}
		name := entry.Name()
		if !entry.IsDir() && strings.Contains(name, ".partial-") {
			health.QuarantinedFiles++
		}
		if entry.IsDir() || !isSnapshotSegmentName(name) {
			continue
		}
		file, openErr := openSnapshotFile(filepath.Join(j.paths.JournalDir, name), snapshotSegment, name != "events.jsonl", cursor)
		if openErr != nil {
			return openErr
		}
		if file == nil {
			continue
		}
		if _, duplicate := known[file.identity]; duplicate {
			_ = file.file.Close()
			continue
		}
		known[file.identity] = struct{}{}
		*captured = append(*captured, *file)
		health.Files++
		health.Bytes += file.limit
	}
	return nil
}

func isSnapshotSegmentName(name string) bool {
	return name == "events.jsonl" || strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")
}

// openSnapshotFile opens a candidate and proves it is the same regular file the
// directory listing named, then pins its size as the capture limit. A path that
// turned into a symlink, or was replaced between the open and the lstat, yields
// no descriptor rather than an error: the file simply is not part of this
// snapshot. A cursor offset is honoured only when the preceding byte is a
// newline, so a truncated or recycled inode restarts from zero instead of
// resuming mid-record.
func openSnapshotFile(path string, kind snapshotFileKind, sealed bool, cursor *SnapshotCursor) (*snapshotFile, error) {
	if err := validateRegularTarget(path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("diagnostics: %s is not a regular file", path)
	}
	pathInfo, err := os.Lstat(path)
	if os.IsNotExist(err) {
		_ = file.Close()
		return nil, nil
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("diagnostics: %s is not a regular file", path)
	}
	if !os.SameFile(info, pathInfo) {
		_ = file.Close()
		return nil, nil
	}
	identity, err := snapshotIdentityFromInfo(info)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	start := int64(0)
	if cursor != nil && cursor.offsets != nil {
		start = cursor.offsets[identity]
		if start < 0 || start > info.Size() {
			start = 0
		}
		if start > 0 {
			var previous [1]byte
			if _, readErr := file.ReadAt(previous[:], start-1); readErr != nil {
				_ = file.Close()
				return nil, fmt.Errorf("diagnostics: validating snapshot cursor for %s: %w", path, readErr)
			}
			if previous[0] != '\n' {
				start = 0
			}
		}
	}
	return &snapshotFile{
		file: file, path: path, identity: identity, kind: kind,
		start: start, limit: info.Size(), sealed: sealed,
	}, nil
}

func snapshotIdentityFromInfo(info os.FileInfo) (snapshotFileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return snapshotFileIdentity{}, errors.New("diagnostics: file identity is unavailable")
	}
	return snapshotFileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}

// scanSnapshotFile parses the captured prefix with every lock released. A line
// that does not decode is counted, never fatal, and a trailing line without its
// newline is only consumed when the file is sealed — on an active file that
// line is a half-written append the next snapshot will read whole.
func scanSnapshotFile(ctx context.Context, captured snapshotFile, visit func([]byte, TraceEnvelope) error) (int64, int64, int, error) {
	end := captured.start
	if captured.start == captured.limit {
		return end, 0, 0, nil
	}
	reader := bufio.NewReader(io.NewSectionReader(captured.file, captured.start, captured.limit-captured.start))
	var bytesRead int64
	corrupt := 0
	for {
		if err := ctxError(ctx); err != nil {
			return end, bytesRead, corrupt, err
		}
		line, err := reader.ReadBytes('\n')
		bytesRead += int64(len(line))
		complete := len(line) > 0 && line[len(line)-1] == '\n'
		if complete || captured.sealed && len(line) > 0 {
			end += int64(len(line))
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				var envelope TraceEnvelope
				if decodeErr := json.Unmarshal(trimmed, &envelope); decodeErr != nil {
					corrupt++
				} else {
					if visitErr := visit(trimmed, envelope); visitErr != nil {
						return end, bytesRead, corrupt, visitErr
					}
					if ctxErr := ctxError(ctx); ctxErr != nil {
						return end, bytesRead, corrupt, ctxErr
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return end, bytesRead, corrupt, fmt.Errorf("diagnostics: reading snapshot %s: %w", captured.path, err)
		}
	}
	return end, bytesRead, corrupt, nil
}

func closeSnapshotFiles(files []snapshotFile) {
	for i := range files {
		if files[i].file != nil {
			_ = files[i].file.Close()
		}
	}
}
