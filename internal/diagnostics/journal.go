package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const MaxSegmentBytes int64 = 16 << 20

var ErrLockTimeout = errors.New("diagnostics: journal lock timeout")

var rotationSequence atomic.Uint64

type JournalOptions struct {
	KeepDays     int
	MaxBytes     int64
	SegmentBytes int64
	LockTimeout  time.Duration
	// MaintenanceLockTimeout bounds sealed-namespace lock waits on the
	// maintenance paths (spool publication, compaction commit, retention,
	// published-identity scans). It is deliberately longer than LockTimeout so
	// maintenance outlasts shared readers instead of reporting lock timeouts,
	// while Append keeps its short fail-open budget.
	MaintenanceLockTimeout time.Duration
	// CompactionInputFiles caps how many sealed segments one compaction
	// commit consumes (zero means maxCompactionInputFiles). Production leaves
	// it at the default; tests lower it so a few dozen segments exercise the
	// same multi-pass drain that a backlog of thousands would.
	CompactionInputFiles int
	Now                  func() time.Time
}

type MaintenanceReport struct {
	PromotedRecords             int
	PromotedFiles               int
	BusySpoolFiles              int
	ReconciledRecords           int
	ReconciledFiles             int
	CorruptLines                int
	QuarantinedFiles            []string
	CompactionInputFiles        int
	CompactionInputBytes        int64
	CompactionOutputFiles       int
	CompactionOutputBytes       int64
	CompactionDuplicatesOmitted int
	RotatedFiles                int
	RemovedFiles                int
	RemovedBytes                int64
}

// ReadReport describes a best-effort journal read. Corrupt JSONL records are
// counted and skipped so operator-facing diagnostics remain available while a
// damaged segment is being investigated.
type ReadReport struct {
	Records      []TraceEnvelope
	FilesRead    int
	CorruptLines int
}

// JournalHealth is a read-only snapshot of durable diagnostic storage.
type JournalHealth struct {
	Files              int       `json:"files"`
	Bytes              int64     `json:"bytes"`
	SpoolFiles         int       `json:"spool_files"`
	SpoolBytes         int64     `json:"spool_bytes"`
	OldestSpoolModTime time.Time `json:"oldest_spool_mod_time,omitempty"`
	LegacyClaimFiles   int       `json:"legacy_claim_files"`
	QuarantinedFiles   int       `json:"quarantined_files"`
	CorruptLines       int       `json:"corrupt_lines"`
	Records            int       `json:"records"`
	EarliestObservedAt time.Time `json:"earliest_observed_at,omitempty"`
	LatestObservedAt   time.Time `json:"latest_observed_at,omitempty"`
	ObservedSchemas    []int     `json:"observed_schemas"`
}

// Journal storage is guarded by two permanent lock sidecars:
//
//   - append.lock guards the active segment only (Append's tail repair, line
//     appends, and size-triggered rotation);
//   - events.lock guards the sealed-segment namespace (snapshot capture and
//     published-identity scans shared; spool publication, compaction commit,
//     and retention exclusive).
//
// Lock ordering is events.lock before append.lock wherever both are taken
// (Read, ReadTolerant, Inspect, and snapshot capture's active-segment
// enumeration), mirrored in-process by sealedMu before appendMu. Append takes
// only append.lock, so hook writes never wait behind a maintenance commit
// window; the split is safe because every sealed-side mutation revalidates its
// candidate files by inode immediately before renaming or unlinking them.
// Snapshot capture holds append.lock shared only while it lists and opens the
// journal directory, so an append cannot rotate events.jsonl between the
// listing and the open and make the capture omit the just-sealed segment.
//
// Mixed-build hazard: a pre-split build still takes only events.lock for its
// Read tail repair, so it can atomically replace events.jsonl while a
// post-split appender holds append.lock. The window is transient and
// fail-open (at worst an in-flight record lands in a quarantined tail), and
// the supported install path keeps CLI and daemon builds matched (doctor's
// daemon_build_mismatch finding, `just install-restart-smoke`).
type Journal struct {
	paths      Paths
	opts       JournalOptions
	key        []byte
	current    string
	lock       string // events.lock: sealed-segment namespace
	appendLock string // append.lock: active segment
	sealedMu   sync.Mutex
	appendMu   sync.Mutex
}

func NewJournal(paths Paths, options JournalOptions) (*Journal, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	key, err := loadOrCreateHMACKey(paths.HMACKey)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(paths.JournalDir, "events.lock")
	if err := initializeFileLock(lockPath); err != nil {
		return nil, err
	}
	appendLockPath := filepath.Join(paths.JournalDir, "append.lock")
	if err := initializeFileLock(appendLockPath); err != nil {
		return nil, err
	}
	options = normalizeJournalOptions(options)
	return &Journal{
		paths: paths, opts: options, key: key,
		current:    paths.JournalFile,
		lock:       lockPath,
		appendLock: appendLockPath,
	}, nil
}

func normalizeJournalOptions(options JournalOptions) JournalOptions {
	if options.KeepDays == 0 {
		options.KeepDays = 14
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 256 << 20
	}
	if options.SegmentBytes <= 0 {
		options.SegmentBytes = MaxSegmentBytes
	}
	if options.LockTimeout <= 0 {
		options.LockTimeout = 25 * time.Millisecond
	}
	if options.MaintenanceLockTimeout <= 0 {
		options.MaintenanceLockTimeout = 2 * time.Second
	}
	if options.CompactionInputFiles <= 0 {
		options.CompactionInputFiles = maxCompactionInputFiles
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return options
}

// ProtectPayload fills the envelope's HMAC and byte count without retaining
// the payload. The local key never leaves the diagnostic state root.
func (j *Journal) ProtectPayload(envelope *TraceEnvelope, payload []byte) {
	if j == nil || envelope == nil {
		return
	}
	envelope.PayloadHMAC = PayloadHMAC(j.key, payload)
	envelope.PayloadSize = int64(len(payload))
}

func (j *Journal) Append(ctx context.Context, envelope TraceEnvelope) error {
	if j == nil {
		return errors.New("diagnostics: nil journal")
	}
	if err := envelope.normalize(j.opts.Now()); err != nil {
		return err
	}
	record, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("diagnostics: encoding trace: %w", err)
	}
	record = append(record, '\n')

	j.appendMu.Lock()
	defer j.appendMu.Unlock()
	lock, lockErr := acquireFileLock(ctx, j.appendLock, j.opts.LockTimeout)
	if lockErr != nil {
		if spoolErr := j.appendSpool(ctx, record); spoolErr == nil {
			return nil
		} else {
			return errors.Join(lockErr, fmt.Errorf("diagnostics: emergency spool: %w", spoolErr))
		}
	}
	defer releaseFileLock(lock)
	if err := j.repairIncompleteTailLocked(); err != nil {
		return err
	}
	return j.appendLineLocked(record)
}

func (j *Journal) repairIncompleteTailLocked() error {
	file, err := os.Open(j.current)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	if info.Size() == 0 {
		return file.Close()
	}
	last := []byte{0}
	_, readErr := file.ReadAt(last, info.Size()-1)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = repairJSONLTail(j.current, j.opts.Now())
	return err
}

// TryAppend is the hook-safe write path: it contains panics and suppresses all
// persistence errors without logging, preventing recursive diagnostic writes.
func (j *Journal) TryAppend(ctx context.Context, envelope TraceEnvelope) (ok bool) {
	if j == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return j.Append(ctx, envelope) == nil
}

func (j *Journal) appendLineLocked(record []byte) error {
	if err := validateRegularTarget(j.current); err != nil {
		return err
	}
	if info, err := os.Stat(j.current); err == nil && info.Size() > 0 && info.Size()+int64(len(record)) > j.opts.SegmentBytes {
		if _, err := j.rotateLocked(); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(j.current, os.O_WRONLY|os.O_CREATE|os.O_APPEND, fileMode)
	if err != nil {
		return fmt.Errorf("diagnostics: opening journal: %w", err)
	}
	if err := file.Chmod(fileMode); err != nil {
		_ = file.Close()
		return err
	}
	written, writeErr := file.Write(record)
	if writeErr == nil && written != len(record) {
		writeErr = io.ErrShortWrite
	}
	// No fsync per record: an O_APPEND write already survives the writer's
	// crash, and on darwin each Sync is an F_FULLFSYNC that flushes the whole
	// drive cache from inside every hook. Only a power loss can take the tail;
	// rotateLocked syncs a segment once, as it is sealed.
	return errors.Join(writeErr, file.Close())
}

func (j *Journal) Maintain(ctx context.Context) (MaintenanceReport, error) {
	var report MaintenanceReport
	if j == nil {
		return report, errors.New("diagnostics: nil journal")
	}
	promotion, err := j.PromoteSpool(ctx)
	report.merge(promotion)
	if err != nil {
		return report, err
	}
	retention, err := j.RotateAndRetain(ctx)
	report.merge(retention)
	return report, err
}

func (r *MaintenanceReport) merge(other MaintenanceReport) {
	r.PromotedRecords += other.PromotedRecords
	r.PromotedFiles += other.PromotedFiles
	r.BusySpoolFiles += other.BusySpoolFiles
	r.ReconciledRecords += other.ReconciledRecords
	r.ReconciledFiles += other.ReconciledFiles
	r.CorruptLines += other.CorruptLines
	r.QuarantinedFiles = append(r.QuarantinedFiles, other.QuarantinedFiles...)
	r.CompactionInputFiles += other.CompactionInputFiles
	r.CompactionInputBytes += other.CompactionInputBytes
	r.CompactionOutputFiles += other.CompactionOutputFiles
	r.CompactionOutputBytes += other.CompactionOutputBytes
	r.CompactionDuplicatesOmitted += other.CompactionDuplicatesOmitted
	r.RotatedFiles += other.RotatedFiles
	r.RemovedFiles += other.RemovedFiles
	r.RemovedBytes += other.RemovedBytes
}

// lockJournalExclusive takes both storage locks in the canonical order —
// events.lock before append.lock (sealedMu before appendMu in-process) — for
// operations that need a consistent view spanning the sealed namespace and
// the active segment. Release with unlockJournalExclusive.
func (j *Journal) lockJournalExclusive(ctx context.Context) (sealed, active *os.File, err error) {
	j.sealedMu.Lock()
	sealed, err = acquireFileLock(ctx, j.lock, j.opts.LockTimeout)
	if err != nil {
		j.sealedMu.Unlock()
		return nil, nil, err
	}
	j.appendMu.Lock()
	active, err = acquireFileLock(ctx, j.appendLock, j.opts.LockTimeout)
	if err != nil {
		j.appendMu.Unlock()
		releaseFileLock(sealed)
		j.sealedMu.Unlock()
		return nil, nil, err
	}
	return sealed, active, nil
}

func (j *Journal) unlockJournalExclusive(sealed, active *os.File) {
	releaseFileLock(active)
	j.appendMu.Unlock()
	releaseFileLock(sealed)
	j.sealedMu.Unlock()
}

func (j *Journal) Read(ctx context.Context, selector Selector) ([]TraceEnvelope, error) {
	if j == nil {
		return nil, errors.New("diagnostics: nil journal")
	}
	sealed, active, err := j.lockJournalExclusive(ctx)
	if err != nil {
		return nil, err
	}
	defer j.unlockJournalExclusive(sealed, active)
	if _, err := repairJSONLTail(j.current, j.opts.Now()); err != nil {
		return nil, err
	}
	files, err := j.segmentFilesLocked(true)
	if err != nil {
		return nil, err
	}
	var records []TraceEnvelope
	for _, file := range files {
		if err := ctxError(ctx); err != nil {
			return nil, err
		}
		if err := readJSONL(file.path, func(envelope TraceEnvelope) {
			if selector.matches(envelope) {
				records = append(records, envelope)
			}
		}); err != nil {
			return nil, err
		}
	}
	if selector.Limit > 0 && len(records) > selector.Limit {
		records = records[len(records)-selector.Limit:]
	}
	return records, nil
}

// ReadTolerant is the operator-facing counterpart to Read. It skips malformed
// JSONL records and returns their count instead of failing the entire query.
// The journal is not repaired or otherwise mutated by this method.
func (j *Journal) ReadTolerant(ctx context.Context, selector Selector) (ReadReport, error) {
	var report ReadReport
	if j == nil {
		return report, errors.New("diagnostics: nil journal")
	}
	sealed, active, err := j.lockJournalExclusive(ctx)
	if err != nil {
		return report, err
	}
	defer j.unlockJournalExclusive(sealed, active)
	files, err := j.segmentFilesLocked(true)
	if err != nil {
		return report, err
	}
	for _, file := range files {
		if err := ctxError(ctx); err != nil {
			return report, err
		}
		corrupt, err := readJSONLTolerant(file.path, func(envelope TraceEnvelope) {
			if selector.matches(envelope) {
				report.Records = append(report.Records, envelope)
			}
		})
		if err != nil {
			return report, err
		}
		report.FilesRead++
		report.CorruptLines += corrupt
	}
	if selector.Limit > 0 && len(report.Records) > selector.Limit {
		report.Records = report.Records[len(report.Records)-selector.Limit:]
	}
	return report, nil
}

// Inspect returns storage and schema health without repairing, reconciling,
// rotating, or pruning diagnostic files.
func (j *Journal) Inspect(ctx context.Context) (JournalHealth, error) {
	var health JournalHealth
	if j == nil {
		return health, errors.New("diagnostics: nil journal")
	}
	sealed, active, err := j.lockJournalExclusive(ctx)
	if err != nil {
		return health, err
	}
	defer j.unlockJournalExclusive(sealed, active)
	files, err := j.segmentFilesLocked(true)
	if err != nil {
		return health, err
	}
	schemas := make(map[int]struct{})
	for _, file := range files {
		corrupt, readErr := readJSONLTolerant(file.path, func(envelope TraceEnvelope) {
			health.Records++
			schemas[envelope.SchemaVersion] = struct{}{}
			if !envelope.ObservedAt.IsZero() &&
				(health.EarliestObservedAt.IsZero() || envelope.ObservedAt.Before(health.EarliestObservedAt)) {
				health.EarliestObservedAt = envelope.ObservedAt
			}
			if envelope.ObservedAt.After(health.LatestObservedAt) {
				health.LatestObservedAt = envelope.ObservedAt
			}
		})
		if readErr != nil {
			return health, readErr
		}
		health.Files++
		health.Bytes += file.size
		health.CorruptLines += corrupt
	}
	entries, err := os.ReadDir(j.paths.SpoolDir)
	if err != nil {
		return health, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if isSpoolSourceName(name) || isSpoolClaimName(name) {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return health, infoErr
			}
			health.SpoolFiles++
			health.SpoolBytes += info.Size()
			continue
		}
		if strings.Contains(name, ".partial-") {
			health.QuarantinedFiles++
		}
	}
	journalEntries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		return health, err
	}
	for _, entry := range journalEntries {
		if !entry.IsDir() && strings.Contains(entry.Name(), ".partial-") {
			health.QuarantinedFiles++
		}
	}
	for schema := range schemas {
		health.ObservedSchemas = append(health.ObservedSchemas, schema)
	}
	sort.Ints(health.ObservedSchemas)
	return health, nil
}
