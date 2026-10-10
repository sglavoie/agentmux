package runtimesession

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

// EnvVar is the environment variable a launched agent inherits naming its
// runtime session. Hook clients read it to identify the runtime that produced
// an event without depending on any terminal backend's own variables.
const EnvVar = "AMX_RUNTIME_SESSION_ID"

// DefaultRegistryPath returns the registry file location. It sits beside the
// transcript and plan caches but, unlike them, is authoritative state; it falls
// back under os.TempDir when HOME is unavailable, as the caches do.
func DefaultRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "agent-mux", "runtime-sessions.json")
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "runtime-sessions.json")
}

// Reader answers lookups from the last registry version it loaded. It reloads
// only when the file was replaced since the previous load, so a caller on a
// latency-sensitive path pays one stat for a miss and nothing for a hit. It never writes the registry and never takes its lock:
// atomic replacement means every load sees a complete version.
//
// Reader is safe for concurrent use.
type Reader struct {
	store *Store

	mu      sync.Mutex
	records map[ID]Record
	loaded  bool
	// info is the file version records came from. Atomic replacement gives
	// every write a new file, so identity, size, and modification time
	// together detect a change even within one timestamp tick.
	info fs.FileInfo
	err  error
}

// NewReader returns a reader over store.
func NewReader(store *Store) *Reader {
	return &Reader{store: store}
}

// Lookup returns the record for id. A miss, or a record that is still pending,
// triggers a reload when the file changed, so a hook arriving right after a
// launch commits sees the committed record. The error reports a registry that
// could not be read; the previous good version keeps answering in that case.
func (r *Reader) Lookup(id ID) (Record, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record, ok := r.records[id]; ok && record.State != StatePending {
		return cloneRecord(record), true, nil
	}
	r.refreshLocked()
	record, ok := r.records[id]
	if !ok {
		return Record{}, false, r.err
	}
	return cloneRecord(record), true, r.err
}

// Refresh reloads the registry when the file changed and reports the read
// error, if any. Periodic callers use it so state transitions such as End are
// observed even for IDs that keep hitting the cache.
func (r *Reader) Refresh() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	return r.err
}

func (r *Reader) refreshLocked() {
	info, err := os.Stat(r.store.Path())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.records, r.loaded, r.info, r.err = nil, true, nil, nil
		return
	case err != nil:
		r.err = err
		return
	}
	if r.loaded && r.err == nil && r.info != nil && os.SameFile(r.info, info) &&
		info.Size() == r.info.Size() && info.ModTime().Equal(r.info.ModTime()) {
		return
	}
	registry, err := r.store.Load()
	if err != nil {
		r.err = err
		return
	}
	records := make(map[ID]Record, len(registry.sessions))
	maps.Copy(records, registry.sessions)
	r.records, r.loaded, r.info, r.err = records, true, info, nil
}

// Live returns every live record of the current registry version, in no
// particular order. Like Lookup it keeps answering from the previous good
// version when the file cannot be read, and reports that error.
func (r *Reader) Live() ([]Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	var live []Record
	for _, record := range r.records {
		if record.State == StateLive {
			live = append(live, cloneRecord(record))
		}
	}
	return live, r.err
}
