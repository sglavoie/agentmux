package runtimesession

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/cachefile"
)

const (
	// SchemaVersion is the only registry file schema this build accepts.
	SchemaVersion = 1
	fileMode      = 0o600
)

// ErrCorrupt reports a registry file this build cannot trust. The registry is
// authoritative, not a cache, so it is never quarantined or rebuilt
// automatically: losing it would orphan every running agent.
var ErrCorrupt = errors.New("runtimesession: registry file is unreadable")

type registryFile struct {
	SchemaVersion int      `json:"schema_version"`
	Sessions      []Record `json:"sessions"`
}

// Store is the registry file. Every mutation reloads the file under the
// stable sidecar lock, applies the change, and atomically replaces the file,
// so concurrent processes serialize and a crash leaves the previous version.
type Store struct {
	path string
}

// NewStore returns a store for the registry file at path.
func NewStore(path string) (*Store, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("runtimesession: registry path %q must be a clean absolute path", path)
	}
	return &Store{path: path}, nil
}

// Path returns the registry file path.
func (s *Store) Path() string { return s.path }

// Load reads the registry without locking. Atomic replacement means it always
// sees a complete version; a missing file is an empty registry.
func (s *Store) Load() (*Registry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return NewRegistry(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("runtimesession: reading %s: %w", s.path, err)
	}
	return decodeRegistry(s.path, data)
}

// Update applies fn to the current registry and persists the result. If fn
// returns an error, or the result violates a registry invariant, nothing is
// written and the error is returned.
func (s *Store) Update(fn func(*Registry) error) error {
	if fn == nil {
		return errors.New("runtimesession: update requires a callback")
	}
	return cachefile.WithExclusive(s.path, func() error {
		registry, err := s.Load()
		if err != nil {
			return err
		}
		if err := fn(registry); err != nil {
			return err
		}
		data, err := encodeRegistry(registry)
		if err != nil {
			return err
		}
		if err := atomicfile.Write(s.path, data, fileMode); err != nil {
			return fmt.Errorf("runtimesession: writing %s: %w", s.path, err)
		}
		return nil
	})
}

func encodeRegistry(registry *Registry) ([]byte, error) {
	records := registry.sortedRecords()
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return nil, err
		}
	}
	if err := registry.checkInvariants(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(registryFile{SchemaVersion: SchemaVersion, Sessions: records}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("runtimesession: encoding registry: %w", err)
	}
	return append(data, '\n'), nil
}

func decodeRegistry(path string, data []byte) (*Registry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file registryFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrCorrupt, path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %s: trailing data after the registry", ErrCorrupt, path)
	}
	if file.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: %s: unsupported schema version %d; want %d", ErrCorrupt, path, file.SchemaVersion, SchemaVersion)
	}
	registry, err := registryFromRecords(file.Sessions)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrCorrupt, path, err)
	}
	return registry, nil
}
