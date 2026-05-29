package overrides

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PathEnvVar is the environment variable that overrides the default file path (for testing).
const PathEnvVar = "AMX_OVERRIDES_PATH"

// Entry records a pinned profile override for a project.
type Entry struct {
	ProjectTitle string `json:"project_title"`
	ProfileName  string `json:"profile_name"`
}

// File is the JSON structure written to disk.
type File struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
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
		return File{Version: 1}, nil
	}
	if err != nil {
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, err
	}
	return f, nil
}

// Save writes the overrides file atomically: it serializes f to a unique
// sibling temp file in the same directory, fsyncs and closes it, then renames
// it over the destination. On any failure the temp file is removed so a
// partial write never lingers as garbage on disk.
func Save(f File) (retErr error) {
	path, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tf, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := tf.Name()
	// On any failure path remove the temp file; the rename consumes it on
	// success so the Remove becomes a no-op error we ignore.
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmp)
		}
	}()
	if err := tf.Chmod(0o644); err != nil {
		_ = tf.Close()
		return err
	}
	if _, err := tf.Write(data); err != nil {
		_ = tf.Close()
		return err
	}
	if err := tf.Sync(); err != nil {
		_ = tf.Close()
		return err
	}
	if err := tf.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
