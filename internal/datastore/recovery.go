package datastore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// RecoveryManifestFileName is the private manifest written into a
	// successful datastore quarantine.
	RecoveryManifestFileName = "manifest.json"

	recoveryDirectoryName         = "recovery"
	recoveryOutcomePending        = "replacement_pending"
	recoveryOutcomeReplaced       = "replaced"
	recoveryOutcomeRollbackFailed = "rollback_failed"
)

// ErrRecoveryRequiresCorruption reports an attempted destructive recovery
// without a wrapped SQLITE_CORRUPT result code authorizing it.
var ErrRecoveryRequiresCorruption = errors.New("datastore: recovery requires a SQLITE_CORRUPT cause")

// RecoveryResult describes a completed corruption-recovery attempt.
type RecoveryResult struct {
	Replaced      bool   `json:"replaced"`
	DatabasePath  string `json:"database_path"`
	QuarantineDir string `json:"quarantine_dir,omitempty"`
}

type recoveryArtifact struct {
	Name           string    `json:"name"`
	OriginalPath   string    `json:"original_path"`
	QuarantinePath string    `json:"quarantine_path"`
	Size           int64     `json:"size"`
	Mode           string    `json:"mode"`
	ModTime        time.Time `json:"mod_time"`
	SHA256         string    `json:"sha256"`
	originalMode   fs.FileMode
}

type recoveryManifest struct {
	CreatedAt          time.Time          `json:"created_at"`
	CompletedAt        *time.Time         `json:"completed_at,omitempty"`
	DatabasePath       string             `json:"database_path"`
	CorruptionReason   string             `json:"corruption_reason"`
	ReplacementOutcome string             `json:"replacement_outcome"`
	Artifacts          []recoveryArtifact `json:"artifacts"`
	RollbackFailures   []string           `json:"rollback_failures,omitempty"`
}

type recoveryOperations struct {
	now           func() time.Time
	mkdir         func(string, fs.FileMode) error
	rename        func(string, string) error
	remove        func(string) error
	writeManifest func(string, recoveryManifest) error
	openFresh     func(context.Context, string) (*Store, error)
	validateFresh func(context.Context, *Store) error
}

func defaultRecoveryOperations() recoveryOperations {
	return recoveryOperations{
		now:           time.Now,
		mkdir:         os.Mkdir,
		rename:        os.Rename,
		remove:        os.Remove,
		writeManifest: writeRecoveryManifest,
		openFresh:     openPathUnderRecoveryLock,
		validateFresh: validateRecoveredStore,
	}
}

// RecoverCorruptDefault quarantines and replaces the default datastore only
// when cause wraps SQLITE_CORRUPT. Compatibility import remains the
// responsibility of the next ordinary data-service initialization.
func RecoverCorruptDefault(ctx context.Context, cause error) (RecoveryResult, error) {
	path, err := DefaultPath()
	if err != nil {
		return RecoveryResult{}, err
	}
	return RecoverCorruptPath(ctx, path, cause)
}

// RecoverCorruptPath quarantines and replaces a confirmed-corrupt datastore at
// path. It serializes with every ordinary Store, rechecks integrity after
// taking the exclusive lock, and returns Replaced=false if another process has
// already repaired the database.
func RecoverCorruptPath(ctx context.Context, path string, cause error) (RecoveryResult, error) {
	return recoverCorruptPath(ctx, path, cause, defaultRecoveryOperations())
}

func recoverCorruptPath(ctx context.Context, path string, cause error, ops recoveryOperations) (_ RecoveryResult, retErr error) {
	if !IsCorruption(cause) {
		return RecoveryResult{}, ErrRecoveryRequiresCorruption
	}
	if err := ctx.Err(); err != nil {
		return RecoveryResult{}, err
	}

	absPath, err := prepareDatabasePath(path)
	if err != nil {
		return RecoveryResult{}, err
	}
	result := RecoveryResult{DatabasePath: absPath}

	lock, err := acquireExclusiveRecoveryLock(ctx, absPath)
	if err != nil {
		return result, err
	}
	defer func() {
		if lock != nil {
			retErr = errors.Join(retErr, lock.Close())
		}
	}()

	corrupt, err := databaseStillCorrupt(ctx, absPath)
	if err != nil {
		return result, err
	}
	if !corrupt {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	artifacts, err := collectRecoveryArtifacts(ctx, absPath)
	if err != nil {
		return result, err
	}
	if len(artifacts) == 0 || artifacts[0].OriginalPath != absPath {
		return result, fmt.Errorf("datastore: corrupt database artifact %s disappeared before recovery", absPath)
	}

	createdAt := ops.now().UTC()
	quarantineDir, err := createQuarantineDirectory(absPath, createdAt, ops)
	if err != nil {
		return result, err
	}
	result.QuarantineDir = quarantineDir
	for i := range artifacts {
		artifacts[i].QuarantinePath = filepath.Join(quarantineDir, artifacts[i].Name)
	}

	moved := make([]recoveryArtifact, 0, len(artifacts))
	manifest := recoveryManifest{
		CreatedAt:          createdAt,
		DatabasePath:       absPath,
		CorruptionReason:   cause.Error(),
		ReplacementOutcome: recoveryOutcomePending,
		Artifacts:          artifacts,
	}
	replacementAttempted := false
	fail := func(primary error, fresh *Store) (RecoveryResult, error) {
		return result, rollbackRecovery(primary, fresh, replacementAttempted, quarantineDir, moved, &manifest, ops)
	}
	manifestPath := filepath.Join(quarantineDir, RecoveryManifestFileName)
	if err := ops.writeManifest(manifestPath, manifest); err != nil {
		return fail(fmt.Errorf("datastore: write recovery manifest: %w", err), nil)
	}

	for _, artifact := range artifacts {
		if err := ops.rename(artifact.OriginalPath, artifact.QuarantinePath); err != nil {
			return fail(fmt.Errorf("datastore: quarantine %s: %w", artifact.OriginalPath, err), nil)
		}
		moved = append(moved, artifact)
		if err := secureArtifact(artifact.QuarantinePath); err != nil {
			return fail(fmt.Errorf("datastore: secure quarantined artifact %s: %w", artifact.QuarantinePath, err), nil)
		}
	}
	if err := errors.Join(syncDirectory(filepath.Dir(absPath)), syncDirectory(quarantineDir)); err != nil {
		return fail(fmt.Errorf("datastore: sync quarantined artifacts: %w", err), nil)
	}

	replacementAttempted = true
	fresh, err := ops.openFresh(ctx, absPath)
	if err != nil {
		return fail(fmt.Errorf("datastore: create replacement database: %w", err), nil)
	}
	if err := ops.validateFresh(ctx, fresh); err != nil {
		return fail(fmt.Errorf("datastore: validate replacement database: %w", err), fresh)
	}
	if err := syncDirectory(filepath.Dir(absPath)); err != nil {
		return fail(fmt.Errorf("datastore: sync replacement database directory: %w", err), fresh)
	}

	completedAt := ops.now().UTC()
	manifest.CompletedAt = &completedAt
	manifest.ReplacementOutcome = recoveryOutcomeReplaced
	if err := ops.writeManifest(manifestPath, manifest); err != nil {
		return fail(fmt.Errorf("datastore: publish recovery manifest: %w", err), fresh)
	}

	// The replacement is fully migrated and validated before the downgrade.
	// Transferring the descriptor to the fresh Store makes that downgrade the
	// publication boundary and keeps the lock owned until SQLite closes.
	if err := lock.downgradeToShared(); err != nil {
		return fail(err, fresh)
	}
	fresh.recoveryLock = lock
	lock = nil
	result.Replaced = true
	if err := fresh.Close(); err != nil {
		return result, fmt.Errorf("datastore: close published replacement database: %w", err)
	}
	return result, nil
}

func databaseStillCorrupt(ctx context.Context, path string) (_ bool, retErr error) {
	for _, artifactPath := range databaseArtifactPaths(path) {
		if err := rejectRecoveryArtifact(artifactPath, artifactPath == path); err != nil {
			return false, err
		}
	}

	// SQLite may create or rewrite WAL shared-memory state even for a mode=ro
	// connection. Recheck a private byte-for-byte snapshot so the authoritative
	// corrupt artifacts remain untouched until their quarantine renames.
	checkDir, err := os.MkdirTemp(filepath.Dir(path), ".recovery-check-*")
	if err != nil {
		return false, fmt.Errorf("datastore: create corruption recheck directory: %w", err)
	}
	if err := os.Chmod(checkDir, dataDirectoryMode); err != nil {
		_ = os.RemoveAll(checkDir)
		return false, fmt.Errorf("datastore: secure corruption recheck directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(checkDir); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("datastore: remove corruption recheck directory: %w", err))
		}
	}()
	checkPath := filepath.Join(checkDir, filepath.Base(path))
	for _, artifactPath := range databaseArtifactPaths(path) {
		destination := filepath.Join(checkDir, filepath.Base(artifactPath))
		if err := copyRecoveryArtifact(ctx, artifactPath, destination); errors.Is(err, fs.ErrNotExist) && artifactPath != path {
			continue
		} else if err != nil {
			return false, err
		}
	}

	db, err := sql.Open("sqlite", readOnlySQLiteDSN(checkPath))
	if err != nil {
		return false, fmt.Errorf("datastore: open corruption recheck snapshot for %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	check, checkErr := pragmaCheck(ctx, db, `PRAGMA quick_check(1)`)
	closeErr := db.Close()
	if checkErr != nil {
		if IsCorruption(checkErr) {
			return true, closeErr
		}
		return false, errors.Join(fmt.Errorf("datastore: corruption recheck for %s: %w", path, checkErr), closeErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("datastore: close corruption recheck for %s: %w", path, closeErr)
	}
	check = strings.TrimSpace(check)
	if check == "" {
		return false, fmt.Errorf("datastore: corruption recheck for %s returned no result", path)
	}
	return !strings.EqualFold(check, "ok"), nil
}

func copyRecoveryArtifact(ctx context.Context, sourcePath, destinationPath string) (retErr error) {
	fd, err := unix.Open(sourcePath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return fs.ErrNotExist
		}
		if errors.Is(err, unix.ELOOP) {
			return fmt.Errorf("datastore: refusing symbolic-link database artifact %s", sourcePath)
		}
		return fmt.Errorf("datastore: open recovery recheck source %s: %w", sourcePath, err)
	}
	source := os.NewFile(uintptr(fd), sourcePath)
	if source == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("datastore: wrap recovery recheck source descriptor %s", sourcePath)
	}
	defer func() {
		retErr = errors.Join(retErr, source.Close())
	}()
	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("datastore: inspect recovery recheck source %s: %w", sourcePath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("datastore: database artifact %s is not a regular file", sourcePath)
	}

	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, databaseFileMode)
	if err != nil {
		return fmt.Errorf("datastore: create recovery recheck copy %s: %w", destinationPath, err)
	}
	destinationOpen := true
	defer func() {
		if destinationOpen {
			retErr = errors.Join(retErr, destination.Close())
		}
	}()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return fmt.Errorf("datastore: write recovery recheck copy %s: %w", destinationPath, err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("datastore: read recovery recheck source %s: %w", sourcePath, readErr)
		}
	}
	if err := destination.Sync(); err != nil {
		return fmt.Errorf("datastore: sync recovery recheck copy %s: %w", destinationPath, err)
	}
	if err := destination.Close(); err != nil {
		return fmt.Errorf("datastore: close recovery recheck copy %s: %w", destinationPath, err)
	}
	destinationOpen = false
	return nil
}

func readOnlySQLiteDSN(path string) string {
	query := url.Values{}
	query.Set("mode", "ro")
	// unix-excl keeps WAL shared-memory state in-process instead of creating or
	// mutating a -shm companion. The application recovery lock already proves
	// this is the only cooperating opener, so exclusive VFS locking is safe and
	// lets the recheck preserve every corrupt artifact byte-for-byte.
	query.Set("vfs", "unix-excl")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "query_only(ON)")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

func databaseArtifactPaths(path string) []string {
	return []string{path, path + "-wal", path + "-shm"}
}

func rejectRecoveryArtifact(path string, required bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if required {
			return fmt.Errorf("datastore: database artifact %s does not exist", path)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("datastore: inspect recovery artifact %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("datastore: refusing symbolic-link database artifact %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("datastore: database artifact %s is not a regular file", path)
	}
	return nil
}

func collectRecoveryArtifacts(ctx context.Context, databasePath string) ([]recoveryArtifact, error) {
	artifacts := make([]recoveryArtifact, 0, 3)
	for _, path := range databaseArtifactPaths(databasePath) {
		artifact, err := inspectRecoveryArtifact(ctx, path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func inspectRecoveryArtifact(ctx context.Context, path string) (recoveryArtifact, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return recoveryArtifact{}, fs.ErrNotExist
		}
		if errors.Is(err, unix.ELOOP) {
			return recoveryArtifact{}, fmt.Errorf("datastore: refusing symbolic-link database artifact %s", path)
		}
		return recoveryArtifact{}, fmt.Errorf("datastore: open recovery artifact %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return recoveryArtifact{}, fmt.Errorf("datastore: wrap recovery artifact descriptor %s", path)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return recoveryArtifact{}, fmt.Errorf("datastore: inspect opened recovery artifact %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return recoveryArtifact{}, fmt.Errorf("datastore: database artifact %s is not a regular file", path)
	}
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return recoveryArtifact{}, err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			if _, err := hash.Write(buffer[:read]); err != nil {
				return recoveryArtifact{}, fmt.Errorf("datastore: hash recovery artifact %s: %w", path, err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return recoveryArtifact{}, fmt.Errorf("datastore: read recovery artifact %s: %w", path, readErr)
		}
	}
	return recoveryArtifact{
		Name:         filepath.Base(path),
		OriginalPath: path,
		Size:         info.Size(),
		Mode:         fmt.Sprintf("%04o", info.Mode().Perm()),
		ModTime:      info.ModTime().UTC(),
		SHA256:       hex.EncodeToString(hash.Sum(nil)),
		originalMode: info.Mode().Perm(),
	}, nil
}

func createQuarantineDirectory(databasePath string, createdAt time.Time, ops recoveryOperations) (string, error) {
	root := filepath.Join(filepath.Dir(databasePath), recoveryDirectoryName)
	if err := ensurePrivateDirectory(root); err != nil {
		return "", fmt.Errorf("datastore: prepare recovery directory: %w", err)
	}
	base := createdAt.UTC().Format("20060102T150405.000000000Z")
	for attempt := 0; attempt < 100; attempt++ {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%s-%02d", base, attempt)
		}
		path := filepath.Join(root, name)
		err := ops.mkdir(path, dataDirectoryMode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("datastore: create quarantine directory %s: %w", path, err)
		}
		if err := os.Chmod(path, dataDirectoryMode); err != nil {
			_ = ops.remove(path)
			return "", fmt.Errorf("datastore: secure quarantine directory %s: %w", path, err)
		}
		return path, nil
	}
	return "", fmt.Errorf("datastore: allocate unique quarantine below %s", root)
}

func writeRecoveryManifest(path string, manifest recoveryManifest) (retErr error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("datastore: encode recovery manifest: %w", err)
	}
	data = append(data, '\n')

	file, err := os.CreateTemp(filepath.Dir(path), ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("datastore: create recovery manifest temp file: %w", err)
	}
	tempPath := file.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(databaseFileMode); err != nil {
		_ = file.Close()
		return fmt.Errorf("datastore: secure recovery manifest temp file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("datastore: write recovery manifest temp file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("datastore: sync recovery manifest temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("datastore: close recovery manifest temp file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("datastore: publish recovery manifest: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func validateRecoveredStore(ctx context.Context, store *Store) error {
	check, err := pragmaCheck(ctx, store.db, `PRAGMA quick_check(1)`)
	if err != nil {
		return fmt.Errorf("quick integrity check: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(check), "ok") {
		return fmt.Errorf("quick integrity check returned %q", check)
	}
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version != CurrentSchemaVersion {
		return fmt.Errorf("schema version is %d; want %d", version, CurrentSchemaVersion)
	}
	issues, err := permissionIssues(store.path)
	if err != nil {
		return err
	}
	if len(issues) != 0 {
		return fmt.Errorf("insecure replacement permissions: %s", strings.Join(issues, "; "))
	}
	return nil
}

func rollbackRecovery(primary error, fresh *Store, replacementAttempted bool, quarantineDir string, moved []recoveryArtifact, manifest *recoveryManifest, ops recoveryOperations) error {
	errs := []error{primary}
	if fresh != nil {
		if err := fresh.Close(); err != nil {
			errs = append(errs, fmt.Errorf("datastore: rollback close replacement: %w", err))
		}
	}
	if replacementAttempted {
		for _, path := range databaseArtifactPaths(manifest.DatabasePath) {
			if err := ops.remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("datastore: rollback remove partial artifact %s: %w", path, err))
			}
		}
	}
	for i := len(moved) - 1; i >= 0; i-- {
		artifact := moved[i]
		if err := ops.rename(artifact.QuarantinePath, artifact.OriginalPath); err != nil {
			errs = append(errs, fmt.Errorf("datastore: rollback restore %s: %w", artifact.OriginalPath, err))
			continue
		}
		if err := os.Chmod(artifact.OriginalPath, artifact.originalMode); err != nil {
			errs = append(errs, fmt.Errorf("datastore: rollback restore permissions for %s: %w", artifact.OriginalPath, err))
		}
	}
	if err := syncDirectory(filepath.Dir(manifest.DatabasePath)); err != nil {
		errs = append(errs, fmt.Errorf("datastore: rollback sync database directory: %w", err))
	}

	rollbackFailed := len(errs) > 1
	manifestPath := filepath.Join(quarantineDir, RecoveryManifestFileName)
	if !rollbackFailed {
		if err := ops.remove(manifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("datastore: rollback remove manifest: %w", err))
			rollbackFailed = true
		}
		if !rollbackFailed {
			if err := ops.remove(quarantineDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("datastore: rollback remove quarantine directory: %w", err))
				rollbackFailed = true
			}
		}
	}
	if rollbackFailed {
		completedAt := ops.now().UTC()
		manifest.CompletedAt = &completedAt
		manifest.ReplacementOutcome = recoveryOutcomeRollbackFailed
		manifest.RollbackFailures = make([]string, 0, len(errs)-1)
		for _, err := range errs[1:] {
			manifest.RollbackFailures = append(manifest.RollbackFailures, err.Error())
		}
		if err := ops.writeManifest(manifestPath, *manifest); err != nil {
			errs = append(errs, fmt.Errorf("datastore: record rollback failure: %w", err))
		}
	}
	return errors.Join(errs...)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("datastore: open directory for sync %s: %w", path, err)
	}
	return errors.Join(dir.Sync(), dir.Close())
}
