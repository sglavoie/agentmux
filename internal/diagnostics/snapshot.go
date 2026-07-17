package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// SnapshotStatistics describes the bounded filesystem work performed by a
// journal snapshot. RecordsMatched is measured before a selector limit is
// applied, so callers can distinguish selection from output truncation.
type SnapshotStatistics struct {
	SegmentFilesRead  int   `json:"segment_files_read"`
	SpoolFilesRead    int   `json:"spool_files_read"`
	BytesScanned      int64 `json:"bytes_scanned"`
	RecordsScanned    int   `json:"records_scanned"`
	RecordsMatched    int   `json:"records_matched"`
	DuplicatesOmitted int   `json:"duplicates_omitted"`
	CorruptLines      int   `json:"corrupt_lines"`
}

type snapshotFileIdentity struct {
	device uint64
	inode  uint64
}

// Keep an offset across one capture that misses its file so a ReadDir/open
// rename window does not replay the file when it reappears. A second
// consecutive miss prunes the stale identity.
const snapshotCursorMissingCaptureGrace = 1

// SnapshotCursor is an opaque per-query cursor. Its inode offsets allow a
// caller to poll only file deltas while treating a renamed segment or promoted
// spool as the same underlying file.
type SnapshotCursor struct {
	offsets         map[snapshotFileIdentity]int64
	missingCaptures map[snapshotFileIdentity]int
}

// JournalSnapshot is a tolerant, read-only view of journal and spool evidence.
// Cursor can be passed to a later Snapshot call to read only newly captured
// byte ranges.
type JournalSnapshot struct {
	Records    []TraceEnvelope    `json:"records"`
	Health     JournalHealth      `json:"health"`
	Statistics SnapshotStatistics `json:"journal_snapshot"`
	Cursor     SnapshotCursor     `json:"-"`
}

// SnapshotVisitor observes each valid, deduplicated record in journal file
// order before selector filtering and result limiting are applied.
type SnapshotVisitor func(TraceEnvelope) error

type snapshotFileKind uint8

const (
	snapshotSegment snapshotFileKind = iota
	snapshotSpool
)

type snapshotFile struct {
	file     *os.File
	path     string
	identity snapshotFileIdentity
	kind     snapshotFileKind
	start    int64
	limit    int64
	sealed   bool
}

type snapshotRecordIdentity struct {
	sourceID       string
	sourceSequence uint64
	legacy         [sha256.Size]byte
	hasSource      bool
}

// Snapshot captures fixed byte prefixes for all journal segments and active or
// claimed spool files. Descriptor reads and JSON parsing happen after both the
// in-process mutex and shared filesystem lock have been released.
//
// A nil cursor performs a full scan. Passing the cursor returned by a previous
// call reads only file deltas. Cursors are tied to a stable selector stream;
// callers should start with nil when changing selectors.
func (j *Journal) Snapshot(ctx context.Context, selector Selector, cursor *SnapshotCursor) (JournalSnapshot, error) {
	return j.SnapshotWithVisitor(ctx, selector, cursor, nil)
}

// SnapshotWithVisitor captures a journal snapshot and calls visitor for every
// valid, deduplicated record in the captured file prefixes. The visitor runs
// after the journal mutex and filesystem lock have been released. A visitor
// error stops traversal, and the returned snapshot has no advancing cursor.
func (j *Journal) SnapshotWithVisitor(ctx context.Context, selector Selector, cursor *SnapshotCursor, visitor SnapshotVisitor) (JournalSnapshot, error) {
	return j.snapshotWithVisitor(ctx, selector, cursor, visitor, nil)
}

// snapshot exposes the post-capture boundary to deterministic concurrency
// tests. Production callers always pass a nil hook.
func (j *Journal) snapshot(ctx context.Context, selector Selector, cursor *SnapshotCursor, afterCapture func()) (JournalSnapshot, error) {
	return j.snapshotWithVisitor(ctx, selector, cursor, nil, afterCapture)
}

func (j *Journal) snapshotWithVisitor(ctx context.Context, selector Selector, cursor *SnapshotCursor, visitor SnapshotVisitor, afterCapture func()) (JournalSnapshot, error) {
	var snapshot JournalSnapshot
	if j == nil {
		return snapshot, errors.New("diagnostics: nil journal")
	}
	files, health, err := j.captureSnapshotFiles(ctx, cursor)
	if err != nil {
		return snapshot, err
	}
	snapshot.Health = health
	offsetCapacity := len(files)
	if cursor != nil {
		offsetCapacity += len(cursor.offsets)
	}
	nextCursor := SnapshotCursor{
		offsets:         make(map[snapshotFileIdentity]int64, offsetCapacity),
		missingCaptures: make(map[snapshotFileIdentity]int),
	}
	if cursor != nil {
		capturedIdentities := make(map[snapshotFileIdentity]struct{}, len(files))
		for _, captured := range files {
			capturedIdentities[captured.identity] = struct{}{}
		}
		for identity, offset := range cursor.offsets {
			if _, captured := capturedIdentities[identity]; captured {
				continue
			}
			missing := cursor.missingCaptures[identity] + 1
			if missing > snapshotCursorMissingCaptureGrace {
				continue
			}
			nextCursor.offsets[identity] = offset
			nextCursor.missingCaptures[identity] = missing
		}
	}
	defer closeSnapshotFiles(files)

	if afterCapture != nil {
		afterCapture()
	}
	if err := ctxError(ctx); err != nil {
		return snapshot, err
	}

	seen := make(map[snapshotRecordIdentity]struct{})
	schemas := make(map[int]struct{})
	for _, captured := range files {
		if captured.kind == snapshotSegment {
			snapshot.Statistics.SegmentFilesRead++
		} else {
			snapshot.Statistics.SpoolFilesRead++
		}
		end, bytesRead, corrupt, readErr := scanSnapshotFile(ctx, captured, func(raw []byte, envelope TraceEnvelope) error {
			snapshot.Statistics.RecordsScanned++
			identity := snapshotIdentity(raw, envelope)
			if _, duplicate := seen[identity]; duplicate {
				snapshot.Statistics.DuplicatesOmitted++
				return nil
			}
			seen[identity] = struct{}{}
			snapshot.Health.Records++
			schemas[envelope.SchemaVersion] = struct{}{}
			if envelope.ObservedAt.After(snapshot.Health.LatestObservedAt) {
				snapshot.Health.LatestObservedAt = envelope.ObservedAt
			}
			if visitor != nil {
				if err := visitor(envelope); err != nil {
					return fmt.Errorf("diagnostics: snapshot visitor: %w", err)
				}
			}
			if selector.matches(envelope) {
				snapshot.Records = append(snapshot.Records, envelope)
			}
			return nil
		})
		snapshot.Statistics.BytesScanned += bytesRead
		snapshot.Statistics.CorruptLines += corrupt
		if readErr != nil {
			return snapshot, readErr
		}
		nextCursor.offsets[captured.identity] = end
	}

	snapshot.Health.CorruptLines = snapshot.Statistics.CorruptLines
	for schema := range schemas {
		snapshot.Health.ObservedSchemas = append(snapshot.Health.ObservedSchemas, schema)
	}
	sort.Ints(snapshot.Health.ObservedSchemas)
	sort.SliceStable(snapshot.Records, func(i, k int) bool {
		return snapshotRecordLess(snapshot.Records[i], snapshot.Records[k])
	})
	snapshot.Statistics.RecordsMatched = len(snapshot.Records)
	if selector.Limit > 0 && len(snapshot.Records) > selector.Limit {
		snapshot.Records = snapshot.Records[len(snapshot.Records)-selector.Limit:]
	}
	snapshot.Cursor = nextCursor
	return snapshot, nil
}

func (j *Journal) captureSnapshotFiles(ctx context.Context, cursor *SnapshotCursor) ([]snapshotFile, JournalHealth, error) {
	var health JournalHealth
	j.mu.Lock()
	defer j.mu.Unlock()
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

	journalEntries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		return closeOnError(err)
	}
	for _, entry := range journalEntries {
		if err := ctxError(ctx); err != nil {
			return closeOnError(err)
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
		health.Files++
		health.Bytes += file.limit
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

func isSnapshotSegmentName(name string) bool {
	return name == "events.jsonl" || strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")
}

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

func snapshotIdentity(raw []byte, envelope TraceEnvelope) snapshotRecordIdentity {
	if envelope.SourceID != "" && envelope.SourceSequence != 0 {
		return snapshotRecordIdentity{
			sourceID: envelope.SourceID, sourceSequence: envelope.SourceSequence, hasSource: true,
		}
	}
	return snapshotRecordIdentity{legacy: sha256.Sum256(raw)}
}

func snapshotRecordLess(left, right TraceEnvelope) bool {
	if !left.ObservedAt.Equal(right.ObservedAt) {
		return left.ObservedAt.Before(right.ObservedAt)
	}
	if left.SourceID != right.SourceID {
		return left.SourceID < right.SourceID
	}
	if left.SourceSequence != right.SourceSequence {
		return left.SourceSequence < right.SourceSequence
	}
	if left.SpanID != right.SpanID {
		return left.SpanID < right.SpanID
	}
	return left.TraceID < right.TraceID
}

func closeSnapshotFiles(files []snapshotFile) {
	for i := range files {
		if files[i].file != nil {
			_ = files[i].file.Close()
		}
	}
}
