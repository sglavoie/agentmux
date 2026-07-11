package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func (i *Index) saveLocked() (retErr error) {
	if err := os.MkdirAll(filepath.Dir(i.path), 0o755); err != nil {
		return err
	}
	for key, meta := range i.entries {
		if meta.SearchText == "" {
			continue
		}
		// saveLocked is only used by the legacy path and is called with
		// i.mu already held. Going through WriteSearchText would re-enter
		// the SQLite-aware facade, which needs the same mutex to determine
		// the active backend. Keep legacy persistence direct to its sidecar
		// store instead.
		if err := i.store.Write(key, meta.SearchText); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(indexFile{Entries: metadataOnlyEntries(i.entries)}, "", "  ")
	if err != nil {
		return err
	}
	tmp := i.path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmp)
		}
	}()
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
	if err := os.Rename(tmp, i.path); err != nil {
		return err
	}
	if saveLockedHookForTest != nil {
		saveLockedHookForTest()
	}
	return nil
}

func metadataOnlyEntries(entries map[string]SessionMeta) map[string]SessionMeta {
	out := make(map[string]SessionMeta, len(entries))
	for key, meta := range entries {
		meta.SearchText = ""
		out[key] = meta
	}
	return out
}
