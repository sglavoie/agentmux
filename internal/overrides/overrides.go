package overrides

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/atomicfile"
)

// PathEnvVar is the environment variable that overrides the default file path (for testing).
const PathEnvVar = "AMX_OVERRIDES_PATH"

// CurrentVersion is the launcher-state schema written by current builds.
// Version 1 contained profile overrides only; version 2 adds pinned projects.
const CurrentVersion = 2

// Entry records a persisted one-time profile override for a project.
type Entry struct {
	ProjectTitle string `json:"project_title"`
	ProfileName  string `json:"profile_name"`
}

// PinnedProject identifies a configured project by its default launch profile
// and title. A one-time profile override does not change this identity.
type PinnedProject struct {
	ProfileName  string `json:"profile_name"`
	ProjectTitle string `json:"project_title"`
}

// File is the JSON structure written to disk.
type File struct {
	Version        int             `json:"version"`
	Entries        []Entry         `json:"entries"`
	PinnedProjects []PinnedProject `json:"pinned_projects"`
}

// Path returns the path to the overrides file.
// Override via AMX_OVERRIDES_PATH for testing.
func Path() (string, error) {
	if p := os.Getenv(PathEnvVar); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "launch_overrides.json"), nil
}

// Load reads the overrides file. Returns an empty File if the path does not exist.
func Load() (File, error) {
	path, err := Path()
	if err != nil {
		return File{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return File{
			Version:        CurrentVersion,
			Entries:        []Entry{},
			PinnedProjects: []PinnedProject{},
		}, nil
	}
	if err != nil {
		return File{}, err
	}
	var fp *File
	if err := json.Unmarshal(data, &fp); err != nil {
		return File{}, err
	}
	if fp == nil {
		return File{}, errors.New("overrides file must contain a JSON object")
	}
	f := *fp
	if f.Version == 0 {
		f.Version = CurrentVersion
	}
	if err := validateVersion(f.Version); err != nil {
		return File{}, err
	}
	if f.Entries == nil {
		f.Entries = []Entry{}
	}
	if f.PinnedProjects == nil {
		f.PinnedProjects = []PinnedProject{}
	}
	return f, nil
}

// Save writes the overrides file atomically (internal/atomicfile): a unique
// sibling temp, fsynced and renamed over the destination, removed on any
// failure so a partial write never lingers as garbage on disk.
func Save(f File) error {
	path, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if f.Version == 0 {
		f.Version = CurrentVersion
	}
	if err := validateVersion(f.Version); err != nil {
		return err
	}
	if f.Entries == nil {
		f.Entries = []Entry{}
	}
	if f.PinnedProjects == nil {
		f.PinnedProjects = []PinnedProject{}
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	// launch_overrides.json is a user asset, not a cache: it gets the same
	// durability as config.toml, directory fsync included.
	return atomicfile.Write(path, data, 0o644)
}

func validateVersion(version int) error {
	if version < 1 {
		return fmt.Errorf("overrides file version must be at least 1, got %d", version)
	}
	if version > CurrentVersion {
		return fmt.Errorf("unsupported overrides file version %d; current version is %d", version, CurrentVersion)
	}
	return nil
}
