package handoff

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const (
	artifactDirectoryMode fs.FileMode = 0o700
	artifactFileMode      fs.FileMode = 0o600
	StatusDraft                       = "draft"
	StatusFinal                       = "final"
)

var artifactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type WriteRequest struct {
	ID        string
	Status    string
	Content   []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Artifact is the complete local metadata for one generated handoff file.
// The file itself is authoritative; this metadata is returned to callers and
// is not persisted in a database.
type Artifact struct {
	ID          string
	Path        string
	ContentHash string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ArtifactStore writes private Markdown artifacts. Draft and final writes use
// distinct filenames so an edited draft remains inspectable after finalization.
type ArtifactStore struct {
	directory string
}

func DefaultDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if err == nil {
			err = errors.New("home directory is empty")
		}
		return "", fmt.Errorf("handoff: resolving home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "handoffs"), nil
}

func NewArtifactStore(directory string) (*ArtifactStore, error) {
	if directory == "" {
		return nil, errors.New("handoff: artifact directory is empty")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("handoff: resolving artifact directory: %w", err)
	}
	return &ArtifactStore{directory: filepath.Clean(abs)}, nil
}

func (s *ArtifactStore) Write(request WriteRequest) (Artifact, error) {
	if !artifactIDPattern.MatchString(request.ID) {
		return Artifact{}, fmt.Errorf("handoff: invalid artifact ID %q", request.ID)
	}
	if request.Status != StatusDraft && request.Status != StatusFinal {
		return Artifact{}, fmt.Errorf("handoff: invalid artifact status %q", request.Status)
	}
	if len(request.Content) == 0 {
		return Artifact{}, errors.New("handoff: artifact content is empty")
	}
	if err := ensurePrivateDirectory(s.directory); err != nil {
		return Artifact{}, err
	}
	suffix := ".md"
	if request.Status == StatusDraft {
		suffix = ".draft.md"
	}
	path := filepath.Join(s.directory, request.ID+suffix)
	if err := validateArtifactTarget(path); err != nil {
		return Artifact{}, err
	}
	if err := atomicWritePrivate(path, request.Content); err != nil {
		return Artifact{}, err
	}
	sum := sha256.Sum256(request.Content)
	now := time.Now().UTC()
	if request.CreatedAt.IsZero() {
		request.CreatedAt = now
	}
	if request.UpdatedAt.IsZero() {
		request.UpdatedAt = now
	}
	return Artifact{
		ID: request.ID, Path: path, ContentHash: fmt.Sprintf("%x", sum[:]), Status: request.Status,
		CreatedAt: request.CreatedAt, UpdatedAt: request.UpdatedAt,
	}, nil
}

func validateArtifactTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("handoff: inspecting existing artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("handoff: existing artifact %s is not a regular file", path)
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("handoff: artifact path %s is not a real directory", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("handoff: inspecting artifact directory: %w", err)
	}
	if err := os.MkdirAll(path, artifactDirectoryMode); err != nil {
		return fmt.Errorf("handoff: creating artifact directory: %w", err)
	}
	if err := os.Chmod(path, artifactDirectoryMode); err != nil {
		return fmt.Errorf("handoff: restricting artifact directory: %w", err)
	}
	return nil
}

func atomicWritePrivate(path string, data []byte) (retErr error) {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("handoff: creating temporary artifact: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(artifactFileMode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("handoff: restricting temporary artifact: %w", err)
	}
	n, err := temp.Write(data)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("handoff: writing temporary artifact: %w", err)
	}
	if n != len(data) {
		_ = temp.Close()
		return fmt.Errorf("handoff: short artifact write: wrote %d of %d bytes", n, len(data))
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("handoff: syncing temporary artifact: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("handoff: closing temporary artifact: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("handoff: publishing artifact: %w", err)
	}
	if directoryHandle, err := os.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}
