package handoff

import (
	"context"
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
)

var artifactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type WriteRequest struct {
	ID             string
	Status         string
	WorkspaceID    string
	ConversationID *int64
	PlanID         string
	Title          string
	Content        []byte
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Artifact struct {
	Record Record
}

// ArtifactStore writes private Markdown artifacts and records their hashes.
// Draft and final writes use distinct filenames so an edited draft can remain
// inspectable after finalization.
type ArtifactStore struct {
	directory  string
	repository *Repository
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

func NewArtifactStore(directory string, repository *Repository) (*ArtifactStore, error) {
	if repository == nil {
		return nil, errors.New("handoff: artifact store requires a metadata repository")
	}
	if directory == "" {
		return nil, errors.New("handoff: artifact directory is empty")
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("handoff: resolving artifact directory: %w", err)
	}
	return &ArtifactStore{directory: filepath.Clean(abs), repository: repository}, nil
}

func (s *ArtifactStore) Write(ctx context.Context, request WriteRequest) (Artifact, error) {
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
	previous, existed, err := readExistingArtifact(path)
	if err != nil {
		return Artifact{}, err
	}
	if err := atomicWritePrivate(path, request.Content); err != nil {
		return Artifact{}, err
	}
	sum := sha256.Sum256(request.Content)
	record, err := s.repository.Save(ctx, Record{
		ID: request.ID, WorkspaceID: request.WorkspaceID, ConversationID: request.ConversationID,
		PlanID: request.PlanID, Path: path, ContentHash: fmt.Sprintf("%x", sum[:]), Status: request.Status,
		CreatedAt: request.CreatedAt, UpdatedAt: request.UpdatedAt,
	}, request.Title, request.Content)
	if err != nil {
		if existed {
			if restoreErr := atomicWritePrivate(path, previous); restoreErr != nil {
				return Artifact{}, fmt.Errorf("%w; restoring previous artifact: %v", err, restoreErr)
			}
		} else {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return Artifact{}, fmt.Errorf("%w; removing unindexed artifact: %v", err, removeErr)
			}
		}
		return Artifact{}, err
	}
	return Artifact{Record: record}, nil
}

func readExistingArtifact(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("handoff: inspecting existing artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("handoff: existing artifact %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("handoff: reading existing artifact: %w", err)
	}
	return data, true, nil
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
