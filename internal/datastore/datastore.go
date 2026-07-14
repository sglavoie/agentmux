// Package datastore owns Agent Mux's durable SQLite database. It keeps the
// connection policy, filesystem permissions, migrations, and transaction
// boundaries in one place so repositories do not need to duplicate them.
package datastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const (
	// ApplicationName is the directory created below ~/.local/share.
	ApplicationName = "agent-mux"
	// DatabaseFileName is the stable name for the SQLite database.
	DatabaseFileName = "agent-mux.db"
	// CurrentSchemaVersion is the newest schema this binary understands.
	CurrentSchemaVersion = 5

	dataDirectoryMode os.FileMode = 0o700
	databaseFileMode  os.FileMode = 0o600

	busyRetryAttempts = 8
	busyRetryMinDelay = 10 * time.Millisecond
	busyRetryMaxDelay = 250 * time.Millisecond
	sqliteBusyCode    = 5
	sqliteCorruptCode = 11
)

// ErrFutureSchemaVersion reports a database created by a newer Agent Mux.
// Opening it for writing could discard data the current binary does not
// understand, so callers must upgrade rather than proceed.
var ErrFutureSchemaVersion = errors.New("datastore: unsupported future schema version")

// Store is an opened, migrated SQLite database.
type Store struct {
	db           *sql.DB
	path         string
	recoveryLock *recoveryLock
	closeMu      sync.Mutex
	closed       bool
}

// DefaultPath returns the location for Agent Mux's durable database. It
// intentionally matches the existing transcript, plan, and daemon data tree.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if err == nil {
			err = errors.New("home directory is empty")
		}
		return "", fmt.Errorf("datastore: resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", ApplicationName, DatabaseFileName), nil
}

// Open opens the default datastore, creating its directory and database with
// private permissions when needed.
func Open(ctx context.Context) (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return OpenPath(ctx, path)
}

// OpenPath opens and migrates the database at path. It is primarily useful to
// callers that explicitly manage an Agent Mux data directory and to tests.
func OpenPath(ctx context.Context, path string) (*Store, error) {
	absPath, err := prepareDatabasePath(path)
	if err != nil {
		return nil, err
	}
	lock, err := acquireSharedRecoveryLock(ctx, absPath)
	if err != nil {
		return nil, err
	}
	store, err := openPathUnderRecoveryLock(ctx, absPath)
	if err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	store.recoveryLock = lock
	return store, nil
}

// prepareDatabasePath resolves the stable path and creates its private parent
// directory. Callers performing recovery can use it before acquiring the
// exclusive recovery lock, then call openPathUnderRecoveryLock without
// reacquiring or downgrading it.
func prepareDatabasePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("datastore: database path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("datastore: resolving database path %q: %w", path, err)
	}
	absPath = filepath.Clean(absPath)
	if err := ensurePrivateDirectory(filepath.Dir(absPath)); err != nil {
		return "", err
	}
	return absPath, nil
}

// openPathUnderRecoveryLock opens and initializes absPath while its caller
// holds either a shared or exclusive recovery lock. The caller retains lock
// ownership on both success and failure, allowing recovery to keep exclusive
// ownership through validation and rollback. Every failure closes the SQL
// pool; an ordinary OpenPath additionally closes its shared lock.
func openPathUnderRecoveryLock(ctx context.Context, absPath string) (_ *Store, retErr error) {
	store := &Store{path: absPath}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, store.Close())
		}
	}()

	if err := rejectSymlink(absPath, "database"); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", sqliteDSN(absPath))
	if err != nil {
		return nil, fmt.Errorf("datastore: opening %s: %w", absPath, err)
	}
	store.db = db
	// WAL permits readers to continue while a writer has a transaction. SQLite
	// still has one writer at a time; the busy timeout in the DSN waits for it
	// instead of immediately surfacing a transient "database is locked" error.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)

	if err := retrySQLiteBusy(ctx, func() error { return db.PingContext(ctx) }); err != nil {
		return nil, fmt.Errorf("datastore: connecting to %s: %w", absPath, err)
	}
	if err := store.enforcePermissions(); err != nil {
		return nil, err
	}
	if err := migrate(ctx, db, migrations); err != nil {
		return nil, err
	}
	if err := configureWAL(ctx, db); err != nil {
		return nil, err
	}
	if err := store.enforcePermissions(); err != nil {
		return nil, err
	}
	return store, nil
}

// OpenTest returns an isolated, automatically cleaned-up file-backed
// datastore. It is intentionally file-backed rather than :memory: so tests
// exercise the same WAL, locking, and migration paths as production.
func OpenTest(t testing.TB) *Store {
	t.Helper()
	store, err := OpenPath(context.Background(), filepath.Join(t.TempDir(), DatabaseFileName))
	if err != nil {
		t.Fatalf("open test datastore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close test datastore: %v", err)
		}
	})
	return store
}

// DB exposes the shared database handle for repository queries. Repository
// mutations should use Transaction so a failure cannot leave a partial update.
func (s *Store) DB() *sql.DB { return s.db }

// Path returns the absolute path of the opened database.
func (s *Store) Path() string { return s.path }

// Close releases all database connections before releasing the recovery lock.
// It preserves errors from both operations and is safe to call repeatedly.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var dbErr error
	if s.db != nil {
		dbErr = s.db.Close()
	}
	var lockErr error
	if s.recoveryLock != nil {
		lockErr = s.recoveryLock.Close()
	}
	return errors.Join(dbErr, lockErr)
}

// Checkpoint flushes every committed WAL frame into the main database and
// truncates the WAL. Recovery calls it after rebuilding compatibility state and
// before daemon startup is re-enabled, establishing one durable publication
// boundary for the replacement database.
func (s *Store) Checkpoint(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("datastore: checkpoint on nil store")
	}
	var busy, logFrames, checkpointed int
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("datastore: checkpointing WAL: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("datastore: checkpointing WAL remained busy (%d of %d frames checkpointed)", checkpointed, logFrames)
	}
	return s.enforcePermissions()
}

// SchemaVersion returns the latest migration recorded in this database.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("datastore: schema version on nil store")
	}
	return schemaVersion(ctx, s.db)
}

// Transaction executes fn in one SQLite transaction. It rolls back fn errors
// and commit failures, and restores restrictive database-file permissions after
// successful writes that may have created WAL sidecars.
func (s *Store) Transaction(ctx context.Context, fn func(*sql.Tx) error) (retErr error) {
	if s == nil || s.db == nil {
		return errors.New("datastore: transaction on nil store")
	}
	if fn == nil {
		return errors.New("datastore: transaction function is nil")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("datastore: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return fmt.Errorf("datastore: transaction: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("datastore: commit transaction: %w", err)
	}
	if err := s.enforcePermissions(); err != nil {
		return err
	}
	return nil
}

func sqliteDSN(path string) string {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "synchronous(NORMAL)")
	// Begin write transactions immediately so a competing writer waits under
	// busy_timeout before it reads stale state and attempts its own migration.
	query.Set("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

// configureWAL changes SQLite's persistent journal mode only after schema
// initialization has committed. A failed PRAGMA that reports SQLITE_BUSY is
// safe to retry because SQLite did not change the journal mode. Other errors
// return immediately, and a successful statement is never repeated.
func configureWAL(ctx context.Context, db *sql.DB) error {
	var journalMode string
	err := retrySQLiteBusy(ctx, func() error {
		return db.QueryRowContext(ctx, `PRAGMA journal_mode(WAL)`).Scan(&journalMode)
	})
	if err != nil {
		return fmt.Errorf("datastore: enabling WAL journal mode: %w", err)
	}
	if journalMode != "wal" {
		return fmt.Errorf("datastore: enabling WAL journal mode returned %q", journalMode)
	}
	return nil
}

// retrySQLiteBusy bounds retries of operations whose SQLITE_BUSY result means
// they are known not to have executed. Callers must not use it around commits
// or arbitrary transaction callbacks because their outcome may be ambiguous.
func retrySQLiteBusy(ctx context.Context, operation func() error) error {
	var err error
	for attempt := 0; attempt < busyRetryAttempts; attempt++ {
		if err = operation(); err == nil || !isSQLiteBusy(err) {
			return err
		}
		if attempt == busyRetryAttempts-1 {
			break
		}

		delay := busyRetryMinDelay << attempt
		if delay > busyRetryMaxDelay {
			delay = busyRetryMaxDelay
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func isSQLiteBusy(err error) bool {
	var sqliteErr interface{ Code() int }
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqliteBusyCode
}

// IsCorruption reports whether err wraps a modernc SQLite error whose primary
// result code is SQLITE_CORRUPT. Masking the low byte also recognizes extended
// corruption result codes without depending on localized error text.
func IsCorruption(err error) bool {
	var sqliteErr interface{ Code() int }
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqliteCorruptCode
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, dataDirectoryMode); err != nil {
		return fmt.Errorf("datastore: creating data directory %s: %w", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("datastore: inspecting data directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("datastore: data directory %s is not a directory", path)
	}
	if err := os.Chmod(path, dataDirectoryMode); err != nil {
		return fmt.Errorf("datastore: securing data directory %s: %w", path, err)
	}
	return nil
}

func rejectSymlink(path, kind string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("datastore: inspecting %s %s: %w", kind, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("datastore: refusing symbolic-link %s %s", kind, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("datastore: %s %s is not a regular file", kind, path)
	}
	return nil
}

// enforcePermissions restores the durable artifact permissions after SQLite
// creates or updates them. SQLite's VFS creates the database, WAL, and shared
// memory files using the process umask, so a brief creation interval can
// precede this chmod. That interval is intentionally accepted: the containing
// data directory is always 0700, preventing other users from traversing to a
// newly-created artifact. Changing the process-wide umask to eliminate it
// would be unsafe for Agent Mux as a library within a larger process.
//
// Every completed OpenPath, Transaction, and RebuildSearchIndex call enforces
// 0600 for each artifact that exists at its completion, including after
// concurrent transactions. Artifact paths are opened with O_NOFOLLOW and
// secured through their file descriptors, so replacing one with a symlink
// cannot redirect the permission change to its target.
func (s *Store) enforcePermissions() error {
	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := secureArtifact(path); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
	}
	return nil
}

// secureArtifact changes an artifact's mode through an already-open file
// descriptor. O_NOFOLLOW makes the final path component resolution and the
// descriptor acquisition one operation, closing the Lstat-then-Chmod race.
func secureArtifact(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return fmt.Errorf("datastore: refusing symbolic-link database artifact %s", path)
		}
		return fmt.Errorf("datastore: opening database artifact %s without following links: %w", path, err)
	}
	defer func() { _ = unix.Close(fd) }()

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("datastore: inspecting opened database artifact %s: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("datastore: database artifact %s is not a regular file", path)
	}
	if err := unix.Fchmod(fd, uint32(databaseFileMode)); err != nil {
		return fmt.Errorf("datastore: securing database artifact %s: %w", path, err)
	}
	return nil
}
