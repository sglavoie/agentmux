package datastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	rebuildGateSuffix        = ".rebuild"
	rebuildStartupLockSuffix = ".rebuild-startup.lock"
)

// ErrRebuildInProgress reports that a process is rebuilding compatibility
// state after replacing the datastore. Daemons must not start while this gate
// is active: opening the fresh schema before the rebuild commits can leave a
// stale WAL view that later truncates the rebuilt database.
var ErrRebuildInProgress = errors.New("datastore: compatibility rebuild is in progress")

type rebuildGateMetadata struct {
	PID            int             `json:"pid"`
	RestartDaemon  bool            `json:"restart_daemon"`
	RebuildPending bool            `json:"rebuild_pending,omitempty"`
	RecoveryResult *RecoveryResult `json:"recovery_result,omitempty"`
}

// RebuildGate is an exclusive, cross-exec lease published beside the database.
// Its descriptor deliberately survives the dashboard's recovery re-exec. Once
// replacement succeeds, its marker also becomes durable so process exit cannot
// reopen daemon startup before a fresh owner finishes the rebuild.
type RebuildGate struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	metadata rebuildGateMetadata
	// markerRemoved lets a claimed fresh owner retry directory sync without
	// attempting to unlink the durable marker twice.
	markerRemoved bool
	released      bool
}

// DaemonStartupLease serializes daemon startup with rebuild-gate publication.
// The stable lock file is never removed; descriptor locking defines ownership.
type DaemonStartupLease struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	released bool
}

// BeginDefaultRebuildGate blocks daemon startup until the caller completes or
// abandons the post-recovery compatibility rebuild.
func BeginDefaultRebuildGate() (*RebuildGate, error) {
	return BeginDefaultRebuildGateContext(context.Background())
}

// BeginDefaultRebuildGateContext is the cancellable form used by automatic
// recovery while it waits for an in-flight daemon startup to become visible.
func BeginDefaultRebuildGateContext(ctx context.Context) (*RebuildGate, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return BeginRebuildGateContext(ctx, path)
}

// BeginRebuildGate publishes an exclusive rebuild lease for databasePath.
func BeginRebuildGate(databasePath string) (*RebuildGate, error) {
	return BeginRebuildGateContext(context.Background(), databasePath)
}

// BeginRebuildGateContext publishes an exclusive rebuild lease while holding
// the same stable lock every daemon startup must cross.
func BeginRebuildGateContext(ctx context.Context, databasePath string) (*RebuildGate, error) {
	if ctx == nil {
		return nil, errors.New("datastore: rebuild gate context is nil")
	}
	absPath, err := prepareDatabasePath(databasePath)
	if err != nil {
		return nil, err
	}
	startupLease, err := acquireDaemonStartupLease(ctx, absPath)
	if err != nil {
		return nil, err
	}
	gate, gateErr := beginRebuildGateLocked(absPath)
	releaseErr := startupLease.Release()
	if gateErr != nil {
		return nil, errors.Join(gateErr, releaseErr)
	}
	if releaseErr != nil {
		return nil, errors.Join(
			fmt.Errorf("datastore: releasing daemon startup serialization after rebuild publication: %w", releaseErr),
			gate.Release(),
		)
	}
	return gate, nil
}

func beginRebuildGateLocked(absPath string) (*RebuildGate, error) {
	markerPath := rebuildGatePath(absPath)
	for attempt := 0; attempt < 2; attempt++ {
		active, err := RebuildGateActive(absPath)
		if err != nil {
			return nil, err
		}
		if active {
			return nil, ErrRebuildInProgress
		}

		gate, err := createRebuildGate(markerPath)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return gate, err
	}
	return nil, ErrRebuildInProgress
}

// BeginDefaultDaemonStartup starts a serialized daemon-startup attempt. A
// successful caller must hold the returned lease until its socket can serve
// shutdown requests.
func BeginDefaultDaemonStartup(ctx context.Context) (*DaemonStartupLease, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return BeginDaemonStartup(ctx, path)
}

// BeginDaemonStartup serializes startup against rebuild publication and rejects
// the attempt when recovery already owns the rebuild gate.
func BeginDaemonStartup(ctx context.Context, databasePath string) (*DaemonStartupLease, error) {
	if ctx == nil {
		return nil, errors.New("datastore: daemon startup context is nil")
	}
	absPath, err := prepareDatabasePath(databasePath)
	if err != nil {
		return nil, err
	}
	lease, err := acquireDaemonStartupLease(ctx, absPath)
	if err != nil {
		return nil, err
	}
	active, activeErr := RebuildGateActive(absPath)
	if activeErr != nil {
		return nil, errors.Join(activeErr, lease.Release())
	}
	if active {
		return nil, errors.Join(ErrRebuildInProgress, lease.Release())
	}
	return lease, nil
}

func acquireDaemonStartupLease(ctx context.Context, databasePath string) (*DaemonStartupLease, error) {
	path := databasePath + rebuildStartupLockSuffix
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(databaseFileMode))
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("datastore: refusing symbolic-link daemon startup lock %s", path)
		}
		return nil, fmt.Errorf("datastore: opening daemon startup lock %s without following links: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("datastore: wrapping daemon startup lock %s", path)
	}
	lease := &DaemonStartupLease{file: file, path: path}
	closeOnError := func(primary error) error {
		return errors.Join(primary, lease.Release())
	}
	info, err := file.Stat()
	if err != nil {
		return nil, closeOnError(fmt.Errorf("datastore: inspecting daemon startup lock %s: %w", path, err))
	}
	if !info.Mode().IsRegular() {
		return nil, closeOnError(fmt.Errorf("datastore: daemon startup lock %s is not a regular file", path))
	}
	if err := unix.Fchmod(fd, uint32(databaseFileMode)); err != nil {
		return nil, closeOnError(fmt.Errorf("datastore: securing daemon startup lock %s: %w", path, err))
	}
	if err := waitForRecoveryLock(ctx, fd, unix.LOCK_EX); err != nil {
		return nil, closeOnError(fmt.Errorf("datastore: acquiring daemon startup lock %s: %w", path, err))
	}
	return lease, nil
}

// Release ends daemon-startup serialization. It is safe to call more than once.
func (l *DaemonStartupLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("datastore: unlocking daemon startup lock %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("datastore: closing daemon startup lock %s: %w", l.path, closeErr)
	}
	return errors.Join(unlockErr, closeErr)
}

func createRebuildGate(markerPath string) (_ *RebuildGate, retErr error) {
	temp, err := os.CreateTemp(filepath.Dir(markerPath), ".rebuild-gate-*")
	if err != nil {
		return nil, fmt.Errorf("datastore: creating rebuild gate: %w", err)
	}
	tempPath := temp.Name()
	linked := false
	defer func() {
		if !linked || retErr != nil {
			_ = os.Remove(tempPath)
		}
		if retErr != nil {
			_ = unix.Flock(int(temp.Fd()), unix.LOCK_UN)
			retErr = errors.Join(retErr, temp.Close())
		}
	}()
	if err := temp.Chmod(databaseFileMode); err != nil {
		return nil, fmt.Errorf("datastore: securing rebuild gate: %w", err)
	}
	if err := unix.Flock(int(temp.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, fmt.Errorf("datastore: locking rebuild gate: %w", err)
	}
	// os.OpenFile descriptors are close-on-exec by default. Recovery re-execs
	// the dashboard at the same PID, so clear FD_CLOEXEC only for this lease.
	if _, err := unix.FcntlInt(temp.Fd(), unix.F_SETFD, 0); err != nil {
		return nil, fmt.Errorf("datastore: preserving rebuild gate across exec: %w", err)
	}
	gate := &RebuildGate{
		file: temp,
		path: markerPath,
		metadata: rebuildGateMetadata{
			PID: os.Getpid(),
		},
	}
	if err := gate.writeMetadataLocked(); err != nil {
		return nil, err
	}
	// Publish only after the descriptor is locked and durable. A hard link is
	// atomic and refuses to replace another recovery's live marker.
	if err := os.Link(tempPath, markerPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fs.ErrExist
		}
		return nil, fmt.Errorf("datastore: publishing rebuild gate: %w", err)
	}
	linked = true
	if err := os.Remove(tempPath); err != nil {
		return nil, fmt.Errorf("datastore: removing rebuild gate staging link: %w", err)
	}
	if err := syncDirectory(filepath.Dir(markerPath)); err != nil {
		return nil, fmt.Errorf("datastore: syncing rebuild gate directory: %w", err)
	}
	return gate, nil
}

// SetRestartDaemon records whether completing this rebuild must restore prior
// daemon liveness. The metadata survives the dashboard recovery re-exec.
func (g *RebuildGate) SetRestartDaemon(restart bool) error {
	if g == nil {
		return errors.New("datastore: setting restart intent on nil rebuild gate")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released || g.file == nil {
		return errors.New("datastore: setting restart intent on released rebuild gate")
	}
	g.metadata.RestartDaemon = restart
	return g.writeMetadataLocked()
}

// MarkRebuildPending makes a successfully replaced datastore fail closed even
// if the process that owns the advisory descriptor exits before compatibility
// rebuilding and checkpointing finish. A later dashboard process may claim
// this durable marker and complete the rebuild.
func (g *RebuildGate) MarkRebuildPending(result RecoveryResult) error {
	if g == nil {
		return errors.New("datastore: marking nil rebuild gate pending")
	}
	if !result.Replaced {
		return errors.New("datastore: cannot mark rebuild pending before replacement")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released || g.file == nil {
		return errors.New("datastore: marking released rebuild gate pending")
	}
	resultCopy := result
	g.metadata.RebuildPending = true
	g.metadata.RecoveryResult = &resultCopy
	return g.writeMetadataLocked()
}

func (g *RebuildGate) writeMetadataLocked() error {
	data, err := json.Marshal(g.metadata)
	if err != nil {
		return fmt.Errorf("datastore: encoding rebuild gate: %w", err)
	}
	data = append(data, '\n')
	if err := g.file.Truncate(0); err != nil {
		return fmt.Errorf("datastore: truncating rebuild gate: %w", err)
	}
	if _, err := g.file.Seek(0, 0); err != nil {
		return fmt.Errorf("datastore: rewinding rebuild gate: %w", err)
	}
	if _, err := g.file.Write(data); err != nil {
		return fmt.Errorf("datastore: writing rebuild gate: %w", err)
	}
	if err := g.file.Sync(); err != nil {
		return fmt.Errorf("datastore: syncing rebuild gate: %w", err)
	}
	return nil
}

// Release removes this process's marker and closes its lease. It is used on
// pre-exec failures and by tests; successful recovery normally finishes from
// the re-execed process through FinishDefaultRebuildGate.
func (g *RebuildGate) Release() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released {
		return nil
	}
	g.released = true

	removeErr := removeOwnedRebuildMarker(g.path, g.file)
	unlockErr := unix.Flock(int(g.file.Fd()), unix.LOCK_UN)
	closeErr := g.file.Close()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("datastore: unlocking rebuild gate: %w", unlockErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("datastore: closing rebuild gate: %w", closeErr)
	}
	return errors.Join(removeErr, unlockErr, closeErr)
}

// Finish removes a rebuild gate held through a fresh-owner claim and returns
// the durable prior-daemon restart intent. The caller must invoke it only after
// compatibility import, workspace synchronization, and the truncating
// checkpoint have completed successfully.
func (g *RebuildGate) Finish() (bool, error) {
	if g == nil {
		return false, errors.New("datastore: finishing nil rebuild gate")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.released || g.file == nil {
		return false, errors.New("datastore: rebuild gate is already released")
	}

	restartDaemon := g.metadata.RestartDaemon
	if !g.markerRemoved {
		ownerInfo, err := g.file.Stat()
		if err != nil {
			return restartDaemon, fmt.Errorf("datastore: inspecting claimed rebuild gate owner: %w", err)
		}
		markerInfo, err := os.Lstat(g.path)
		if err != nil {
			return restartDaemon, fmt.Errorf("datastore: inspecting claimed rebuild gate marker: %w", err)
		}
		if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
			return restartDaemon, fmt.Errorf("datastore: rebuild gate %s is not a regular file", g.path)
		}
		if !os.SameFile(ownerInfo, markerInfo) {
			return restartDaemon, errors.New("datastore: claimed rebuild gate ownership changed before finish")
		}
		if err := os.Remove(g.path); err != nil {
			return restartDaemon, fmt.Errorf("datastore: removing claimed rebuild gate: %w", err)
		}
		g.markerRemoved = true
	}
	if err := syncDirectory(filepath.Dir(g.path)); err != nil {
		return restartDaemon, fmt.Errorf("datastore: syncing claimed rebuild gate removal: %w", err)
	}
	unlockErr := unix.Flock(int(g.file.Fd()), unix.LOCK_UN)
	closeErr := g.file.Close()
	g.released = true
	if unlockErr != nil {
		unlockErr = fmt.Errorf("datastore: unlocking finished rebuild gate: %w", unlockErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("datastore: closing finished rebuild gate: %w", closeErr)
	}
	return restartDaemon, errors.Join(unlockErr, closeErr)
}

func removeOwnedRebuildMarker(path string, owner *os.File) error {
	ownerInfo, err := owner.Stat()
	if err != nil {
		return fmt.Errorf("datastore: inspecting rebuild gate owner: %w", err)
	}
	markerInfo, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("datastore: inspecting rebuild gate marker: %w", err)
	}
	if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
		return fmt.Errorf("datastore: rebuild gate %s is not a regular file", path)
	}
	if !os.SameFile(ownerInfo, markerInfo) {
		return errors.New("datastore: rebuild gate ownership changed before release")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("datastore: removing rebuild gate: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

// RebuildGateActive reports whether databasePath's rebuild gate blocks daemon
// startup. An unlocked ordinary lease is stale crash residue and is removed;
// an unlocked post-replacement marker remains active until a fresh owner
// completes or explicitly repairs the pending rebuild.
func RebuildGateActive(databasePath string) (_ bool, retErr error) {
	markerPath := rebuildGatePath(databasePath)
	fd, err := unix.Open(markerPath, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if errors.Is(err, unix.ELOOP) {
		return false, fmt.Errorf("datastore: refusing symbolic-link rebuild gate %s", markerPath)
	}
	if err != nil {
		return false, fmt.Errorf("datastore: opening rebuild gate %s: %w", markerPath, err)
	}
	file := os.NewFile(uintptr(fd), markerPath)
	if file == nil {
		_ = unix.Close(fd)
		return false, fmt.Errorf("datastore: wrapping rebuild gate %s", markerPath)
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("datastore: inspecting rebuild gate %s: %w", markerPath, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("datastore: rebuild gate %s is not a regular file", markerPath)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return true, nil
		}
		return false, fmt.Errorf("datastore: checking rebuild gate %s: %w", markerPath, err)
	}
	metadata, err := readRebuildGateMetadata(file)
	if err != nil {
		return false, fmt.Errorf("datastore: reading unlocked rebuild gate %s: %w", markerPath, err)
	}
	if metadata.RebuildPending {
		return true, nil
	}
	markerInfo, err := os.Lstat(markerPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("datastore: inspecting stale rebuild gate %s: %w", markerPath, err)
	}
	if !os.SameFile(info, markerInfo) {
		return false, errors.New("datastore: rebuild gate changed during stale-marker cleanup")
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("datastore: removing stale rebuild gate %s: %w", markerPath, err)
	}
	if err := syncDirectory(filepath.Dir(markerPath)); err != nil {
		return false, fmt.Errorf("datastore: syncing stale rebuild gate removal: %w", err)
	}
	return false, nil
}

func readRebuildGateMetadata(file *os.File) (rebuildGateMetadata, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return rebuildGateMetadata{}, fmt.Errorf("rewinding rebuild gate: %w", err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return rebuildGateMetadata{}, fmt.Errorf("reading rebuild gate: %w", err)
	}
	var metadata rebuildGateMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return rebuildGateMetadata{}, fmt.Errorf("decoding rebuild gate: %w", err)
	}
	return metadata, nil
}

// DefaultRebuildGateActive applies RebuildGateActive to the production path.
func DefaultRebuildGateActive() (bool, error) {
	path, err := DefaultPath()
	if err != nil {
		return false, err
	}
	return RebuildGateActive(path)
}

// ClaimDefaultPendingRebuildGate claims an unlocked durable rebuild marker for
// a fresh dashboard process. A nil gate means no durable rebuild is pending.
func ClaimDefaultPendingRebuildGate(ctx context.Context) (*RebuildGate, RecoveryResult, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, RecoveryResult{}, err
	}
	return ClaimPendingRebuildGate(ctx, path)
}

// ClaimPendingRebuildGate claims databasePath's unlocked durable rebuild
// marker while serialized against daemon startup. The returned descriptor must
// remain owned until Finish succeeds; dropping it leaves the durable marker in
// place, so a later fresh process can retry safely.
func ClaimPendingRebuildGate(ctx context.Context, databasePath string) (*RebuildGate, RecoveryResult, error) {
	if ctx == nil {
		return nil, RecoveryResult{}, errors.New("datastore: pending rebuild claim context is nil")
	}
	absPath, err := prepareDatabasePath(databasePath)
	if err != nil {
		return nil, RecoveryResult{}, err
	}
	startupLease, err := acquireDaemonStartupLease(ctx, absPath)
	if err != nil {
		return nil, RecoveryResult{}, err
	}
	gate, result, claimErr := claimPendingRebuildGateLocked(absPath)
	releaseErr := startupLease.Release()
	if claimErr != nil {
		return nil, RecoveryResult{}, errors.Join(claimErr, releaseErr)
	}
	if releaseErr != nil {
		if gate != nil {
			_ = unix.Flock(int(gate.file.Fd()), unix.LOCK_UN)
			_ = gate.file.Close()
		}
		return nil, RecoveryResult{}, fmt.Errorf("datastore: releasing daemon startup serialization after rebuild claim: %w", releaseErr)
	}
	return gate, result, nil
}

func claimPendingRebuildGateLocked(databasePath string) (_ *RebuildGate, _ RecoveryResult, retErr error) {
	markerPath := rebuildGatePath(databasePath)
	fd, err := unix.Open(markerPath, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, RecoveryResult{}, nil
	}
	if errors.Is(err, unix.ELOOP) {
		return nil, RecoveryResult{}, fmt.Errorf("datastore: refusing symbolic-link rebuild gate %s", markerPath)
	}
	if err != nil {
		return nil, RecoveryResult{}, fmt.Errorf("datastore: opening pending rebuild gate %s: %w", markerPath, err)
	}
	file := os.NewFile(uintptr(fd), markerPath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, RecoveryResult{}, fmt.Errorf("datastore: wrapping pending rebuild gate %s", markerPath)
	}
	keepOpen := false
	defer func() {
		if !keepOpen {
			retErr = errors.Join(retErr, file.Close())
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, RecoveryResult{}, fmt.Errorf("datastore: inspecting pending rebuild gate %s: %w", markerPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, RecoveryResult{}, fmt.Errorf("datastore: rebuild gate %s is not a regular file", markerPath)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, RecoveryResult{}, ErrRebuildInProgress
		}
		return nil, RecoveryResult{}, fmt.Errorf("datastore: claiming pending rebuild gate %s: %w", markerPath, err)
	}
	metadata, err := readRebuildGateMetadata(file)
	if err != nil {
		return nil, RecoveryResult{}, fmt.Errorf("datastore: reading pending rebuild gate %s: %w", markerPath, err)
	}
	if !metadata.RebuildPending {
		gate := &RebuildGate{file: file, path: markerPath, metadata: metadata}
		if err := gate.Release(); err != nil {
			return nil, RecoveryResult{}, fmt.Errorf("datastore: removing abandoned ordinary rebuild lease: %w", err)
		}
		keepOpen = true
		return nil, RecoveryResult{}, nil
	}
	if metadata.RecoveryResult == nil || !metadata.RecoveryResult.Replaced {
		return nil, RecoveryResult{}, errors.New("datastore: durable rebuild marker has no completed replacement result; explicit operator repair is required")
	}
	result := *metadata.RecoveryResult
	if result.DatabasePath != "" && filepath.Clean(result.DatabasePath) != databasePath {
		return nil, RecoveryResult{}, fmt.Errorf("datastore: durable rebuild marker targets %s, not %s", result.DatabasePath, databasePath)
	}
	gate := &RebuildGate{file: file, path: markerPath, metadata: metadata}
	keepOpen = true
	return gate, result, nil
}

// FinishDefaultRebuildGate removes the active recovery marker after a re-exec
// and returns whether the previously live daemon must be restarted.
func FinishDefaultRebuildGate() (bool, error) {
	path, err := DefaultPath()
	if err != nil {
		return false, err
	}
	return FinishRebuildGate(path)
}

// FinishRebuildGate completes a gate owned by the current PID. The original
// locked descriptor may have survived exec and is intentionally not required.
func FinishRebuildGate(databasePath string) (bool, error) {
	markerPath := rebuildGatePath(databasePath)
	fd, err := unix.Open(markerPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ELOOP) {
		return false, fmt.Errorf("datastore: refusing symbolic-link rebuild gate %s", markerPath)
	}
	if err != nil {
		return false, fmt.Errorf("datastore: opening rebuild gate: %w", err)
	}
	file := os.NewFile(uintptr(fd), markerPath)
	if file == nil {
		_ = unix.Close(fd)
		return false, fmt.Errorf("datastore: wrapping rebuild gate %s", markerPath)
	}
	defer func() { _ = file.Close() }()
	ownerInfo, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("datastore: inspecting opened rebuild gate: %w", err)
	}
	if !ownerInfo.Mode().IsRegular() {
		return false, fmt.Errorf("datastore: rebuild gate %s is not a regular file", markerPath)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return false, fmt.Errorf("datastore: reading rebuild gate: %w", err)
	}
	var metadata rebuildGateMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return false, fmt.Errorf("datastore: decoding rebuild gate: %w", err)
	}
	if metadata.PID != os.Getpid() {
		return false, fmt.Errorf("datastore: rebuild gate belongs to pid %d, current pid is %d", metadata.PID, os.Getpid())
	}
	markerInfo, err := os.Lstat(markerPath)
	if err != nil {
		return false, fmt.Errorf("datastore: inspecting rebuild gate: %w", err)
	}
	if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
		return false, fmt.Errorf("datastore: rebuild gate %s is not a regular file", markerPath)
	}
	if !os.SameFile(ownerInfo, markerInfo) {
		return false, errors.New("datastore: rebuild gate ownership changed before finish")
	}
	if err := os.Remove(markerPath); err != nil {
		return false, fmt.Errorf("datastore: removing rebuild gate: %w", err)
	}
	if err := syncDirectory(filepath.Dir(markerPath)); err != nil {
		return false, fmt.Errorf("datastore: syncing rebuild gate removal: %w", err)
	}
	return metadata.RestartDaemon, nil
}

func rebuildGatePath(databasePath string) string {
	return filepath.Clean(databasePath) + rebuildGateSuffix
}
