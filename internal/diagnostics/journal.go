package diagnostics

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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const MaxSegmentBytes int64 = 16 << 20

var ErrLockTimeout = errors.New("diagnostics: journal lock timeout")

var rotationSequence atomic.Uint64

type JournalOptions struct {
	KeepDays     int
	MaxBytes     int64
	SegmentBytes int64
	LockTimeout  time.Duration
	Now          func() time.Time
}

type MaintenanceReport struct {
	ReconciledRecords int
	ReconciledFiles   int
	CorruptLines      int
	QuarantinedFiles  []string
	RotatedFiles      int
	RemovedFiles      int
	RemovedBytes      int64
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
	Files            int
	Bytes            int64
	SpoolFiles       int
	SpoolBytes       int64
	QuarantinedFiles int
	CorruptLines     int
	Records          int
	LatestObservedAt time.Time
	ObservedSchemas  []int
}

type Journal struct {
	paths   Paths
	opts    JournalOptions
	key     []byte
	current string
	lock    string
	mu      sync.Mutex
}

func NewJournal(paths Paths, options JournalOptions) (*Journal, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	key, err := loadOrCreateHMACKey(paths.HMACKey)
	if err != nil {
		return nil, err
	}
	options = normalizeJournalOptions(options)
	return &Journal{
		paths: paths, opts: options, key: key,
		current: paths.JournalFile,
		lock:    filepath.Join(paths.JournalDir, "events.lock"),
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

	j.mu.Lock()
	defer j.mu.Unlock()
	lock, lockErr := acquireFileLock(ctx, j.lock, j.opts.LockTimeout)
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
	return errors.Join(writeErr, file.Sync(), file.Close())
}

func (j *Journal) appendSpool(ctx context.Context, record []byte) error {
	return j.appendSpoolWithOpenHook(ctx, record, nil)
}

// appendSpoolWithOpenHook exposes the open-before-lock boundary to deterministic
// concurrency tests. Production callers always pass a nil hook.
func (j *Journal) appendSpoolWithOpenHook(ctx context.Context, record []byte, afterFirstOpen func()) error {
	if err := ensurePrivateDirectory(j.paths.SpoolDir); err != nil {
		return err
	}
	path := filepath.Join(j.paths.SpoolDir, fmt.Sprintf("journal-%d.jsonl", os.Getpid()))
	deadline := time.Now().Add(j.opts.LockTimeout)
	for {
		if err := validateRegularTarget(path); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, fileMode)
		if err != nil {
			return err
		}
		if afterFirstOpen != nil {
			afterFirstOpen()
			afterFirstOpen = nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			_ = file.Close()
			return ErrLockTimeout
		}
		if err := acquireOpenFileLock(ctx, file, remaining); err != nil {
			_ = file.Close()
			return err
		}
		lockedInfo, statErr := file.Stat()
		pathInfo, pathErr := os.Stat(path)
		if os.IsNotExist(pathErr) {
			pathInfo = nil
			pathErr = nil
		}
		if err := errors.Join(statErr, pathErr); err != nil {
			_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
			_ = file.Close()
			return err
		}
		if pathInfo == nil || !os.SameFile(lockedInfo, pathInfo) {
			if err := errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close()); err != nil {
				return err
			}
			continue
		}
		written, writeErr := file.Write(record)
		if writeErr == nil && written != len(record) {
			writeErr = io.ErrShortWrite
		}
		return errors.Join(writeErr, file.Sync(), unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
	}
}

func (j *Journal) Maintain(ctx context.Context) (MaintenanceReport, error) {
	var report MaintenanceReport
	if j == nil {
		return report, errors.New("diagnostics: nil journal")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	lock, err := acquireFileLock(ctx, j.lock, j.opts.LockTimeout)
	if err != nil {
		return report, err
	}
	defer releaseFileLock(lock)

	if quarantine, err := repairJSONLTail(j.current, j.opts.Now()); err != nil {
		return report, err
	} else if quarantine != "" {
		report.QuarantinedFiles = append(report.QuarantinedFiles, quarantine)
	}
	if err := j.reconcileSpoolLocked(&report); err != nil {
		return report, err
	}
	if info, err := os.Stat(j.current); err == nil && info.Size() > j.opts.SegmentBytes {
		if _, err := j.rotateLocked(); err != nil {
			return report, err
		}
		report.RotatedFiles++
	} else if err != nil && !os.IsNotExist(err) {
		return report, err
	}
	if err := j.applyRetentionLocked(&report); err != nil {
		return report, err
	}
	return report, nil
}

// ReconcileSpool merges emergency spool records without rotating segments or
// applying retention. It is the bounded preparation used by offline readers:
// a stopped daemon is not required, and later valid records survive an
// isolated corrupt spool line.
func (j *Journal) ReconcileSpool(ctx context.Context) (MaintenanceReport, error) {
	var report MaintenanceReport
	if j == nil {
		return report, errors.New("diagnostics: nil journal")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	lock, err := acquireFileLock(ctx, j.lock, j.opts.LockTimeout)
	if err != nil {
		return report, err
	}
	defer releaseFileLock(lock)
	if err := j.reconcileSpoolLocked(&report); err != nil {
		return report, err
	}
	return report, nil
}

func (j *Journal) reconcileSpoolLocked(report *MaintenanceReport) error {
	return j.reconcileSpoolLockedWithAppender(report, j.appendLineLocked)
}

type journalRecordIdentity struct {
	sourceID       string
	sourceSequence uint64
}

func recordIdentity(record []byte) (journalRecordIdentity, bool) {
	var identity struct {
		SourceID       string `json:"source_id"`
		SourceSequence uint64 `json:"source_sequence"`
	}
	if err := json.Unmarshal(record, &identity); err != nil || identity.SourceID == "" || identity.SourceSequence == 0 {
		return journalRecordIdentity{}, false
	}
	return journalRecordIdentity{sourceID: identity.SourceID, sourceSequence: identity.SourceSequence}, true
}

func (j *Journal) journalRecordIdentitiesLocked() (map[journalRecordIdentity]struct{}, error) {
	files, err := j.segmentFilesLocked(true)
	if err != nil {
		return nil, err
	}
	identities := make(map[journalRecordIdentity]struct{})
	for _, file := range files {
		if _, err := readJSONLTolerant(file.path, func(envelope TraceEnvelope) {
			if envelope.SourceID != "" && envelope.SourceSequence != 0 {
				identities[journalRecordIdentity{sourceID: envelope.SourceID, sourceSequence: envelope.SourceSequence}] = struct{}{}
			}
		}); err != nil {
			return nil, err
		}
	}
	return identities, nil
}

func isSpoolClaimName(name string) bool {
	if !strings.HasPrefix(name, "journal-") || !strings.Contains(name, "-reconciling-") {
		return false
	}
	return strings.HasSuffix(name, ".claim") || strings.HasSuffix(name, ".jsonl")
}

func isSpoolSourceName(name string) bool {
	return strings.HasPrefix(name, "journal-") && strings.HasSuffix(name, ".jsonl") && !isSpoolClaimName(name)
}

func (j *Journal) reconcileSpoolLockedWithAppender(report *MaintenanceReport, appendRecord func([]byte) error) error {
	entries, err := os.ReadDir(j.paths.SpoolDir)
	if err != nil {
		return err
	}
	spoolEntries := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && (isSpoolClaimName(name) || isSpoolSourceName(name)) {
			spoolEntries = append(spoolEntries, entry)
		}
	}
	if len(spoolEntries) == 0 {
		return nil
	}
	if err := j.repairIncompleteTailLocked(); err != nil {
		return err
	}
	identities, err := j.journalRecordIdentitiesLocked()
	if err != nil {
		return err
	}
	for _, entry := range spoolEntries {
		name := entry.Name()
		isClaim := isSpoolClaimName(name)
		path := filepath.Join(j.paths.SpoolDir, name)
		if err := validateRegularTarget(path); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_RDWR, fileMode)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
			_ = file.Close()
			return err
		}
		claim := path
		if !isClaim {
			claim = filepath.Join(j.paths.SpoolDir, fmt.Sprintf("journal-%d-reconciling-%d-%d.claim", os.Getpid(), rotationSequence.Add(1), j.opts.Now().UnixNano()))
			if err := os.Rename(path, claim); err != nil {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
		}
		data, readErr := io.ReadAll(file)
		if readErr != nil {
			_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
			_ = file.Close()
			return readErr
		}
		valid, invalid, corruptLines := tolerantJSONLParts(data)
		report.CorruptLines += corruptLines
		if len(invalid) > 0 {
			quarantine := claim + ".partial-corrupt"
			if err := atomicWritePrivate(quarantine, invalid); err != nil {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
				return err
			}
			report.QuarantinedFiles = append(report.QuarantinedFiles, quarantine)
		}
		for _, line := range valid {
			if len(line) == 0 {
				continue
			}
			identity, hasIdentity := recordIdentity(line)
			if hasIdentity {
				if _, alreadyReconciled := identities[identity]; alreadyReconciled {
					continue
				}
			}
			record := append(append([]byte(nil), line...), '\n')
			if err := appendRecord(record); err != nil {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
				return err
			}
			if hasIdentity {
				identities[identity] = struct{}{}
			}
			report.ReconciledRecords++
		}
		unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeErr := file.Close()
		if err := errors.Join(unlockErr, closeErr); err != nil {
			return err
		}
		if err := os.Remove(claim); err != nil && !os.IsNotExist(err) {
			return err
		}
		report.ReconciledFiles++
	}
	return nil
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
	if err := os.Rename(j.current, rotated); err != nil {
		return "", fmt.Errorf("diagnostics: rotating journal: %w", err)
	}
	return rotated, nil
}

type retainedFile struct {
	path      string
	name      string
	size      int64
	modTime   time.Time
	protected bool
}

func (j *Journal) applyRetentionLocked(report *MaintenanceReport) error {
	files, err := j.retentionFilesLocked()
	if err != nil {
		return err
	}
	now := j.opts.Now()
	var total int64
	for _, file := range files {
		total += file.size
	}
	cutoff := time.Time{}
	if j.opts.KeepDays > 0 {
		cutoff = now.Add(-time.Duration(j.opts.KeepDays) * 24 * time.Hour)
	}
	for _, file := range files {
		if file.path == j.current {
			continue
		}
		removeForAge := !cutoff.IsZero() && file.modTime.Before(cutoff)
		removeForSize := j.opts.MaxBytes > 0 && total > j.opts.MaxBytes
		if !removeForAge && !removeForSize {
			continue
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= file.size
		report.RemovedFiles++
		report.RemovedBytes += file.size
	}
	return nil
}

func (j *Journal) retentionFilesLocked() ([]retainedFile, error) {
	entries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		return nil, err
	}
	var files []retainedFile
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "events") || name == filepath.Base(j.lock) || strings.Contains(name, ".tmp") {
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

type Selector struct {
	After         time.Time
	Before        time.Time
	TraceID       string
	RequestID     string
	SourceEventID string
	Harness       string
	Profile       string
	SessionID     string
	WindowID      int
	RunID         string
	Event         string
	Stages        []string
	Outcomes      []string
	Limit         int
}

func (j *Journal) Read(ctx context.Context, selector Selector) ([]TraceEnvelope, error) {
	if j == nil {
		return nil, errors.New("diagnostics: nil journal")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	lock, err := acquireFileLock(ctx, j.lock, j.opts.LockTimeout)
	if err != nil {
		return nil, err
	}
	defer releaseFileLock(lock)
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
	j.mu.Lock()
	defer j.mu.Unlock()
	lock, err := acquireFileLock(ctx, j.lock, j.opts.LockTimeout)
	if err != nil {
		return report, err
	}
	defer releaseFileLock(lock)
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
	j.mu.Lock()
	defer j.mu.Unlock()
	lock, err := acquireFileLock(ctx, j.lock, j.opts.LockTimeout)
	if err != nil {
		return health, err
	}
	defer releaseFileLock(lock)
	files, err := j.segmentFilesLocked(true)
	if err != nil {
		return health, err
	}
	schemas := make(map[int]struct{})
	for _, file := range files {
		corrupt, readErr := readJSONLTolerant(file.path, func(envelope TraceEnvelope) {
			health.Records++
			schemas[envelope.SchemaVersion] = struct{}{}
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

func (s Selector) matches(envelope TraceEnvelope) bool {
	if !s.After.IsZero() && envelope.ObservedAt.Before(s.After) {
		return false
	}
	if !s.Before.IsZero() && envelope.ObservedAt.After(s.Before) {
		return false
	}
	if s.TraceID != "" && envelope.TraceID != s.TraceID || s.RequestID != "" && envelope.RequestID != s.RequestID ||
		s.Harness != "" && envelope.Harness != s.Harness || s.Profile != "" && envelope.Profile != s.Profile ||
		s.SessionID != "" && envelope.SessionID != s.SessionID || s.WindowID != 0 && envelope.WindowID != s.WindowID ||
		s.RunID != "" && envelope.RunID != s.RunID {
		return false
	}
	if s.SourceEventID != "" && !envelopeHasEvidenceID(envelope, s.SourceEventID, "source_event_id", "source_event_ids") {
		return false
	}
	if s.Event != "" && envelope.Evidence["event"] != s.Event {
		return false
	}
	return containsOrEmpty(s.Stages, envelope.Stage) && containsOrEmpty(s.Outcomes, envelope.Outcome)
}

func envelopeHasEvidenceID(envelope TraceEnvelope, id string, keys ...string) bool {
	for _, key := range keys {
		for _, value := range strings.Split(envelope.Evidence[key], ",") {
			if strings.TrimSpace(value) == id {
				return true
			}
		}
	}
	return false
}

func containsOrEmpty(values []string, candidate string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func readJSONL(path string, visit func(TraceEnvelope)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), int(MaxSegmentBytes)+1)
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var envelope TraceEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return fmt.Errorf("diagnostics: decoding %s line %d: %w", path, line, err)
		}
		visit(envelope)
	}
	return scanner.Err()
}

func readJSONLTolerant(path string, visit func(TraceEnvelope)) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), int(MaxSegmentBytes)+1)
	corrupt := 0
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var envelope TraceEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			corrupt++
			continue
		}
		visit(envelope)
	}
	return corrupt, scanner.Err()
}

// tolerantJSONLParts separates complete valid records from corrupt or partial
// lines while preserving valid records that follow a damaged one.
func tolerantJSONLParts(data []byte) (valid [][]byte, invalid []byte, corrupt int) {
	for offset := 0; offset < len(data); {
		relativeEnd := bytes.IndexByte(data[offset:], '\n')
		if relativeEnd < 0 {
			line := bytes.TrimSpace(data[offset:])
			if len(line) > 0 {
				invalid = append(invalid, data[offset:]...)
				corrupt++
			}
			break
		}
		end := offset + relativeEnd
		line := bytes.TrimSpace(data[offset:end])
		if len(line) > 0 {
			if json.Valid(line) {
				valid = append(valid, append([]byte(nil), line...))
			} else {
				invalid = append(invalid, data[offset:end+1]...)
				corrupt++
			}
		}
		offset = end + 1
	}
	return valid, invalid, corrupt
}

func repairJSONLTail(path string, now time.Time) (string, error) {
	if err := validateRegularTarget(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	valid, invalid := validJSONLPrefix(data)
	if len(invalid) == 0 {
		return "", nil
	}
	quarantine := path + ".partial-" + timestamp(now)
	if err := atomicWritePrivate(quarantine, invalid); err != nil {
		return "", err
	}
	if err := atomicWritePrivate(path, valid); err != nil {
		return "", err
	}
	return quarantine, nil
}

func validJSONLPrefix(data []byte) (valid, invalid []byte) {
	offset := 0
	for offset < len(data) {
		relativeEnd := bytes.IndexByte(data[offset:], '\n')
		if relativeEnd < 0 {
			return data[:offset], data[offset:]
		}
		end := offset + relativeEnd
		line := bytes.TrimSpace(data[offset:end])
		if len(line) > 0 && !json.Valid(line) {
			return data[:offset], data[offset:]
		}
		offset = end + 1
	}
	return data, nil
}

func atomicWritePrivate(path string, data []byte) (retErr error) {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err := validateRegularTarget(path); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(fileMode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func acquireFileLock(ctx context.Context, path string, timeout time.Duration) (*os.File, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := validateRegularTarget(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(fileMode); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := acquireOpenFileLock(ctx, file, timeout); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func acquireOpenFileLock(ctx context.Context, file *os.File, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(timeout)
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		if time.Now().After(deadline) {
			return ErrLockTimeout
		}
		timer := time.NewTimer(2 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func releaseFileLock(file *os.File) {
	if file == nil {
		return
	}
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	_ = file.Close()
}

func ctxError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func timestamp(now time.Time) string {
	return now.UTC().Format("20060102T150405.000000000Z")
}
