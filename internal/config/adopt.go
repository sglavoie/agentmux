package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

var (
	ErrConcurrentChange = errors.New("config: changed since adoption preview")
	ErrDuplicateProject = errors.New("config: project directory is already configured")
	adoptRename         = os.Rename
)

// AdoptionResult describes the exact append transaction that was published.
type AdoptionResult struct {
	Path       string
	BackupPath string
	Block      string
}

// ProjectAdopter is the action-layer seam for an explicit config mutation.
type ProjectAdopter interface {
	AdoptProject(context.Context, Session) (AdoptionResult, error)
}

// FileAdopter appends one project while preserving all existing TOML bytes.
// expected is the byte snapshot used to produce the confirmation preview.
type FileAdopter struct {
	path     string
	expected []byte
}

func NewFileAdopter(path string, expected []byte) *FileAdopter {
	return &FileAdopter{path: path, expected: append([]byte(nil), expected...)}
}

// ProjectBlock renders the canonical block shown before confirmation and
// appended byte-for-byte after confirmation.
func ProjectBlock(project Session) (string, error) {
	type projectBlock struct {
		ID        string `toml:"id,omitempty"`
		Title     string `toml:"title"`
		Directory string `toml:"directory"`
		Profile   string `toml:"profile"`
	}
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(struct {
		Projects []projectBlock `toml:"projects"`
	}{Projects: []projectBlock{{
		ID: project.ID, Title: project.Title, Directory: project.Directory, Profile: project.EffectiveProfile(),
	}}}); err != nil {
		return "", fmt.Errorf("config: render project block: %w", err)
	}
	return strings.TrimSpace(out.String()) + "\n", nil
}

func (a *FileAdopter) AdoptProject(ctx context.Context, project Session) (AdoptionResult, error) {
	if a == nil || strings.TrimSpace(a.path) == "" {
		return AdoptionResult{}, errors.New("config: adoption path is required")
	}
	block, err := ProjectBlock(project)
	if err != nil {
		return AdoptionResult{}, err
	}
	lock, err := acquireConfigLock(ctx, a.path+".lock")
	if err != nil {
		return AdoptionResult{}, err
	}
	defer func() { _ = releaseConfigLock(lock) }()

	original, info, err := readRegularConfig(a.path)
	if err != nil {
		return AdoptionResult{}, err
	}
	if !bytes.Equal(original, a.expected) {
		return AdoptionResult{}, ErrConcurrentChange
	}
	current, err := parseConfig(original)
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("config: parse current file: %w", err)
	}
	if duplicateProjectDirectory(current.Projects, project.Directory) {
		return AdoptionResult{}, fmt.Errorf("%w: %s", ErrDuplicateProject, project.Directory)
	}

	candidate := appendProjectBlock(original, block)
	parsed, err := parseConfig(candidate)
	if err != nil {
		return AdoptionResult{}, fmt.Errorf("config: appended project does not parse: %w", err)
	}
	if err := Validate(parsed); err != nil {
		return AdoptionResult{}, fmt.Errorf("config: appended project is invalid: %w", err)
	}

	mode := info.Mode().Perm()
	tempPath, err := writeTempFile(filepath.Dir(a.path), ".config-adopt-*", candidate, mode)
	if err != nil {
		return AdoptionResult{}, err
	}
	defer func() { _ = os.Remove(tempPath) }()

	backupPath := a.path + ".bak"
	backupTemp, err := writeTempFile(filepath.Dir(a.path), ".config-backup-*", original, mode)
	if err != nil {
		return AdoptionResult{}, err
	}
	defer func() { _ = os.Remove(backupTemp) }()
	if err := adoptRename(backupTemp, backupPath); err != nil {
		return AdoptionResult{}, fmt.Errorf("config: publish backup: %w", err)
	}
	if err := adoptRename(tempPath, a.path); err != nil {
		return AdoptionResult{}, fmt.Errorf("config: publish adopted project: %w", err)
	}
	if err := syncDirectory(filepath.Dir(a.path)); err != nil {
		// Restore from the already-fsynced backup when durability publication
		// cannot be confirmed.
		_ = copyFileAtomic(backupPath, a.path, mode)
		return AdoptionResult{}, fmt.Errorf("config: sync adopted project: %w", err)
	}
	return AdoptionResult{Path: a.path, BackupPath: backupPath, Block: block}, nil
}

func appendProjectBlock(original []byte, block string) []byte {
	out := append([]byte(nil), original...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n\n")) {
		out = append(out, '\n')
	}
	return append(out, block...)
}

func readRegularConfig(path string) ([]byte, fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("config: %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	return data, info, err
}

func canonicalDirectory(path string) string {
	path = ExpandTilde(strings.TrimSpace(path))
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if physical, err := filepath.EvalSymlinks(abs); err == nil {
		abs = physical
	}
	return filepath.Clean(abs)
}

func duplicateProjectDirectory(projects []Session, directory string) bool {
	target := canonicalDirectory(directory)
	for _, project := range projects {
		if canonicalDirectory(project.Directory) == target {
			return true
		}
	}
	return false
}

func acquireConfigLock(ctx context.Context, path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("config: open adoption lock: %w", err)
	}
	_ = f.Chmod(0o600)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, fmt.Errorf("config: acquire adoption lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("config: acquire adoption lock: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func releaseConfigLock(f *os.File) error {
	if f == nil {
		return nil
	}
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return errors.Join(err, f.Close())
}

func writeTempFile(dir, pattern string, data []byte, mode fs.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("config: create temporary file: %w", err)
	}
	path := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		return "", fmt.Errorf("config: preserve permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("config: write temporary file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("config: sync temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("config: close temporary file: %w", err)
	}
	ok = true
	return path, nil
}

func copyFileAtomic(source, target string, mode fs.FileMode) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	temp, err := writeTempFile(filepath.Dir(target), ".config-restore-*", data, mode)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp) }()
	return os.Rename(temp, target)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
