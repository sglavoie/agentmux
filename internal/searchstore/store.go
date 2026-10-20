// Package searchstore manages per-key search-text sidecar files, the
// disposable search storage beside a metadata cache. The store derives a
// directory from an index path (stripping the file extension and appending
// ".search") and writes each entry as sha256(key)+".txt" so callers can look
// up content by an opaque string key without coordinating filenames.
package searchstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/atomicfile"
)

// Store wraps the on-disk directory that holds per-key search-text sidecars.
type Store struct {
	dir string
}

// SidecarFile describes one regular hash-named sidecar found by Inventory.
type SidecarFile struct {
	Path string
	Size int64
}

// Inventory summarizes regular hash-named sidecars relative to an expected
// set of opaque keys. Directories, symlinks, temporary files, and unrelated
// artifacts are ignored.
type Inventory struct {
	Count       int
	Size        int64
	MissingKeys []string
	Orphans     []SidecarFile
	OrphanSize  int64
}

// New returns a Store whose directory mirrors the legacy
// transcript.Index.SearchStoreDir derivation: strip the index path's
// extension and append ".search". When the index path has no extension,
// the path is used verbatim as the base — preserved for parity with
// callers that pass an extension-less path.
func New(indexPath string) *Store {
	base := indexPath
	if ext := filepath.Ext(indexPath); ext != "" && len(filepath.Base(indexPath)) > len(ext) {
		base = indexPath[:len(indexPath)-len(ext)]
	}
	return &Store{dir: base + ".search"}
}

// Dir returns the on-disk directory used to hold sidecar files.
func (s *Store) Dir() string { return s.dir }

// Path returns the deterministic sidecar path for an opaque key.
func (s *Store) Path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".txt")
}

// Write atomically writes the sidecar for key (tmp + rename, fsync before
// rename so a crash mid-write never leaves a half-written sidecar in place).
func (s *Store) Write(key, text string) error {
	path := s.Path(key)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// A directory, FIFO, or other non-regular object cannot be atomically
	// replaced by rename on every supported Unix. Treat it as a corrupt cache
	// entry and remove it first. Regular files remain in place until the final
	// rename so readers always see either the old or the new complete content.
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Sidecars are a disposable cache written in bulk: 0600 as before, and no
	// directory fsync per entry, since a rename lost to a power cut only means
	// the sidecar is reparsed from its source.
	return atomicfile.WriteStream(path, 0o600, func(w io.Writer) error {
		_, err := io.WriteString(w, text)
		return err
	}, atomicfile.WithDirSync(atomicfile.SyncDirNone))
}

// Readable reports whether key has a regular sidecar that can be opened for
// reading. It intentionally does not read the entire search corpus; callers
// use it only to decide whether a source reparse is necessary.
func (s *Store) Readable(key string) (bool, error) {
	path := s.Path(key)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	return true, nil
}

// Read returns the sidecar contents for key. A missing sidecar surfaces
// the underlying os.ErrNotExist so callers can branch on it via errors.Is.
func (s *Store) Read(key string) (string, error) {
	path := s.Path(key)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", &os.PathError{Op: "read", Path: path, Err: fs.ErrInvalid}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Exists reports whether the regular sidecar file for key exists. A missing
// file is not an error — it returns (false, nil).
func (s *Store) Exists(key string) (bool, error) {
	info, err := os.Lstat(s.Path(key))
	if err == nil {
		return info.Mode().IsRegular(), nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// Delete removes the sidecar for key. Removing a missing sidecar is not an
// error.
func (s *Store) Delete(key string) error {
	err := os.Remove(s.Path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Inventory classifies regular hashed .txt sidecars against keys expected by
// the current metadata cache. A missing directory is treated as empty. The
// returned orphan paths are safe candidates for explicit maintenance; this
// method never removes files.
func (s *Store) Inventory(keys []string) (Inventory, error) {
	expected := make(map[string]string, len(keys))
	orderedKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		name := filepath.Base(s.Path(key))
		if _, ok := expected[name]; ok {
			continue
		}
		expected[name] = key
		orderedKeys = append(orderedKeys, key)
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Inventory{MissingKeys: orderedKeys}, nil
		}
		return Inventory{}, err
	}

	var inventory Inventory
	found := make(map[string]struct{}, len(expected))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !isSidecarName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return Inventory{}, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		inventory.Count++
		inventory.Size += info.Size()
		if _, ok := expected[entry.Name()]; ok {
			found[entry.Name()] = struct{}{}
			continue
		}
		inventory.Orphans = append(inventory.Orphans, SidecarFile{
			Path: filepath.Join(s.dir, entry.Name()),
			Size: info.Size(),
		})
		inventory.OrphanSize += info.Size()
	}
	for _, key := range orderedKeys {
		name := filepath.Base(s.Path(key))
		if _, ok := found[name]; !ok {
			inventory.MissingKeys = append(inventory.MissingKeys, key)
		}
	}
	return inventory, nil
}

// Stats returns the number and total byte size of regular hashed .txt sidecar
// files in the store's directory.
func (s *Store) Stats() (count int, size int64, err error) {
	inventory, err := s.Inventory(nil)
	if err != nil {
		return 0, 0, err
	}
	return inventory.Count, inventory.Size, nil
}

func isSidecarName(name string) bool {
	if len(name) != sha256.Size*2+len(".txt") || filepath.Ext(name) != ".txt" {
		return false
	}
	for _, r := range name[:sha256.Size*2] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
