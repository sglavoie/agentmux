package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// SearchStoreDir returns the directory that stores per-session search text
// sidecars for this index path.
func (i *Index) SearchStoreDir() string {
	base := i.path[:len(i.path)-len(filepath.Ext(i.path))]
	if base == "" {
		base = i.path
	}
	return base + ".search"
}

// SearchTextPath returns the deterministic sidecar path for an index key.
func (i *Index) SearchTextPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(i.SearchStoreDir(), hex.EncodeToString(sum[:])+".txt")
}

// WriteSearchText atomically writes a per-session search-text sidecar.
func (i *Index) WriteSearchText(key, searchText string) (retErr error) {
	path := i.SearchTextPath(key)
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
	if _, err := tf.WriteString(searchText); err != nil {
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

// ReadSearchText reads a per-session search-text sidecar.
func (i *Index) ReadSearchText(key string) (string, error) {
	data, err := os.ReadFile(i.SearchTextPath(key))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SearchTextExists reports whether a per-session search-text sidecar exists.
func (i *Index) SearchTextExists(key string) (bool, error) {
	_, err := os.Stat(i.SearchTextPath(key))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// DeleteSearchText removes a per-session search-text sidecar if it exists.
func (i *Index) DeleteSearchText(key string) error {
	err := os.Remove(i.SearchTextPath(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// SearchStoreStats returns the number and total byte size of regular sidecar
// files in the search store directory.
func (i *Index) SearchStoreStats() (count int, size int64, err error) {
	dir := i.SearchStoreDir()
	entries, err := os.ReadDir(dir)
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
