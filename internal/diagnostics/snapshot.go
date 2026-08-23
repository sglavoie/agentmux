package diagnostics

// Tolerant read-only views over journal and spool evidence. A snapshot captures
// fixed byte prefixes under lock and then does every descriptor read and JSON
// parse with the locks released, so reading diagnostics can never block an
// append. Corrupt lines are counted rather than fatal. The capture and scan
// mechanics live in snapshot_capture.go.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SnapshotStatistics describes the bounded filesystem work performed by a
// journal snapshot. RecordsMatched is measured before a selector limit is
// applied, so callers can distinguish selection from output truncation.
type SnapshotStatistics struct {
	// SegmentFilesRead counts every journal-dir file read, including spool
	// files that maintenance already promoted into `events-spool-*` segments.
	SegmentFilesRead int `json:"segment_files_read"`
	// PromotedSpoolFilesRead is the subset of SegmentFilesRead whose base name
	// carries the `events-spool-` promotion prefix. SpoolFilesRead counts only
	// spool-dir files that have not been promoted yet.
	PromotedSpoolFilesRead int   `json:"promoted_spool_files_read"`
	SpoolFilesRead         int   `json:"spool_files_read"`
	BytesScanned           int64 `json:"bytes_scanned"`
	RecordsScanned         int   `json:"records_scanned"`
	RecordsMatched         int   `json:"records_matched"`
	DuplicatesOmitted      int   `json:"duplicates_omitted"`
	CorruptLines           int   `json:"corrupt_lines"`
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
	return j.snapshotWithVisitor(ctx, selector, cursor, visitor, snapshotHooks{})
}

// snapshotHooks exposes internal capture boundaries to deterministic
// concurrency tests. Production callers always leave every field nil.
type snapshotHooks struct {
	// afterCapture runs once every file descriptor has been captured and both
	// storage locks have been released.
	afterCapture func()
	// betweenEnumerateAndOpen runs inside the active-segment capture, after the
	// journal directory has been listed but before any segment is opened, while
	// events.lock and append.lock are still held.
	betweenEnumerateAndOpen func()
}

// snapshot exposes the post-capture boundary to deterministic concurrency
// tests. Production callers always pass a nil hook.
func (j *Journal) snapshot(ctx context.Context, selector Selector, cursor *SnapshotCursor, afterCapture func()) (JournalSnapshot, error) {
	return j.snapshotWithVisitor(ctx, selector, cursor, nil, snapshotHooks{afterCapture: afterCapture})
}

func (j *Journal) snapshotWithVisitor(ctx context.Context, selector Selector, cursor *SnapshotCursor, visitor SnapshotVisitor, hooks snapshotHooks) (JournalSnapshot, error) {
	var snapshot JournalSnapshot
	if j == nil {
		return snapshot, errors.New("diagnostics: nil journal")
	}
	files, health, err := j.captureSnapshotFiles(ctx, cursor, hooks.betweenEnumerateAndOpen)
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

	if hooks.afterCapture != nil {
		hooks.afterCapture()
	}
	if err := ctxError(ctx); err != nil {
		return snapshot, err
	}

	seen := make(map[snapshotRecordIdentity]struct{})
	schemas := make(map[int]struct{})
	for _, captured := range files {
		if captured.kind == snapshotSegment {
			snapshot.Statistics.SegmentFilesRead++
			if strings.HasPrefix(filepath.Base(captured.path), "events-spool-") {
				snapshot.Statistics.PromotedSpoolFilesRead++
			}
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
			if !envelope.ObservedAt.IsZero() &&
				(snapshot.Health.EarliestObservedAt.IsZero() || envelope.ObservedAt.Before(snapshot.Health.EarliestObservedAt)) {
				snapshot.Health.EarliestObservedAt = envelope.ObservedAt
			}
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

// snapshotIdentity dedupes on the producer's own identity when the record
// carries one, and falls back to hashing the raw line for records written
// before source identity existed.
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
