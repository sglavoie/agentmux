// Package searchstore manages per-key search-text sidecar files. The store
// derives a directory from an index path (stripping the file extension and
// appending ".search") and writes each entry as sha256(key)+".txt" so callers
// can look up content by an opaque string key without coordinating filenames.
package searchstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Store wraps the on-disk directory that holds per-key search-text sidecars.
type Store struct {
	dir string
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
func (s *Store) Write(key, text string) (retErr error) {
	path := s.Path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := tf.WriteString(text); err != nil {
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

// Read returns the sidecar contents for key. A missing sidecar surfaces
// the underlying os.ErrNotExist so callers can branch on it via errors.Is.
func (s *Store) Read(key string) (string, error) {
	data, err := os.ReadFile(s.Path(key))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Exists reports whether the sidecar for key exists. A missing file is not
// an error — it returns (false, nil).
func (s *Store) Exists(key string) (bool, error) {
	_, err := os.Stat(s.Path(key))
	if err == nil {
		return true, nil
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

// Stats returns the number and total byte size of regular .txt sidecar
// files in the store's directory. A missing directory is treated as empty
// (returns 0, 0, nil) rather than an error so a freshly constructed Store
// whose directory has not been created yet is reported as empty.
func (s *Store) Stats() (count int, size int64, err error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".txt" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, 0, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		count++
		size += info.Size()
	}
	return count, size, nil
}
