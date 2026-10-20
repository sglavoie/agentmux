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

	"github.com/sglavoie/agentmux/internal/atomicfile"
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
	// The directory was just made private and real, so the write neither
	// creates parents nor follows a symlink or replaces a non-regular target
	// planted where the artifact goes. The directory fsync stays best effort:
	// by then the artifact is published, and failing the handoff over it would
	// strand an artifact the caller was told did not land.
	if err := atomicfile.Write(path, request.Content, artifactFileMode,
		atomicfile.NoCreateParents(),
		atomicfile.RequireRegularTarget(),
		atomicfile.WithDirSync(atomicfile.SyncDirBestEffort),
		atomicfile.ErrorPrefix("handoff: writing artifact"),
	); err != nil {
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
