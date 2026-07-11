package transcript

import (
	"context"
	"errors"
	"io/fs"
)

// The (*Index).Search* methods are now thin facades over an embedded
// *searchstore.Store. The real I/O lives in internal/searchstore; this file
// preserves the legacy receiver-method signatures so existing callers in
// index.go, search.go, maintenance.go, and prune.go keep compiling unchanged.

// SearchStoreDir returns the directory that stores per-session search text
// sidecars for this index path.
func (i *Index) SearchStoreDir() string { return i.store.Dir() }

// SearchTextPath returns the deterministic sidecar path for an index key.
func (i *Index) SearchTextPath(key string) string { return i.store.Path(key) }

// WriteSearchText atomically writes a per-session search-text sidecar.
func (i *Index) WriteSearchText(key, searchText string) error {
	if repo := i.repository(); repo != nil {
		harness, sessionID, err := splitRepositoryKey(key)
		if err != nil {
			return err
		}
		return repo.SetSearchText(context.Background(), harness, sessionID, searchText)
	}
	return i.store.Write(key, searchText)
}

// ReadSearchText reads a per-session search-text sidecar.
func (i *Index) ReadSearchText(key string) (string, error) {
	if repo := i.repository(); repo != nil {
		harness, sessionID, err := splitRepositoryKey(key)
		if err != nil {
			return "", err
		}
		content, found, err := repo.SearchText(context.Background(), harness, sessionID)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fs.ErrNotExist
		}
		return content, nil
	}
	return i.store.Read(key)
}

// SearchTextExists reports whether a per-session search-text sidecar exists.
func (i *Index) SearchTextExists(key string) (bool, error) {
	if i.UsesSQLite() {
		_, err := i.ReadSearchText(key)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return err == nil, err
	}
	return i.store.Exists(key)
}

// DeleteSearchText removes a per-session search-text sidecar if it exists.
func (i *Index) DeleteSearchText(key string) error {
	if repo := i.repository(); repo != nil {
		harness, sessionID, err := splitRepositoryKey(key)
		if err != nil {
			return err
		}
		return repo.DeleteSearchText(context.Background(), harness, sessionID)
	}
	return i.store.Delete(key)
}

// SearchStoreStats returns the number and total byte size of regular sidecar
// files in the search store directory.
func (i *Index) SearchStoreStats() (count int, size int64, err error) {
	return i.store.Stats()
}
