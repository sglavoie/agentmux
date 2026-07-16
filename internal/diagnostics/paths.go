package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	directoryMode = 0o700
	fileMode      = 0o600
)

// Paths is the resolved private diagnostic filesystem layout.
type Paths struct {
	Root          string
	JournalDir    string
	DaemonDir     string
	NativeDir     string
	CaptureDir    string
	SpoolDir      string
	HMACKey       string
	JournalFile   string
	DaemonLogFile string
}

// DefaultPaths resolves $XDG_STATE_HOME/agent-mux, falling back to
// ~/.local/state/agent-mux when the XDG variable is unset.
func DefaultPaths() (Paths, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			if err == nil {
				err = errors.New("home directory is empty")
			}
			return Paths{}, fmt.Errorf("diagnostics: resolving state directory: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(base) {
		return Paths{}, fmt.Errorf("diagnostics: XDG state directory must be absolute: %s", base)
	}
	return PathsForRoot(filepath.Join(base, "agent-mux")), nil
}

func PathsForRoot(root string) Paths {
	if strings.TrimSpace(root) == "" {
		return Paths{}
	}
	root = filepath.Clean(root)
	paths := Paths{
		Root:       root,
		JournalDir: filepath.Join(root, "journal"),
		DaemonDir:  filepath.Join(root, "daemon"),
		NativeDir:  filepath.Join(root, "native"),
		CaptureDir: filepath.Join(root, "captures"),
		SpoolDir:   filepath.Join(root, "spool"),
		HMACKey:    filepath.Join(root, "diagnostics.key"),
	}
	paths.JournalFile = filepath.Join(paths.JournalDir, "events.jsonl")
	paths.DaemonLogFile = filepath.Join(paths.DaemonDir, "daemon.jsonl")
	return paths
}

// Ensure creates the complete private layout and tightens existing directory
// modes. Symlinks are rejected at every managed leaf.
func (p Paths) Ensure() error {
	if p.Root == "" || !filepath.IsAbs(p.Root) {
		return fmt.Errorf("diagnostics: state root must be an absolute path")
	}
	for label, path := range map[string]string{
		"journal directory": p.JournalDir, "daemon directory": p.DaemonDir,
		"native directory": p.NativeDir, "capture directory": p.CaptureDir,
		"spool directory": p.SpoolDir, "HMAC key": p.HMACKey,
		"journal file": p.JournalFile, "daemon log file": p.DaemonLogFile,
	} {
		if !withinRoot(p.Root, path) {
			return fmt.Errorf("diagnostics: %s is outside state root", label)
		}
	}
	for _, dir := range []string{p.Root, p.JournalDir, p.DaemonDir, p.NativeDir, p.CaptureDir, p.SpoolDir} {
		if err := ensurePrivateDirectory(dir); err != nil {
			return err
		}
	}
	return nil
}

func withinRoot(root, path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ensurePrivateDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("diagnostics: %s is not a real directory", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("diagnostics: inspecting directory %s: %w", path, err)
	}
	if err := os.MkdirAll(path, directoryMode); err != nil {
		return fmt.Errorf("diagnostics: creating directory %s: %w", path, err)
	}
	if err := os.Chmod(path, directoryMode); err != nil {
		return fmt.Errorf("diagnostics: restricting directory %s: %w", path, err)
	}
	return nil
}

func validateRegularTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("diagnostics: %s is not a regular file", path)
	}
	return nil
}
