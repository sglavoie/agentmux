// Package atomicfile replaces a file's contents in one step, so a reader never
// observes a partial write and a crash never leaves a truncated file behind.
//
// It exists apart from internal/cachefile because the two have opposite
// stakes. A cache is disposable: a corrupt one is rebuilt from the source and
// the worst outcome is wasted work. The files this package was extracted for —
// config.toml above all — are authoritative source artifacts. There is nothing
// to rebuild them from, so "the write was interrupted" and "the operator's
// configuration is gone" are the same sentence. cachefile now delegates here,
// which keeps one implementation without implying config is a cache.
//
// Every atomic replacement in the tree goes through Write or WriteStream. The
// options cover the ways callers legitimately differ (who creates the parent,
// whether a symlink target may be replaced, how hard the directory entry is
// made durable, how errors read); the mechanism itself — sibling temp, chmod,
// write, fsync, rename — is the same for all of them.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// DirSync is how hard the rename's directory entry is made durable.
type DirSync int

const (
	// SyncDirStrict opens and fsyncs the parent after the rename and returns
	// any failure. It is the default: an authoritative file whose rename is
	// lost to a power cut is as gone as one that was never written.
	SyncDirStrict DirSync = iota
	// SyncDirBestEffort fsyncs the parent but ignores a failure to, for
	// callers whose replacement has already been observed as successful.
	SyncDirBestEffort
	// SyncDirNone skips the directory fsync. Only for disposable caches
	// written in bulk, where one fsync per entry is the cost and a lost
	// rename merely means the entry is rebuilt.
	SyncDirNone
)

// NotRegularError reports an existing target that RequireRegularTarget
// refused: a symlink, directory, FIFO, or anything else rename would replace
// without the caller meaning it to.
type NotRegularError struct{ Path string }

func (e *NotRegularError) Error() string { return e.Path + " is not a regular file" }

// ErrNotRegular is what errors.Is matches for a NotRegularError.
var ErrNotRegular = errors.New("not a regular file")

// Is lets errors.Is(err, ErrNotRegular) match.
func (e *NotRegularError) Is(target error) bool { return target == ErrNotRegular }

// Option adjusts one Write or WriteStream.
type Option func(*options)

type options struct {
	createParents  bool
	parentMode     os.FileMode
	requireRegular bool
	dirSync        DirSync
	tempPattern    string
	errorPrefix    string
}

// ParentMode sets the mode missing parent directories are created with
// (default 0755). It applies only to directories this call creates.
func ParentMode(mode os.FileMode) Option {
	return func(o *options) { o.createParents, o.parentMode = true, mode }
}

// NoCreateParents fails rather than creating a missing parent, for callers
// that prepare (and restrict) the directory themselves and would rather hear
// it vanished than have it recreated with another mode.
func NoCreateParents() Option { return func(o *options) { o.createParents = false } }

// RequireRegularTarget refuses to replace an existing path that is not a
// regular file. The check uses Lstat, so a symlink is refused rather than
// followed or silently replaced by a file.
func RequireRegularTarget() Option { return func(o *options) { o.requireRegular = true } }

// WithDirSync chooses how the rename is made durable (default SyncDirStrict).
func WithDirSync(mode DirSync) Option { return func(o *options) { o.dirSync = mode } }

// TempPattern overrides the os.CreateTemp pattern of the sibling temp file
// (default "."+base+".*.tmp": hidden, and ending in .tmp so sweeps that skip
// temps keep skipping it). It must contain exactly one '*'.
func TempPattern(pattern string) Option { return func(o *options) { o.tempPattern = pattern } }

// ErrorPrefix wraps every error returned as "<prefix>: <error>", so a caller
// keeps its package's voice without re-wrapping each step.
func ErrorPrefix(prefix string) Option { return func(o *options) { o.errorPrefix = prefix } }

// Write replaces path with data at the given mode.
//
// The temporary file is a sibling rather than a file in TMPDIR, because rename
// is only atomic within a filesystem and TMPDIR is routinely on another one.
// Its name is unique so a stale temp from a crashed older writer cannot be
// adopted, and it is removed on every failure path so a failed replacement
// leaves nothing but the original file.
//
// Both the file and its parent directory are synced (unless WithDirSync says
// otherwise). Syncing only the file makes the bytes durable while leaving the
// directory entry that points at them unwritten, which is exactly the case
// where a power loss loses the rename.
func Write(path string, data []byte, perm os.FileMode, opts ...Option) error {
	return WriteStream(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	}, opts...)
}

// WriteStream is Write for content produced by a writer, such as an archive
// encoded straight into the replacement. fill receives the temporary file; an
// error from it abandons the replacement exactly as a failed Write would, so
// the original stays in place and no temporary file survives.
func WriteStream(path string, perm os.FileMode, fill func(io.Writer) error, opts ...Option) error {
	o := options{createParents: true, parentMode: 0o755, dirSync: SyncDirStrict}
	for _, opt := range opts {
		opt(&o)
	}
	if o.tempPattern == "" {
		o.tempPattern = "." + filepath.Base(path) + ".*.tmp"
	}
	err := writeStream(path, perm, fill, o)
	if err != nil && o.errorPrefix != "" {
		return fmt.Errorf("%s: %w", o.errorPrefix, err)
	}
	return err
}

func writeStream(path string, perm os.FileMode, fill func(io.Writer) error, o options) (retErr error) {
	dir := filepath.Dir(path)
	if o.createParents {
		if err := os.MkdirAll(dir, o.parentMode); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if o.requireRegular {
		info, err := os.Lstat(path)
		if err == nil && !info.Mode().IsRegular() {
			return &NotRegularError{Path: path}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspecting %s: %w", path, err)
		}
	}
	tmp, err := os.CreateTemp(dir, o.tempPattern)
	if err != nil {
		return fmt.Errorf("creating a temporary file beside %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	// Chmod rather than trusting CreateTemp's 0600: the replacement must land
	// with the mode the caller asked for, not the one the temp happened to get,
	// and Chmod is not narrowed by the umask.
	if err := chmodFile(tmp, perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := fill(writerFor(tmp)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	switch o.dirSync {
	case SyncDirNone:
		return nil
	case SyncDirBestEffort:
		_ = syncDir(dir)
		return nil
	default:
		return syncDir(dir)
	}
}

// syncDir fsyncs a directory so a rename inside it survives a power loss. It
// is a variable so the package's tests can observe and fail it.
var syncDir = func(dir string) error {
	dirFile, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := dirFile.Sync()
	closeErr := dirFile.Close()
	return errors.Join(syncErr, closeErr)
}

// chmodFile and writerFor are the permission and write steps, variables so the
// package's tests can fail them part-way; production code never reassigns them.
var (
	chmodFile = func(f *os.File, perm os.FileMode) error { return f.Chmod(perm) }
	writerFor = func(f *os.File) io.Writer { return f }
)

// Replace rewrites an existing file, keeping the mode it already had.
//
// Preserving the mode is the point: an operator who tightened config.toml to
// 0600 did so deliberately, and a rewrite that silently widened it back to a
// default would be a quiet regression in a file that can name private paths.
// fallbackPerm applies only when there is no file yet.
//
// A path that is a symlink is replaced at its final target, so the link
// survives: an operator who keeps config.toml in a dotfiles repository and
// links it into place must find the link still there, pointing at the new
// contents, rather than a regular file that silently forked the two. A link
// whose target does not exist yet is replaced at the path itself, since there
// is nothing to keep it pointing at.
func Replace(path string, data []byte, fallbackPerm os.FileMode, opts ...Option) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		switch {
		case err == nil:
			path = target
		case !os.IsNotExist(err):
			return fmt.Errorf("resolving the symlink %s: %w", path, err)
		}
	}
	perm := fallbackPerm
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading the mode of %s: %w", path, err)
	}
	return Write(path, data, perm, opts...)
}
