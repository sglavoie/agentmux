package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

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
		// Unsynced, like appendLineLocked; promotion syncs what it publishes.
		return errors.Join(writeErr, unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
	}
}

// PromoteSpool publishes emergency spool evidence as immutable journal
// segments. A fresh spool is first claimed under its own non-blocking file
// lock. Validation and quarantine happen without the global journal lock; the
// lock is held only for the final atomic rename into the journal directory.
func (j *Journal) PromoteSpool(ctx context.Context) (MaintenanceReport, error) {
	return j.promoteSpool(ctx, spoolPromotionHooks{})
}

// ReconcileSpool is the compatibility name used by v1 debugger callers. New
// daemon maintenance should call PromoteSpool; the operation no longer copies
// records into the active journal line by line.
func (j *Journal) ReconcileSpool(ctx context.Context) (MaintenanceReport, error) {
	return j.PromoteSpool(ctx)
}

type spoolPromotionHooks struct {
	afterClaim      func(string) error
	afterValidation func(string) error
	beforePublish   func(string) error
	afterPublish    func(string) error
}

type claimedSpool struct {
	file   *os.File
	path   string
	legacy bool
}

func (j *Journal) promoteSpool(ctx context.Context, hooks spoolPromotionHooks) (MaintenanceReport, error) {
	var report MaintenanceReport
	if j == nil {
		return report, errors.New("diagnostics: nil journal")
	}
	if err := ctxError(ctx); err != nil {
		return report, err
	}
	entries, err := os.ReadDir(j.paths.SpoolDir)
	if err != nil {
		return report, err
	}
	for _, entry := range entries {
		if err := ctxError(ctx); err != nil {
			return report, err
		}
		name := entry.Name()
		if entry.IsDir() || !isSpoolClaimName(name) && !isSpoolSourceName(name) {
			continue
		}
		claim, busy, err := j.claimSpool(filepath.Join(j.paths.SpoolDir, name), name)
		if err != nil {
			return report, err
		}
		if busy {
			report.BusySpoolFiles++
			continue
		}
		if claim == nil {
			continue
		}
		if err := j.prepareAndPublishSpool(ctx, claim, &report, hooks); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (j *Journal) claimSpool(path, name string) (*claimedSpool, bool, error) {
	if err := validateRegularTarget(path); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, fileMode)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	closeFile := func() { _ = file.Close() }
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		closeFile()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, true, nil
		}
		return nil, false, err
	}
	if err := file.Chmod(fileMode); err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeFile()
		return nil, false, err
	}
	fail := func(err error) (*claimedSpool, bool, error) {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeFile()
		return nil, false, err
	}
	lockedInfo, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	pathInfo, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fail(nil)
	}
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(lockedInfo, pathInfo) {
		return fail(nil)
	}
	claimPath := path
	legacy := isLegacySpoolClaimName(name)
	if isSpoolSourceName(name) {
		claimPath = filepath.Join(j.paths.SpoolDir, fmt.Sprintf(
			"journal-%d-promoting-%d-%d.claim", os.Getpid(), rotationSequence.Add(1), j.opts.Now().UnixNano(),
		))
		if err := os.Rename(path, claimPath); err != nil {
			if os.IsNotExist(err) {
				return fail(nil)
			}
			return fail(err)
		}
		if err := syncDirectory(j.paths.SpoolDir); err != nil {
			return fail(err)
		}
	}
	return &claimedSpool{file: file, path: claimPath, legacy: legacy}, false, nil
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

func isSpoolClaimName(name string) bool {
	if !strings.HasPrefix(name, "journal-") {
		return false
	}
	isClaim := strings.Contains(name, "-reconciling-") || strings.Contains(name, "-promoting-")
	return isClaim && (strings.HasSuffix(name, ".claim") || strings.HasSuffix(name, ".jsonl"))
}

func isLegacySpoolClaimName(name string) bool {
	return strings.HasPrefix(name, "journal-") && strings.Contains(name, "-reconciling-") &&
		(strings.HasSuffix(name, ".claim") || strings.HasSuffix(name, ".jsonl"))
}

func isSpoolSourceName(name string) bool {
	return strings.HasPrefix(name, "journal-") && strings.HasSuffix(name, ".jsonl") && !isSpoolClaimName(name)
}

func (j *Journal) prepareAndPublishSpool(ctx context.Context, claim *claimedSpool, report *MaintenanceReport, hooks spoolPromotionHooks) (retErr error) {
	defer func() {
		if claim.file != nil {
			_ = unix.Flock(int(claim.file.Fd()), unix.LOCK_UN)
			_ = claim.file.Close()
		}
	}()
	if hooks.afterClaim != nil {
		if err := hooks.afterClaim(claim.path); err != nil {
			return err
		}
	}
	data, err := io.ReadAll(claim.file)
	if err != nil {
		return err
	}
	valid, invalid, corruptLines := tolerantJSONLParts(data)
	report.CorruptLines += corruptLines
	if len(invalid) > 0 {
		quarantine := claim.path + ".partial-corrupt"
		if err := atomicWritePrivate(quarantine, invalid); err != nil {
			return err
		}
		report.QuarantinedFiles = append(report.QuarantinedFiles, quarantine)
	}
	if claim.legacy {
		identities, err := j.publishedRecordIdentities(ctx)
		if err != nil {
			return err
		}
		filtered := valid[:0]
		for _, line := range valid {
			identity, ok := recordIdentity(line)
			if ok {
				if _, duplicate := identities[identity]; duplicate {
					continue
				}
				identities[identity] = struct{}{}
			}
			filtered = append(filtered, line)
		}
		valid = filtered
	}
	validData := make([]byte, 0, len(data))
	for _, line := range valid {
		validData = append(validData, line...)
		validData = append(validData, '\n')
	}
	if len(invalid) > 0 || claim.legacy {
		if err := rewriteLockedSpoolClaim(claim, validData); err != nil {
			return err
		}
	}
	if hooks.afterValidation != nil {
		if err := hooks.afterValidation(claim.path); err != nil {
			return err
		}
	}
	if err := ctxError(ctx); err != nil {
		return err
	}
	if len(valid) == 0 {
		if err := os.Remove(claim.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := syncDirectory(j.paths.SpoolDir); err != nil {
			return err
		}
		report.ReconciledFiles++
		return nil
	}
	if hooks.beforePublish != nil {
		if err := hooks.beforePublish(claim.path); err != nil {
			return err
		}
	}
	segment, err := j.publishSpoolClaim(ctx, claim.path)
	if err != nil {
		return err
	}
	report.PromotedRecords += len(valid)
	report.PromotedFiles++
	// Preserve the v1 counters until debugger reports move to snapshot metadata.
	report.ReconciledRecords += len(valid)
	report.ReconciledFiles++
	if hooks.afterPublish != nil {
		return hooks.afterPublish(segment)
	}
	return nil
}

// rewriteLockedSpoolClaim atomically replaces a claim while preserving its
// cross-process exclusivity. The replacement inode is locked before it enters
// the spool namespace, then ownership is transferred to claim.file before the
// old inode is unlocked. A competing maintainer therefore observes either the
// old locked inode or the new locked inode throughout the rewrite.
//
// It deliberately does not use internal/atomicfile: the temp must be flocked
// before it is renamed into place, checked against the claim's inode just
// before the rename, and kept open afterwards as the claim's new handle.
// atomicfile closes its temp before renaming, which would open a window where
// the claim is visible unlocked.
func rewriteLockedSpoolClaim(claim *claimedSpool, data []byte) (retErr error) {
	if claim == nil || claim.file == nil {
		return errors.New("diagnostics: rewriting unlocked spool claim")
	}
	directory := filepath.Dir(claim.path)
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	if err := validateRegularTarget(claim.path); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, "."+filepath.Base(claim.path)+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		if temp != nil {
			_ = unix.Flock(int(temp.Fd()), unix.LOCK_UN)
			_ = temp.Close()
		}
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(fileMode); err != nil {
		return err
	}
	if err := unix.Flock(int(temp.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	written, writeErr := temp.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	if err := errors.Join(writeErr, temp.Sync()); err != nil {
		return err
	}
	lockedInfo, err := claim.file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Stat(claim.path)
	if err != nil {
		return err
	}
	if !os.SameFile(lockedInfo, pathInfo) {
		return fmt.Errorf("diagnostics: spool claim changed during rewrite: %s", claim.path)
	}
	if err := os.Rename(tempPath, claim.path); err != nil {
		return err
	}

	old := claim.file
	claim.file = temp
	temp = nil
	return errors.Join(
		syncDirectory(directory),
		unix.Flock(int(old.Fd()), unix.LOCK_UN),
		old.Close(),
	)
}

func (j *Journal) publishedRecordIdentities(ctx context.Context) (map[journalRecordIdentity]struct{}, error) {
	j.sealedMu.Lock()
	lock, err := acquireSharedFileLock(ctx, j.lock, j.opts.MaintenanceLockTimeout)
	if err != nil {
		j.sealedMu.Unlock()
		return nil, err
	}
	entries, err := os.ReadDir(j.paths.JournalDir)
	if err != nil {
		releaseFileLock(lock)
		j.sealedMu.Unlock()
		return nil, err
	}
	var files []snapshotFile
	for _, entry := range entries {
		if err := ctxError(ctx); err != nil {
			closeSnapshotFiles(files)
			releaseFileLock(lock)
			j.sealedMu.Unlock()
			return nil, err
		}
		name := entry.Name()
		if entry.IsDir() || !isSnapshotSegmentName(name) {
			continue
		}
		captured, err := openSnapshotFile(filepath.Join(j.paths.JournalDir, name), snapshotSegment, name != "events.jsonl", nil)
		if err != nil {
			closeSnapshotFiles(files)
			releaseFileLock(lock)
			j.sealedMu.Unlock()
			return nil, err
		}
		if captured != nil {
			files = append(files, *captured)
		}
	}
	releaseFileLock(lock)
	j.sealedMu.Unlock()
	defer closeSnapshotFiles(files)
	identities := make(map[journalRecordIdentity]struct{})
	for _, file := range files {
		_, _, _, err := scanSnapshotFile(ctx, file, func(_ []byte, envelope TraceEnvelope) error {
			if envelope.SourceID != "" && envelope.SourceSequence != 0 {
				identities[journalRecordIdentity{sourceID: envelope.SourceID, sourceSequence: envelope.SourceSequence}] = struct{}{}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return identities, nil
}

func (j *Journal) publishSpoolClaim(ctx context.Context, claim string) (string, error) {
	j.sealedMu.Lock()
	defer j.sealedMu.Unlock()
	lock, err := acquireFileLock(ctx, j.lock, j.opts.MaintenanceLockTimeout)
	if err != nil {
		return "", err
	}
	defer releaseFileLock(lock)
	segment := filepath.Join(j.paths.JournalDir, fmt.Sprintf(
		"events-spool-%s-%d-%06d.jsonl", timestamp(j.opts.Now()), os.Getpid(), rotationSequence.Add(1),
	))
	if err := syncFile(claim); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("diagnostics: syncing spool claim: %w", err)
	}
	if err := os.Rename(claim, segment); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("diagnostics: spool claim disappeared before publication: %w", err)
		}
		return "", fmt.Errorf("diagnostics: publishing spool segment: %w", err)
	}
	if err := errors.Join(syncDirectory(j.paths.JournalDir), syncDirectory(j.paths.SpoolDir)); err != nil {
		return segment, err
	}
	return segment, nil
}
