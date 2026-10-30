package transcript

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
	err := i.store.Write(key, searchText)
	i.searchCache.invalidate(key)
	return err
}

// ReadSearchText reads a per-session search-text sidecar.
func (i *Index) ReadSearchText(key string) (string, error) {
	return i.store.Read(key)
}

// SearchTextExists reports whether a per-session search-text sidecar exists.
func (i *Index) SearchTextExists(key string) (bool, error) {
	return i.store.Exists(key)
}

// DeleteSearchText removes a per-session search-text sidecar if it exists.
func (i *Index) DeleteSearchText(key string) error {
	err := i.store.Delete(key)
	i.searchCache.invalidate(key)
	return err
}

// persistSearchText clears pending state only after the sidecar matches the
// current corpus. An empty pending corpus removes the obsolete sidecar.
func (i *Index) persistSearchText(key string, meta *SessionMeta) error {
	var err error
	if meta.SearchText == "" {
		err = i.DeleteSearchText(key)
	} else {
		err = i.WriteSearchText(key, meta.SearchText)
	}
	meta.SearchTextPending = err != nil
	if err == nil {
		meta.SearchText = ""
	}
	return err
}

// SearchStoreStats returns the number and total byte size of regular sidecar
// files in the search store directory.
func (i *Index) SearchStoreStats() (count int, size int64, err error) {
	return i.store.Stats()
}
